package fileio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAtomicOutputNeverClobbersOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	write := func(w io.Writer) error { _, _ = io.WriteString(w, "new"); return errors.New("encoder failed") }
	if Publish(path, true, write) == nil {
		t.Fatal("failure ignored")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "old" {
		t.Fatal("partial output published")
	}
	if Publish(path, false, func(w io.Writer) error { _, e := io.WriteString(w, "new"); return e }) == nil {
		t.Fatal("existing file overwritten")
	}
}
func TestRejectFIFOAndDuplicateJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFile(path, 1024); err == nil {
		t.Fatal("FIFO accepted")
	}
	for _, input := range []string{`{"x":1,"X":2}`, `{"data":{"x":1,"x":2}}`, `{} {}`, `null`, `[]`} {
		if _, err := JSONObject([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
}
func FuzzJSONObject(f *testing.F) {
	f.Add(`{"name":"example.com"}`)
	f.Fuzz(func(t *testing.T, s string) { _, _ = JSONObject([]byte(s)) })
}
