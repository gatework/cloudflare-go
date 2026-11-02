package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestLocalErrorsNeverConnect(t *testing.T) {
	for _, args := range [][]string{
		{"zone", "get", ""}, {"profile", "show", ""}, {"zone", "list", "--format", "bind"},
		{"dns", "get", "example.com", "--id", ""}, {"dns", "list", "example.com", "--timeout", "0"},
		{"dns", "create", "example.com", "--file", "record.json", "--content", "bad"},
	} {
		var out, diagnostic bytes.Buffer
		a := &app{in: strings.NewReader(""), out: &out, err: &diagnostic, path: filepath.Join(t.TempDir(), "config.json"), factory: func(string) (dns.API, func()) { t.Fatal("connected before validation"); return nil, nil }}
		if code := a.execute(t.Context(), args); code != 2 {
			t.Fatal(args, code, diagnostic.String())
		}
	}
}

func TestRecordInputIsValidatedBeforeCredentialsAndAPIs(t *testing.T) {
	dir := t.TempDir()
	invalidPatch := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalidPatch, []byte(`{"id":"read-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		flags []string
		want  string
	}{
		{[]string{"--type", "A", "--content", "::1"}, "IP"},
		{[]string{"--type", "TXT", "--content", "text", "--ttl", "0"}, "ttl"},
		{[]string{"--file", invalidPatch}, "字段"},
		{[]string{"--type", "TXT", "--content-file", filepath.Join(dir, "missing")}, "missing"},
		{[]string{"--type", "A", "--content", "192.0.2.1", "--caa-value", "wrong-type"}, "data"},
	} {
		var diagnostic bytes.Buffer
		a := &app{in: strings.NewReader(""), out: &diagnostic, err: &diagnostic, path: filepath.Join(dir, "missing-config.json"), factory: func(string) (dns.API, func()) {
			t.Fatal("connected before input validation")
			return nil, nil
		}}
		args := append([]string{"dns", "create", "app.example.com", "--token-file", filepath.Join(dir, "missing.token")}, tc.flags...)
		if code := a.execute(t.Context(), args); code != 2 || !strings.Contains(diagnostic.String(), tc.want) {
			t.Fatal("credentials were read before validating the record", tc.flags, code, diagnostic.String())
		}
	}
}
