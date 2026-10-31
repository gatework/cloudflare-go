package fileio

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
	for _, input := range []string{`{"x":1,"x":2}`, `{"data":{"x":1,"x":2}}`, `{} {}`, `null`, `[]`, `{"data":` + strings.Repeat("[", 129) + "0" + strings.Repeat("]", 129) + "}"} {
		if _, err := JSONObject([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
	if _, err := JSONObject([]byte(`{"Work":{},"work":{}}`)); err != nil {
		t.Fatal("case-sensitive map keys rejected", err)
	}
}
func FuzzJSONObject(f *testing.F) {
	f.Add(`{"name":"example.com"}`)
	f.Fuzz(func(t *testing.T, s string) { _, _ = JSONObject([]byte(s)) })
}

func TestStagedOutputCannotClobberConcurrentCreator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "records.json")
	output, err := NewOutput(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if _, err = io.WriteString(output, "staged"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("concurrent"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = output.Commit(); !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "concurrent" {
		t.Fatal(string(data), err)
	}
	if err = output.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatal("staged file leaked", files, err)
	}
}

func TestStagedOverwriteRejectsSymlinkReplacement(t *testing.T) {
	dir := t.TempDir()
	path, target := filepath.Join(dir, "out"), filepath.Join(dir, "target")
	output, err := NewOutput(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err = os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err = output.Commit(); err == nil {
		t.Fatal("concurrent symlink replaced")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "preserve" {
		t.Fatal(string(data), err)
	}
}
