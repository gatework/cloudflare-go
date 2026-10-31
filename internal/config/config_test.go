package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOptionalConfigOnlyAllowsAnAbsentFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if _, err := Load(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing explicit config accepted", err)
	}
	if c, err := LoadOptional(path); err != nil || c.Profiles == nil {
		t.Fatal(c, err)
	}
	if err := os.Symlink("missing-target", path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOptional(path); err == nil {
		t.Fatal("broken config symlink silently ignored")
	}
}

func TestProfilesAndAtomicUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := Update(path, func(c *Config) error {
		c.Profiles["one"] = Profile{TokenFile: "secrets/token"}
		c.DefaultProfile = "one"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := Update(path, func(c *Config) error { c.Profiles["two"] = Profile{TokenEnv: "SECOND_TOKEN"}; return nil }); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil || len(c.Profiles) != 2 {
		t.Fatal(c, err)
	}
	p, err := c.Select("", filepath.Dir(path))
	if err != nil || p.TokenFile != filepath.Join(filepath.Dir(path), "secrets/token") {
		t.Fatal(p, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	before, _ := os.ReadFile(path)
	if err := Update(path, func(c *Config) error { c.DefaultProfile = "missing"; return nil }); err == nil {
		t.Fatal("invalid update accepted")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed update replaced config")
	}
}
func TestStrictConfiguration(t *testing.T) {
	for _, input := range []string{`{"profiles":{"a":null}}`, `{"Profiles":{}}`, `{"profiles":{"a":{"Token_Env":"TOKEN"}}}`, `{"profiles":{"a":{"format":null}}}`, `{"profiles":{},"unexpected":true}`, `{"profiles":{"a":{"token_file":"file","token_env":"TOKEN"}}}`, `{"profiles":{},"default_profile":"missing"}`, `{"profiles":{"../bad":{}}}`, `{"profiles":{"a":{"api_token":"secret"}}}`} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Fatal(input)
		}
	}
	if c, err := Decode([]byte(`{"profiles":{"Work":{},"work":{}}}`)); err != nil || len(c.Profiles) != 2 {
		t.Fatal(c, err)
	}
}
func FuzzDecode(f *testing.F) {
	f.Add(`{"profiles":{}}`)
	f.Fuzz(func(t *testing.T, s string) {
		c, err := Decode([]byte(s))
		if err == nil {
			b, e := json.Marshal(c)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = Decode(b); e != nil {
				t.Fatal(e)
			}
		}
	})
}
