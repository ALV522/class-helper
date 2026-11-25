package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const sampleRate = 16000
const audioChunkSeconds = 12
const asrURL = "https://api.deepseek.com/v1/tools/asr"
const chatURL = "https://api.deepseek.com/chat/completions"

var apiClient = &http.Client{Timeout: 70 * time.Second}

type SessionMeta struct {
	Course       string  `json:"course"`
	Created      string  `json:"created"`
	LastSaved    string  `json:"last_saved"`
	Recorder     string  `json:"recorder"`
	AudioSeconds float64 `json:"audio_seconds"`
	Version      int     `json:"version"`
}

func newSession(base, course string) (string, *SessionMeta, error) {
	folder := filepath.Join(base, "sessions", time.Now().Format("20060102_150405"))
	for n := 1; ; n++ {
		if _, err := os.Stat(folder); errors.Is(err, os.ErrNotExist) {
			break
		}
		folder = filepath.Join(base, "sessions", fmt.Sprintf("%s_%d", time.Now().Format("20060102_150405"), n))
	}
	for _, sub := range []string{"", "images", "chunks"} {
		if err := os.MkdirAll(filepath.Join(folder, sub), 0700); err != nil {
			return "", nil, err
		}
	}
	m := &SessionMeta{Course: course, Created: time.Now().Format(time.RFC3339), Recorder: "Windows waveIn 16kHz mono PCM", Version: 1}
	return folder, m, saveJSON(filepath.Join(folder, "session.json"), m)
}

func saveJSON(path string, data any) error {
	b, e := json.MarshalIndent(data, "", "  ")
	if e != nil {
		return e
	}
	return atomicWrite(path, append(b, '\n'))
}
func atomicWrite(path string, b []byte) error {
	tmp := path + ".tmp"
	if e := os.WriteFile(tmp, b, 0600); e != nil {
		return e
	}
	if e := os.Rename(tmp, path); e != nil {
		_ = os.Remove(tmp)
		return e
	}
	return nil
}

func wavHeader(pcmLength uint32) []byte {
	h := make([]byte, 44)
	copy(h[:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], pcmLength+36)
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], sampleRate)
	binary.LittleEndian.PutUint32(h[28:], sampleRate*2)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:], pcmLength)
	return h
}
func wavBytes(pcm []byte) []byte { return append(wavHeader(uint32(len(pcm))), pcm...) }

func transcribe(ctx context.Context, key string, wav []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "segment.wav")
	if err != nil {
		return "", err
	}
	if _, err = part.Write(wav); err != nil {
		return "", err
	}
	if err = w.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", asrURL, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := apiClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("ASR HTTP %d: %s", resp.StatusCode, truncate(string(data), 260))
	}
	var result struct {
		Text string `json:"text"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Text), nil
}

func askDeepSeek(ctx context.Context, key, prompt string) (string, error) {
	body := map[string]any{"model": "deepseek-flash", "temperature": 0.25, "messages": []map[string]string{
		{"role": "system", "content": "你是严谨的大学课堂笔记助理。仅根据用户提供的课堂文字整理内容，保持概念、公式和不确定性，不编造老师未讲述的信息。用中文 Markdown 输出，结构自然清晰。"},
		{"role": "user", "content": prompt},
	}}
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := apiClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("AI HTTP %d: %s", resp.StatusCode, truncate(string(data), 300))
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err = json.Unmarshal(data, &result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		return "", errors.New("AI 返回内容为空")
	}
	return result.Choices[0].Message.Content, nil
}
func summarize(ctx context.Context, key, course, transcript string) (string, error) {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" {
		return "", errors.New("转写为空：先录制并完成转写")
	}
	r := []rune(transcript)
	if len(r) <= 15000 {
		return askDeepSeek(ctx, key, "课程："+course+"\n请梳理本节课的核心内容、重要概念和论证过程、待核对的概念、复习重点。不要添加原文没有的知识。\n\n课堂转写：\n"+transcript)
	}
	var parts []string
	for i := 0; i < len(r); i += 12000 {
		end := i + 12000
		if end > len(r) {
			end = len(r)
		}
		draft, err := askDeepSeek(ctx, key, fmt.Sprintf("这是课程《%s》的课堂转写第 %d 部分。提炼核心内容，保留重要术语和论证顺序，避免补充原文之外的信息：\n%s", course, len(parts)+1, string(r[i:end])))
		if err != nil {
			return "", err
		}
		parts = append(parts, draft)
	}
	return askDeepSeek(ctx, key, "请整合以下分段课堂记录，形成一份完整有逻辑的中文课堂笔记，并区分确定信息与转写中可能有误的信息：\n\n"+strings.Join(parts, "\n\n---\n\n"))
}
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func packageSession(folder, dest string) (err error) {
	// ZIP includes only classroom files and never any API credentials.
	paths := make([]string, 0, 16)
	if err = filepath.Walk(folder, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(info.Name(), ".tmp") || strings.HasSuffix(info.Name(), ".key") {
			return nil
		}
		if filepath.Clean(p) != filepath.Clean(dest) {
			paths = append(paths, p)
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Strings(paths)
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(dest)
		}
	}()
	zw := zip.NewWriter(out)
	for _, p := range paths {
		rel, e := filepath.Rel(folder, p)
		if e != nil {
			err = e
			break
		}
		info, e := os.Stat(p)
		if e != nil {
			err = e
			break
		}
		h, e := zip.FileInfoHeader(info)
		if e != nil {
			err = e
			break
		}
		h.Name = filepath.ToSlash(rel)
		h.Method = zip.Deflate
		writer, e := zw.CreateHeader(h)
		if e != nil {
			err = e
			break
		}
		input, e := os.Open(p)
		if e != nil {
			err = e
			break
		}
		_, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			err = copyErr
			break
		}
		if closeErr != nil {
			err = closeErr
			break
		}
	}
	zipErr := zw.Close()
	fileErr := out.Close()
	if err != nil {
		return err
	}
	if zipErr != nil {
		return zipErr
	}
	return fileErr
}

// chunkFileNames returns pending audio chunks not acknowledged by a successful ASR request.
func chunkFileNames(folder string) ([]string, error) {
	fs, err := filepath.Glob(filepath.Join(folder, "chunks", "*.wav"))
	if err != nil {
		return nil, err
	}
	sort.Strings(fs)
	res := make([]string, 0, len(fs))
	for _, p := range fs {
		if _, err := os.Stat(p + ".done"); errors.Is(err, os.ErrNotExist) {
			res = append(res, p)
		}
	}
	return res, nil
}
