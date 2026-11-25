package main

import (
	"archive/zip"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWAV(t *testing.T) {
	b := wavBytes(make([]byte, 32000))
	if len(b) != 32044 {
		t.Fatal(len(b))
	}
	if string(b[:4]) != "RIFF" {
		t.Fatal("header")
	}
	if binary.LittleEndian.Uint32(b[24:28]) != 16000 {
		t.Fatal("rate")
	}
	if binary.LittleEndian.Uint32(b[40:44]) != 32000 {
		t.Fatal("length")
	}
}
func TestSessionAndExport(t *testing.T) {
	base := t.TempDir()
	p, m, e := newSession(base, "经济学")
	if e != nil {
		t.Fatal(e)
	}
	if m.Course != "经济学" {
		t.Fatal(m)
	}
	if e = os.WriteFile(filepath.Join(p, "transcript.txt"), []byte("中文转写"), 0600); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(base, "bundle.zip")
	if e = packageSession(p, dest); e != nil {
		t.Fatal(e)
	}
	f, e := zip.OpenReader(dest)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	names := ""
	for _, v := range f.File {
		names += v.Name + ","
	}
	if !strings.Contains(names, "transcript.txt") || !strings.Contains(names, "session.json") {
		t.Fatal(names)
	}
}
func TestChunks(t *testing.T) {
	p, _, _ := newSession(t.TempDir(), "Test")
	os.WriteFile(filepath.Join(p, "chunks", "000001.wav"), []byte{1}, 0600)
	os.WriteFile(filepath.Join(p, "chunks", "000002.wav"), []byte{2}, 0600)
	os.WriteFile(filepath.Join(p, "chunks", "000001.wav.done"), []byte("ok"), 0600)
	fs, e := chunkFileNames(p)
	if e != nil || len(fs) != 1 || !strings.Contains(fs[0], "000002") {
		t.Fatalf("%v %v", fs, e)
	}
}
