//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

const (
	wmCreate           = 0x0001
	wmDestroy          = 0x0002
	wmSize             = 0x0005
	wmClose            = 0x0010
	wmCommand          = 0x0111
	wmTimer            = 0x0113
	wmSetFont          = 0x0030
	wmGetText          = 0x000D
	wmGetTextLength    = 0x000E
	wmSetText          = 0x000C
	wmPaste            = 0x0302
	emSetSel           = 0x00B1
	emGetSel           = 0x00B0
	emReplaceSel       = 0x00C2
	emStreamIn         = 0x0449
	emStreamOut        = 0x044A
	wsVisible          = 0x10000000
	wsChild            = 0x40000000
	wsOverlappedWindow = 0x00CF0000
	wsVScroll          = 0x00200000
	wsHScroll          = 0x00100000
	wsClipChildren     = 0x02000000
	esMultiline        = 0x0004
	esAutoVScroll      = 0x0040
	esWantReturn       = 0x1000
	esReadOnly         = 0x0800
	esPassword         = 0x0020
	bsPushbutton       = 0
	swShow             = 5
	cfDIB              = 8
	gmemMoveable       = 2
	ofnFileMustExist   = 0x1000
	ofnPathMustExist   = 0x800
	sfRTF              = 2
	gmEMGetText        = 0x000D
	idNew              = 100
	idStart            = 101
	idPause            = 102
	idStop             = 103
	idImage            = 104
	idPaste            = 105
	idAI               = 106
	idExport           = 107
	idOpen             = 108
	idRetry            = 109
	idFolder           = 110
)

var (
	kernel           = syscall.NewLazyDLL("kernel32.dll")
	user             = syscall.NewLazyDLL("user32.dll")
	gdi              = syscall.NewLazyDLL("gdi32.dll")
	shell            = syscall.NewLazyDLL("shell32.dll")
	comdlg           = syscall.NewLazyDLL("comdlg32.dll")
	ole              = syscall.NewLazyDLL("ole32.dll")
	winmm            = syscall.NewLazyDLL("winmm.dll")
	pCreateWindow    = user.NewProc("CreateWindowExW")
	pDefWindow       = user.NewProc("DefWindowProcW")
	pRegisterClass   = user.NewProc("RegisterClassExW")
	pSend            = user.NewProc("SendMessageW")
	pMove            = user.NewProc("MoveWindow")
	pGetText         = user.NewProc("GetWindowTextW")
	pGetTextLen      = user.NewProc("GetWindowTextLengthW")
	pSetText         = user.NewProc("SetWindowTextW")
	pShowWindow      = user.NewProc("ShowWindow")
	pMsgBox          = user.NewProc("MessageBoxW")
	pPostQuit        = user.NewProc("PostQuitMessage")
	pGlobalAlloc     = kernel.NewProc("GlobalAlloc")
	pGlobalLock      = kernel.NewProc("GlobalLock")
	pGlobalUnlock    = kernel.NewProc("GlobalUnlock")
	pGlobalFree      = kernel.NewProc("GlobalFree")
	pOpenClip        = user.NewProc("OpenClipboard")
	pEmptyClip       = user.NewProc("EmptyClipboard")
	pSetClip         = user.NewProc("SetClipboardData")
	pCloseClip       = user.NewProc("CloseClipboard")
	pGetOpenFileName = comdlg.NewProc("GetOpenFileNameW")
	pWaveOpen        = winmm.NewProc("waveInOpen")
	pWavePrepare     = winmm.NewProc("waveInPrepareHeader")
	pWaveAdd         = winmm.NewProc("waveInAddBuffer")
	pWaveStart       = winmm.NewProc("waveInStart")
	pWaveStop        = winmm.NewProc("waveInStop")
	pWaveReset       = winmm.NewProc("waveInReset")
	pWaveUnprepare   = winmm.NewProc("waveInUnprepareHeader")
	pWaveClose       = winmm.NewProc("waveInClose")
)

func u(s string) *uint16                             { return syscall.StringToUTF16Ptr(s) }
func call(p *syscall.LazyProc, a ...uintptr) uintptr { r, _, _ := p.Call(a...); return r }
func msg(hwnd uintptr, s string) {
	call(pMsgBox, hwnd, uintptr(unsafe.Pointer(u(s))), uintptr(unsafe.Pointer(u("课堂助手"))), 0x40)
}
func send(h, m, w, l uintptr) uintptr { return call(pSend, h, m, w, l) }
func setText(h uintptr, s string)     { call(pSetText, h, uintptr(unsafe.Pointer(u(s)))) }
func getText(h uintptr) string {
	n := int(call(pGetTextLen, h))
	if n <= 0 {
		return ""
	}
	b := make([]uint16, n+2)
	call(pGetText, h, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	return syscall.UTF16ToString(b)
}
func create(class, name string, style uintptr, x, y, w, h int, parent uintptr, id int) uintptr {
	return call(pCreateWindow, 0, uintptr(unsafe.Pointer(u(class))), uintptr(unsafe.Pointer(u(name))), style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), parent, uintptr(id), 0, 0)
}
func move(h uintptr, x, y, w, hh int) {
	if w < 4 {
		w = 4
	}
	if hh < 4 {
		hh = 4
	}
	call(pMove, h, uintptr(x), uintptr(y), uintptr(w), uintptr(hh), 1)
}

// OPENFILENAMEW matches the Windows API, including pointer alignment on amd64.
type openFileName struct {
	size          uint32
	owner         uintptr
	instance      uintptr
	filter        uintptr
	customFilter  uintptr
	maxCustFilter uint32
	filterIndex   uint32
	file          uintptr
	maxFile       uint32
	fileTitle     uintptr
	maxFileTitle  uint32
	initialDir    uintptr
	title         uintptr
	flags         uint32
	fileOffset    uint16
	fileExtension uint16
	defExt        uintptr
	custData      uintptr
	hook          uintptr
	templateName  uintptr
	reserved      uintptr
	reserved2     uint32
	flagsEx       uint32
}

func selectFile(filter, title string) string {
	buf := make([]uint16, 2048)
	f := append(utf16.Encode([]rune(filter)), 0) // Win32 filter needs embedded NUL separators.
	o := openFileName{file: uintptr(unsafe.Pointer(&buf[0])), maxFile: uint32(len(buf)), filter: uintptr(unsafe.Pointer(&f[0])), title: uintptr(unsafe.Pointer(u(title))), flags: ofnPathMustExist | ofnFileMustExist, owner: app.hwnd}
	o.size = uint32(unsafe.Sizeof(o))
	if call(pGetOpenFileName, uintptr(unsafe.Pointer(&o))) == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

type winClass struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	inst       uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   uintptr
	className  uintptr
	iconSm     uintptr
}
type winMsg struct {
	hwnd    uintptr
	message uint32
	_       uint32
	wparam  uintptr
	lparam  uintptr
	time    uint32
	ptX     int32
	ptY     int32
	private uint32
}
type editStream struct {
	cookie    uintptr
	errorCode uint32
	_         uint32
	callback  uintptr
}

type result struct {
	kind    string
	text    string
	err     error
	segment int
	start   float64
	path    string
}
type asrJob struct {
	index int
	start float64
	path  string
	key   string
}
type waveFormat struct {
	tag        uint16
	channels   uint16
	rate       uint32
	avgRate    uint32
	blockAlign uint16
	bits       uint16
	extra      uint16
}
type waveHdr struct {
	data     uintptr
	length   uint32
	recorded uint32
	user     uintptr
	flags    uint32
	loops    uint32
	next     uintptr
	reserved uintptr
}
type waveBuffer struct {
	data []byte
	hdr  waveHdr
}
type recorder struct {
	handle     uintptr
	buffers    []*waveBuffer
	file       *os.File
	total      uint32
	chunk      []byte
	chunkStart float64
	chunkIndex int
	started    time.Time
	paused     bool
}
type application struct {
	hwnd, notes, transcript, summary, course, key, status uintptr
	notesLabel, transcriptLabel, summaryLabel             uintptr
	controls                                              map[int]uintptr
	font                                                  uintptr
	base, folder                                          string
	meta                                                  *SessionMeta
	rec                                                   *recorder
	results                                               chan result
	jobs                                                  chan asrJob
	pending                                               int
	queued                                                map[string]bool
	lastSave                                              time.Time
	mu                                                    sync.Mutex
}

var app = &application{controls: map[int]uintptr{}, results: make(chan result, 128), jobs: make(chan asrJob, 256), queued: map[string]bool{}}

func notesRTF() ([]byte, error) {
	var out bytes.Buffer
	cb := syscall.NewCallback(func(cookie, data, count, written uintptr) uintptr {
		b := unsafe.Slice((*byte)(unsafe.Pointer(data)), int(count))
		n, e := out.Write(b)
		*(*uint32)(unsafe.Pointer(written)) = uint32(n)
		if e != nil {
			return 1
		}
		return 0
	})
	es := editStream{callback: cb}
	send(app.notes, emStreamOut, sfRTF, uintptr(unsafe.Pointer(&es)))
	runtime.KeepAlive(cb)
	if es.errorCode != 0 {
		return nil, fmt.Errorf("保存 RTF 失败: %d", es.errorCode)
	}
	return out.Bytes(), nil
}
func loadRTF(data []byte) error {
	r := bytes.NewReader(data)
	cb := syscall.NewCallback(func(cookie, p, count, written uintptr) uintptr {
		b := unsafe.Slice((*byte)(unsafe.Pointer(p)), int(count))
		n, _ := r.Read(b)
		*(*uint32)(unsafe.Pointer(written)) = uint32(n)
		return 0
	})
	es := editStream{callback: cb}
	send(app.notes, emStreamIn, sfRTF, uintptr(unsafe.Pointer(&es)))
	runtime.KeepAlive(cb)
	if es.errorCode != 0 {
		return fmt.Errorf("读取 RTF 失败: %d", es.errorCode)
	}
	return nil
}
func saveSession() error {
	if app.folder == "" {
		return nil
	}
	if b, e := notesRTF(); e == nil {
		if e = os.WriteFile(filepath.Join(app.folder, "notes.rtf"), b, 0600); e != nil {
			return e
		}
	} else {
		return e
	}
	if e := atomicWrite(filepath.Join(app.folder, "transcript.txt"), []byte(getText(app.transcript))); e != nil {
		return e
	}
	if e := atomicWrite(filepath.Join(app.folder, "summary.md"), []byte(getText(app.summary))); e != nil {
		return e
	}
	app.meta.Course = strings.TrimSpace(getText(app.course))
	app.meta.LastSaved = time.Now().Format(time.RFC3339)
	if app.rec != nil {
		app.meta.AudioSeconds = float64(app.rec.total) / float64(sampleRate*2)
	}
	return saveJSON(filepath.Join(app.folder, "session.json"), app.meta)
}
func status(s string) { setText(app.status, s) }
func refreshTitle()   { setText(app.hwnd, "课堂助手 Class Helper  |  "+getText(app.course)) }
func newClass() {
	if app.rec != nil || app.pending > 0 {
		msg(app.hwnd, "请先结束录音并等待正在进行的转写结束")
		return
	}
	if e := saveSession(); e != nil {
		msg(app.hwnd, e.Error())
	}
	dir, m, e := newSession(app.base, "未命名课程")
	if e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	app.folder, app.meta = dir, m
	setText(app.course, "未命名课程")
	setText(app.transcript, "")
	setText(app.summary, "")
	setText(app.notes, "")
	app.pending = 0
	refreshTitle()
	status("新课堂已创建。可开始录音、记笔记、Ctrl+V 粘贴图片。")
}
func loadClass() {
	if app.rec != nil || app.pending > 0 {
		msg(app.hwnd, "请先结束录音并等待正在进行的转写结束")
		return
	}
	p := selectFile("课堂文件 (session.json)\x00session.json\x00JSON 文件\x00*.json\x00\x00", "选择课堂中的 session.json")
	if p == "" {
		return
	}
	data, e := os.ReadFile(p)
	if e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	var m SessionMeta
	if e = json.Unmarshal(data, &m); e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	if e := saveSession(); e != nil {
		msg(app.hwnd, "之前课堂保存失败: "+e.Error())
		return
	}
	app.folder = filepath.Dir(p)
	app.meta = &m
	setText(app.course, m.Course)
	if b, e := os.ReadFile(filepath.Join(app.folder, "notes.rtf")); e == nil {
		_ = loadRTF(b)
	} else {
		setText(app.notes, "")
	}
	if b, e := os.ReadFile(filepath.Join(app.folder, "transcript.txt")); e == nil {
		setText(app.transcript, string(b))
	} else {
		setText(app.transcript, "")
	}
	if b, e := os.ReadFile(filepath.Join(app.folder, "summary.md")); e == nil {
		setText(app.summary, string(b))
	} else {
		setText(app.summary, "")
	}
	refreshTitle()
	status("课堂已打开：" + app.folder)
}

func wavPatch(f *os.File, n uint32) error {
	if _, e := f.Seek(0, 0); e != nil {
		return e
	}
	if _, e := f.Write(wavHeader(n)); e != nil {
		return e
	}
	if _, e := f.Seek(0, 2); e != nil {
		return e
	}
	return f.Sync()
}
func startRecord() {
	if app.rec != nil {
		return
	}
	if app.folder == "" {
		newClass()
	}
	p := filepath.Join(app.folder, "audio.wav")
	if _, exists := os.Stat(p); exists == nil {
		msg(app.hwnd, "当前课堂已经保存有录音。为防止覆盖，请点「新建课堂」开始新的录制。")
		return
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	if _, e = f.Write(wavHeader(0)); e != nil {
		f.Close()
		msg(app.hwnd, e.Error())
		return
	}
	r := &recorder{file: f, started: time.Now()}
	format := waveFormat{tag: 1, channels: 1, rate: sampleRate, avgRate: sampleRate * 2, blockAlign: 2, bits: 16}
	code, _, _ := pWaveOpen.Call(uintptr(unsafe.Pointer(&r.handle)), uintptr(^uint32(0)), uintptr(unsafe.Pointer(&format)), 0, 0, 0)
	if code != 0 {
		f.Close()
		msg(app.hwnd, fmt.Sprintf("无法打开麦克风 (waveIn 错误 %d)。请检查麦克风权限和默认录音设备。", code))
		return
	}
	for i := 0; i < 6; i++ {
		b := &waveBuffer{data: make([]byte, sampleRate)} // 0.5 second of 16 kHz 16-bit mono
		b.hdr = waveHdr{data: uintptr(unsafe.Pointer(&b.data[0])), length: uint32(len(b.data))}
		if rc, _, _ := pWavePrepare.Call(r.handle, uintptr(unsafe.Pointer(&b.hdr)), unsafe.Sizeof(b.hdr)); rc != 0 {
			closeRecorder(r)
			msg(app.hwnd, fmt.Sprintf("麦克风缓冲区配置失败: %d", rc))
			return
		}
		r.buffers = append(r.buffers, b)
		if rc, _, _ := pWaveAdd.Call(r.handle, uintptr(unsafe.Pointer(&b.hdr)), unsafe.Sizeof(b.hdr)); rc != 0 {
			closeRecorder(r)
			msg(app.hwnd, fmt.Sprintf("麦克风缓冲区提交失败: %d", rc))
			return
		}
	}
	if rc, _, _ := pWaveStart.Call(r.handle); rc != 0 {
		closeRecorder(r)
		msg(app.hwnd, fmt.Sprintf("麦克风启动失败: %d", rc))
		return
	}
	app.rec = r
	status("正在录音 · 自动保存 WAV · 每 12 秒上传一次短音频（需 DeepSeek API Key）")
}
func closeRecorder(r *recorder) {
	if r == nil {
		return
	}
	if r.handle != 0 {
		pWaveStop.Call(r.handle)
		pWaveReset.Call(r.handle)
		for _, b := range r.buffers {
			pWaveUnprepare.Call(r.handle, uintptr(unsafe.Pointer(&b.hdr)), unsafe.Sizeof(b.hdr))
		}
		pWaveClose.Call(r.handle)
		r.handle = 0
	}
	if r.file != nil {
		_ = wavPatch(r.file, r.total)
		_ = r.file.Close()
		r.file = nil
	}
}
func processWaves() {
	r := app.rec
	if r == nil || r.paused {
		return
	}
	for _, b := range r.buffers {
		if b.hdr.flags&1 == 0 {
			continue
		} // WHDR_DONE
		n := int(b.hdr.recorded)
		if n > len(b.data) {
			n = len(b.data)
		}
		if n > 0 {
			if _, e := r.file.Write(b.data[:n]); e != nil {
				status("录音写入失败：" + e.Error())
				stopRecord()
				return
			}
			r.total += uint32(n)
			r.chunk = append(r.chunk, b.data[:n]...)
			if len(r.chunk) >= sampleRate*2*audioChunkSeconds {
				submitChunk(r)
			}
		}
		b.hdr.recorded = 0
		b.hdr.flags = 0
		if rc, _, _ := pWaveAdd.Call(r.handle, uintptr(unsafe.Pointer(&b.hdr)), unsafe.Sizeof(b.hdr)); rc != 0 {
			status(fmt.Sprintf("音频缓冲区错误: %d", rc))
			stopRecord()
			return
		}
	}
}
func submitChunk(r *recorder) {
	if len(r.chunk) < sampleRate*2*2 {
		return
	} // skip tiny residual clips
	data := append([]byte(nil), r.chunk...)
	r.chunk = nil
	idx := r.chunkIndex
	r.chunkIndex++
	p := filepath.Join(app.folder, "chunks", fmt.Sprintf("%05d.wav", idx))
	start := float64(r.total-uint32(len(data))) / float64(sampleRate*2)
	_ = os.WriteFile(p+".start", []byte(fmt.Sprintf("%.3f", start)), 0600)
	if e := os.WriteFile(p, wavBytes(data), 0600); e != nil {
		status("缓存音频失败：" + e.Error())
		return
	}
	key := strings.TrimSpace(getText(app.key))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	if key == "" {
		status("录音已保存；输入 API Key 后点「补转写」即可处理音频片段。")
		return
	}
	app.pending++
	select {
	case app.jobs <- asrJob{index: idx, start: start, path: p, key: key}:
		app.queued[p] = true
	default:
		app.pending--
		status("转写队列已满；音频已保留，稍后使用「补转写」。")
	}
}
func pauseRecord() {
	r := app.rec
	if r == nil {
		return
	}
	if r.paused {
		r.paused = false
		pWaveStart.Call(r.handle)
		status("已继续录音")
	} else {
		pWaveStop.Call(r.handle)
		processWaves()
		r.paused = true
		status("录音已暂停（仍可编辑笔记）")
	}
}
func stopRecord() {
	r := app.rec
	if r == nil {
		return
	}
	pWaveStop.Call(r.handle)
	pWaveReset.Call(r.handle)
	// Reset marks returned buffers done; do not requeue them as we're stopping.
	for _, b := range r.buffers {
		if b.hdr.flags&1 != 0 && b.hdr.recorded > 0 {
			n := int(b.hdr.recorded)
			if n > len(b.data) {
				n = len(b.data)
			}
			_, _ = r.file.Write(b.data[:n])
			r.total += uint32(n)
			r.chunk = append(r.chunk, b.data[:n]...)
		}
	}
	submitChunk(r)
	closeRecorder(r)
	app.meta.AudioSeconds = float64(r.total) / float64(sampleRate*2)
	app.rec = nil
	_ = saveSession()
	status(fmt.Sprintf("录音完成：%.0f 秒，文件已保存到 %s", app.meta.AudioSeconds, app.folder))
}
func appendTranscript(start float64, s string) {
	if strings.TrimSpace(s) == "" {
		return
	}
	var lo, hi uint32
	send(app.transcript, emGetSel, uintptr(unsafe.Pointer(&lo)), uintptr(unsafe.Pointer(&hi)))
	length := send(app.transcript, wmGetTextLength, 0, 0)
	send(app.transcript, emSetSel, length, length)
	t := fmt.Sprintf("[%02d:%02d:%02d] %s\r\n\r\n", int(start)/3600, int(start)/60%60, int(start)%60, strings.TrimSpace(s))
	send(app.transcript, emReplaceSel, 1, uintptr(unsafe.Pointer(u(t))))
	send(app.transcript, emSetSel, uintptr(lo), uintptr(hi))
}
func retryChunks() {
	if app.folder == "" {
		return
	}
	key := strings.TrimSpace(getText(app.key))
	if key == "" {
		key = os.Getenv("DEEPSEEK_API_KEY")
	}
	if key == "" {
		msg(app.hwnd, "请先输入 DeepSeek API Key。密钥只在内存中使用，不写入课程文件。")
		return
	}
	paths, e := chunkFileNames(app.folder)
	if e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	queued := 0
	for _, p := range paths {
		if app.queued[p] {
			continue
		}
		var i int
		_, _ = fmt.Sscanf(strings.TrimSuffix(filepath.Base(p), ".wav"), "%d", &i)
		start := float64(i * audioChunkSeconds)
		if b, e := os.ReadFile(p + ".start"); e == nil {
			fmt.Sscanf(string(b), "%f", &start)
		}
		select {
		case app.jobs <- asrJob{index: i, start: start, path: p, key: key}:
			queued++
			app.queued[p] = true
		default:
			break
		}
	}
	app.pending += queued
	status(fmt.Sprintf("已将 %d 段待识别音频加入队列", queued))
}
func worker() {
	for j := range app.jobs {
		b, e := os.ReadFile(j.path)
		if e != nil {
			app.results <- result{kind: "asr", err: e, path: j.path}
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 65*time.Second)
		s, e := transcribe(ctx, j.key, b)
		cancel()
		app.results <- result{kind: "asr", text: s, err: e, path: j.path, start: j.start, segment: j.index}
	}
}
func doSummary() {
	key := strings.TrimSpace(getText(app.key))
	if key == "" {
		key = os.Getenv("DEEPSEEK_API_KEY")
	}
	if key == "" {
		msg(app.hwnd, "请输入 DeepSeek API Key 后再生成 AI 总结。")
		return
	}
	transcript := getText(app.transcript)
	if strings.TrimSpace(transcript) == "" {
		msg(app.hwnd, "转写内容为空，无法生成总结。")
		return
	}
	title := getText(app.course)
	status("DeepSeek 正在整理课堂内容；录音和编辑不受影响……")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		s, e := summarize(ctx, key, title, transcript)
		app.results <- result{kind: "summary", text: s, err: e}
	}()
}
func pollResults() {
	for i := 0; i < 30; i++ {
		select {
		case r := <-app.results:
			if r.kind == "summary" {
				if r.err != nil {
					status("AI 总结失败：" + r.err.Error())
				} else {
					setText(app.summary, r.text)
					status("AI 总结已生成并将在自动保存时写入 summary.md")
				}
			}
			if r.kind == "asr" {
				if app.pending > 0 {
					app.pending--
				}
				if r.err != nil {
					status("转写失败（可点补转写重试）：" + r.err.Error())
				} else {
					appendTranscript(r.start, r.text)
					_ = os.WriteFile(r.path+".done", []byte(time.Now().Format(time.RFC3339)), 0600)
					status(fmt.Sprintf("已转写第 %d 段；剩余队列约 %d 段", r.segment+1, app.pending))
				}
			}
		default:
			return
		}
	}
}

func pasteDIB(img image.Image) error {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return errors.New("图片尺寸无效")
	}
	// Bound size of pasted slides to limit the RTF document size.
	scale := math.Min(1, math.Min(1250/float64(w), 900/float64(h)))
	dstW, dstH := int(math.Max(1, float64(w)*scale)), int(math.Max(1, float64(h)*scale))
	stride := (dstW*3 + 3) &^ 3
	buf := make([]byte, 40+stride*dstH)
	put := func(off int, v uint32) {
		buf[off] = byte(v)
		buf[off+1] = byte(v >> 8)
		buf[off+2] = byte(v >> 16)
		buf[off+3] = byte(v >> 24)
	}
	put(0, 40)
	put(4, uint32(dstW))
	put(8, uint32(dstH))
	buf[12] = 1
	buf[14] = 24
	put(20, uint32(stride*dstH))
	for y := 0; y < dstH; y++ {
		sy := b.Min.Y + int(float64(y)/scale)
		for x := 0; x < dstW; x++ {
			sx := b.Min.X + int(float64(x)/scale)
			r, g, bl, _ := img.At(sx, sy).RGBA()
			off := 40 + (dstH-1-y)*stride + x*3
			buf[off] = byte(bl >> 8)
			buf[off+1] = byte(g >> 8)
			buf[off+2] = byte(r >> 8)
		}
	}
	mem := call(pGlobalAlloc, gmemMoveable, uintptr(len(buf)))
	if mem == 0 {
		return errors.New("图片内存分配失败")
	}
	p := call(pGlobalLock, mem)
	if p == 0 {
		call(pGlobalFree, mem)
		return errors.New("图片内存锁定失败")
	}
	copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), len(buf)), buf)
	call(pGlobalUnlock, mem)
	if call(pOpenClip, app.hwnd) == 0 {
		call(pGlobalFree, mem)
		return errors.New("无法打开系统剪贴板")
	}
	call(pEmptyClip)
	ok := call(pSetClip, cfDIB, mem)
	call(pCloseClip)
	if ok == 0 {
		call(pGlobalFree, mem)
		return errors.New("图片写入剪贴板失败")
	}
	send(app.notes, wmPaste, 0, 0)
	return nil
}
func addImage() {
	p := selectFile("图片文件\x00*.png;*.jpg;*.jpeg\x00所有文件\x00*.*\x00\x00", "选择需要插入笔记的 PPT 图片")
	if p == "" {
		return
	}
	f, e := os.Open(p)
	if e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	defer f.Close()
	im, _, e := image.Decode(f)
	if e != nil {
		msg(app.hwnd, "无法读取图片："+e.Error())
		return
	}
	if e = pasteDIB(im); e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	status("图片已插入光标处。Ctrl+V 也可以粘贴剪贴板图片。")
}
func exportZIP() {
	if app.rec != nil {
		msg(app.hwnd, "请先停止录音，再导出课堂完整资料。")
		return
	}
	if e := saveSession(); e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	dir := filepath.Join(app.base, "exports")
	if e := os.MkdirAll(dir, 0700); e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	name := time.Now().Format("20060102_150405") + "_class.zip"
	dest := filepath.Join(dir, name)
	if e := packageSession(app.folder, dest); e != nil {
		msg(app.hwnd, e.Error())
		return
	}
	status("已导出：" + dest)
	msg(app.hwnd, "完整课堂资料已导出至：\n"+dest+"\n\n包含录音 WAV、富文本笔记 RTF、转写 TXT、AI 总结 Markdown。")
}
func openFolder() {
	if app.folder == "" {
		return
	}
	call(shell.NewProc("ShellExecuteW"), app.hwnd, uintptr(unsafe.Pointer(u("open"))), uintptr(unsafe.Pointer(u(app.folder))), 0, 0, swShow)
}

func uiCreate() {
	// Transparent security boundary: the API key is not saved in session data.
	app.course = create("EDIT", "未命名课程", wsChild|wsVisible|0x00800000, 18, 18, 240, 26, app.hwnd, 0)
	create("STATIC", "DeepSeek Key（不保存）", wsChild|wsVisible, 278, 22, 156, 22, app.hwnd, 0)
	app.key = create("EDIT", "", wsChild|wsVisible|0x00800000|esPassword, 434, 18, 208, 26, app.hwnd, 0)
	ids := []struct {
		id    int
		label string
	}{
		{idNew, "新建课堂"}, {idOpen, "打开课堂"}, {idStart, "开始录音"}, {idPause, "暂停/继续"}, {idStop, "停止录音"},
		{idImage, "插入 PPT 图片"}, {idPaste, "粘贴"}, {idRetry, "补转写"}, {idAI, "AI 总结"}, {idExport, "导出 ZIP"}, {idFolder, "打开文件夹"},
	}
	for _, v := range ids {
		app.controls[v.id] = create("BUTTON", v.label, wsChild|wsVisible|bsPushbutton, 0, 0, 94, 30, app.hwnd, v.id)
	}
	app.notesLabel = create("STATIC", "课堂笔记（支持文字与图片）", wsChild|wsVisible, 0, 0, 300, 24, app.hwnd, 0)
	app.transcriptLabel = create("STATIC", "实时转写（可直接编辑历史识别内容）", wsChild|wsVisible, 0, 0, 300, 24, app.hwnd, 0)
	app.summaryLabel = create("STATIC", "AI 课堂总结（可编辑，点击 AI 总结生成）", wsChild|wsVisible, 0, 0, 600, 24, app.hwnd, 0)
	app.notes = create("RICHEDIT50W", "", wsChild|wsVisible|wsVScroll|esMultiline|esAutoVScroll|esWantReturn|0x00800000, 0, 0, 500, 500, app.hwnd, 0)
	app.transcript = create("RICHEDIT50W", "", wsChild|wsVisible|wsVScroll|esMultiline|esAutoVScroll|esWantReturn|0x00800000, 0, 0, 500, 500, app.hwnd, 0)
	app.summary = create("RICHEDIT50W", "", wsChild|wsVisible|wsVScroll|esMultiline|esAutoVScroll|esWantReturn|0x00800000, 0, 0, 500, 200, app.hwnd, 0)
	app.status = create("STATIC", "就绪。笔记支持 Ctrl+V 粘贴截图，插入本地 PPT 图片。", wsChild|wsVisible, 14, 0, 800, 24, app.hwnd, 0)
	fontHeight := int32(-20)
	app.font = call(gdi.NewProc("CreateFontW"), uintptr(uint32(fontHeight)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 0, 0, uintptr(unsafe.Pointer(u("Microsoft YaHei UI"))))
	for _, h := range []uintptr{app.notes, app.transcript, app.summary, app.course, app.key} {
		send(h, wmSetFont, app.font, 1)
	}
	base := filepath.Join(os.Getenv("USERPROFILE"), "Documents", "ClassHelper")
	if os.Getenv("USERPROFILE") == "" {
		base = filepath.Join(os.TempDir(), "ClassHelper")
	}
	app.base = base
	_ = os.MkdirAll(base, 0700)
	go worker()
	newClass()
	call(user.NewProc("SetTimer"), app.hwnd, 1, 150, 0)
}
func uiLayout(w, h int) {
	if w < 760 {
		w = 760
	}
	if h < 560 {
		h = 560
	}
	x := 16
	y := 58
	order := []int{idNew, idOpen, idStart, idPause, idStop, idImage, idPaste, idRetry, idAI, idExport, idFolder}
	widths := []int{84, 84, 84, 87, 84, 112, 64, 74, 76, 85, 100}
	for i, id := range order {
		bw := widths[i]
		if x+bw > w-10 {
			x = 16
			y += 36
		}
		move(app.controls[id], x, y, bw, 30)
		x += bw + 7
	}
	top := y + 60
	bottom := h - 33
	summaryHeight := 145
	mainEnd := bottom - summaryHeight - 16
	leftWidth := (w - 48) / 2
	move(app.notesLabel, 16, top-26, leftWidth, 24)
	move(app.transcriptLabel, 32+leftWidth, top-26, w-leftWidth-48, 24)
	move(app.summaryLabel, 16, mainEnd+2, w-32, 24)
	move(app.notes, 16, top, leftWidth, mainEnd-top)
	move(app.transcript, 32+leftWidth, top, w-leftWidth-48, mainEnd-top)
	move(app.summary, 16, mainEnd+27, w-32, summaryHeight-19)
	move(app.status, 16, h-29, w-32, 22)
}
func wndProc(hwnd, method, w, l uintptr) uintptr {
	switch method {
	case wmCreate:
		app.hwnd = hwnd
		uiCreate()
		return 0
	case wmSize:
		uiLayout(int(uint16(l)), int(uint16(l>>16)))
		return 0
	case wmCommand:
		id := int(w & 0xffff)
		switch id {
		case idNew:
			newClass()
		case idOpen:
			loadClass()
		case idStart:
			startRecord()
		case idPause:
			pauseRecord()
		case idStop:
			stopRecord()
		case idImage:
			addImage()
		case idPaste:
			send(app.notes, wmPaste, 0, 0)
		case idAI:
			doSummary()
		case idExport:
			exportZIP()
		case idRetry:
			retryChunks()
		case idFolder:
			openFolder()
		}
		return 0
	case wmTimer:
		processWaves()
		pollResults()
		if time.Since(app.lastSave) >= 5*time.Second {
			if app.rec != nil {
				_ = wavPatch(app.rec.file, app.rec.total)
			}
			if e := saveSession(); e != nil {
				status("自动保存出错：" + e.Error())
			}
			app.lastSave = time.Now()
			refreshTitle()
		}
		return 0
	case wmClose:
		if app.rec != nil {
			stopRecord()
		}
		if e := saveSession(); e != nil {
			msg(hwnd, "保存失败："+e.Error())
		}
		call(user.NewProc("DestroyWindow"), hwnd)
		return 0
	case wmDestroy:
		call(pPostQuit, 0)
		return 0
	}
	return call(pDefWindow, hwnd, method, w, l)
}
func main() {
	// Native Win32 UI. No browser or external server is launched.
	ole.NewProc("OleInitialize").Call(0)
	defer ole.NewProc("OleUninitialize").Call()
	kernel.NewProc("LoadLibraryW").Call(uintptr(unsafe.Pointer(u("Msftedit.dll"))))
	class := u("ClassHelperNativeWindow")
	wc := winClass{style: 3, wndProc: syscall.NewCallback(wndProc), background: 6, className: uintptr(unsafe.Pointer(class))}
	wc.size = uint32(unsafe.Sizeof(wc))
	wc.cursor = call(user.NewProc("LoadCursorW"), 0, 32512)
	if call(pRegisterClass, uintptr(unsafe.Pointer(&wc))) == 0 {
		panic("RegisterClassExW failed")
	}
	hwnd := create("ClassHelperNativeWindow", "课堂助手 Class Helper", wsOverlappedWindow|wsVisible|wsClipChildren, 70, 45, 1250, 850, 0, 0)
	if hwnd == 0 {
		panic("CreateWindowExW failed")
	}
	call(pShowWindow, hwnd, swShow)
	call(user.NewProc("UpdateWindow"), hwnd)
	var m winMsg
	for {
		r, _, _ := user.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if r == 0 || r == ^uintptr(0) {
			break
		}
		call(user.NewProc("TranslateMessage"), uintptr(unsafe.Pointer(&m)))
		call(user.NewProc("DispatchMessageW"), uintptr(unsafe.Pointer(&m)))
	}
}