// Package config stores operator intent; it performs no API calls.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"github.com/gatework/cloudflare-go/internal/fileio"
)

type Profile struct {
	TokenFile   string `json:"token_file,omitempty"`
	TokenEnv    string `json:"token_env,omitempty"`
	AccountID   string `json:"account_id,omitempty"`
	DefaultZone string `json:"default_zone,omitempty"`
	Format      string `json:"format,omitempty"`
}
type Config struct {
	DefaultProfile string             `json:"default_profile,omitempty"`
	Profiles       map[string]Profile `json:"profiles"`
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
var envPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ValidProfile(name string) bool { return namePattern.MatchString(name) }
func ValidID(id string) bool        { return idPattern.MatchString(id) }
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cloudflare-go", "config.json"), nil
}
func Decode(data []byte) (Config, error) {
	var c Config
	object, err := fileio.JSONObject(data)
	if err != nil {
		return c, err
	}
	for key, value := range object {
		if key != "default_profile" && key != "profiles" {
			return c, errors.New("配置包含未知字段")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return c, errors.New("配置字段不能为 null")
		}
	}
	var profiles map[string]json.RawMessage
	if json.Unmarshal(object["profiles"], &profiles) != nil || profiles == nil {
		return c, errors.New("配置缺少有效 profiles")
	}
	for _, raw := range profiles {
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return c, errors.New("profile 必须是对象")
		}
		for key, value := range fields {
			switch key {
			case "token_file", "token_env", "account_id", "default_zone", "format":
			default:
				return c, errors.New("profile 包含未知字段")
			}
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return c, errors.New("配置字段不能为 null")
			}
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil {
		return c, errors.New("配置字段无效")
	}
	if c.Profiles == nil {
		return c, errors.New("配置缺少 profiles")
	}
	for name, p := range c.Profiles {
		if !ValidProfile(name) || p.TokenFile != "" && p.TokenEnv != "" || p.TokenEnv != "" && !envPattern.MatchString(p.TokenEnv) {
			return c, errors.New("配置名称或凭据来源无效")
		}
		if p.AccountID != "" && !ValidID(p.AccountID) {
			return c, errors.New("account_id 应为 32 位十六进制 ID")
		}
		if p.Format != "" && p.Format != "table" && p.Format != "txt" && p.Format != "csv" && p.Format != "json" {
			return c, errors.New("profile.format 无效")
		}
	}
	if c.DefaultProfile != "" {
		if _, ok := c.Profiles[c.DefaultProfile]; !ok {
			return c, errors.New("默认配置不存在")
		}
	}
	return c, nil
}
func Load(path string) (Config, error) {
	data, err := fileio.ReadRegularFile(path, 1<<20)
	if err != nil {
		return Config{}, err
	}
	return Decode(data)
}

// LoadOptional allows first use without a default config. An existing but
// dangling symlink is a broken configuration, not permission to change identity.
func LoadOptional(path string) (Config, error) {
	c, err := Load(path)
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
			return Config{Profiles: map[string]Profile{}}, nil
		}
	}
	return c, err
}
func (c Config) Select(name, base string) (Profile, error) {
	if name == "" {
		name = c.DefaultProfile
	}
	if name == "" {
		return Profile{}, nil
	}
	p, ok := c.Profiles[name]
	if !ok {
		return p, errors.New("指定 profile 不存在")
	}
	if p.TokenFile != "" && !filepath.IsAbs(p.TokenFile) {
		p.TokenFile = filepath.Join(base, p.TokenFile)
	}
	return p, nil
}
func Update(path string, change func(*Config) error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	unlock, err := fileio.Lock(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	c, err := LoadOptional(path)
	if err != nil {
		return err
	}
	if err = change(&c); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if _, err = Decode(data); err != nil {
		return err
	}
	return fileio.Publish(path, true, func(w io.Writer) error { _, err := w.Write(append(data, '\n')); return err })
}
