package fileio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCanceledCommitPreservesFileAndCleansStage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(path, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := NewOutput(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := out.Write([]byte("next")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := out.CommitContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "previous" {
		t.Fatal(string(data), err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 1 {
		t.Fatal("temporary file leaked", files, err)
	}
}
