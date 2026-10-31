package fileio

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// PublishedError means publication succeeded but its durability is unconfirmed.
type PublishedError struct{ Err error }

func (e *PublishedError) Error() string {
	return fmt.Sprintf("文件已发布，但目录同步失败：%v", e.Err)
}
func (e *PublishedError) Unwrap() error { return e.Err }

// Output stages an atomic file replacement. Close removes only the temporary
// file, even if Commit reports a post-publication durability failure.
type Output struct {
	file      *os.File
	path      string
	temporary string
	overwrite bool
}

func NewOutput(path string, overwrite bool) (*Output, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, errors.New("输出目标必须是普通文件")
		}
		if !overwrite {
			return nil, fmt.Errorf("%s: %w", path, os.ErrExist)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cloudflare-go-*")
	if err != nil {
		return nil, err
	}
	output := &Output{file: f, path: path, temporary: f.Name(), overwrite: overwrite}
	if err = f.Chmod(0600); err != nil {
		_ = output.Close()
		return nil, err
	}
	return output, nil
}

func (o *Output) Write(data []byte) (int, error) { return o.file.Write(data) }

func (o *Output) Close() error {
	_ = o.file.Close()
	err := os.Remove(o.temporary)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (o *Output) Commit() error {
	if err := o.file.Sync(); err != nil {
		return err
	}
	if err := o.file.Close(); err != nil {
		return err
	}
	var err error
	if o.overwrite {
		// Recheck after encoding: a concurrently replaced special file must not
		// be silently removed. Rename never follows a destination symlink.
		if info, statErr := os.Lstat(o.path); statErr == nil && !info.Mode().IsRegular() {
			return errors.New("输出目标必须是普通文件")
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		err = os.Rename(o.temporary, o.path)
	} else {
		err = os.Link(o.temporary, o.path)
	}
	if err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(o.path))
	if err != nil {
		return &PublishedError{Err: err}
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return &PublishedError{Err: err}
	}
	return nil
}

// Publish writes beside the destination and publishes only complete output.
// No-clobber publication uses a hard link, so concurrent creators stay safe.
func Publish(path string, overwrite bool, write func(io.Writer) error) error {
	output, err := NewOutput(path, overwrite)
	if err != nil {
		return err
	}
	defer output.Close()
	if err = write(output); err != nil {
		return err
	}
	return output.Commit()
}
func Lock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("配置锁不是普通文件")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("另一进程正在修改配置")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}
