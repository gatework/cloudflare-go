package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCancelDuringUpdatePreservesConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Update(path, func(c *Config) error { c.Profiles["previous"] = Profile{}; return nil }); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err = UpdateContext(ctx, path, func(c *Config) error { c.Profiles["next"] = Profile{}; cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != string(previous) {
		t.Fatal("canceled configuration published", err)
	}
}
