package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecializedFlagsCannotChangeOtherRecordTypes(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, tc := range []struct{ typ, flag string }{
		{"HTTPS", "--caa-value"}, {"SRV", "--params"}, {"CAA", "--params"}, {"DNSKEY", "--caa-flags"},
	} {
		f := &apiFixture{record: map[string]any{"id": id, "name": "example.com", "type": tc.typ, "ttl": 300, "data": map[string]any{"value": "existing", "flags": 0, "port": 443, "weight": 1, "priority": 1, "target": "example.com"}}}
		value := "changed"
		if tc.flag == "--caa-flags" {
			value = "1"
		}
		code, _, errOut := command(t, f, filepath.Join(t.TempDir(), "config.json"), "dns", "update", "example.com", "--id", id, tc.flag, value)
		if code != 2 || f.writes != 0 {
			t.Fatal(tc, code, f.writes, errOut)
		}
	}
}

func TestContentFileRejectsInvalidUTF8WithoutWriting(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	dir := t.TempDir()
	path := filepath.Join(dir, "content")
	if err := os.WriteFile(path, []byte{'a', 0xff, 'b'}, 0600); err != nil {
		t.Fatal(err)
	}
	f := &apiFixture{}
	code, _, errOut := command(t, f, filepath.Join(dir, "config.json"), "dns", "create", "example.com", "--type", "TXT", "--content-file", path)
	if code != 2 || f.writes != 0 || !strings.Contains(errOut, "UTF-8") {
		t.Fatal(code, f.writes, errOut)
	}
}

func TestEmptySelectorsAndZeroBudgetDoNotFallBack(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, extra := range [][]string{{"--id", ""}, {"--profile", ""}, {"--token-file", ""}, {"--type", ""}, {"--timeout", "0"}, {"--format", ""}, {"--file", ""}, {"--overwrite"}} {
		f := &apiFixture{record: map[string]any{"id": id, "name": "example.com", "type": "A", "content": "192.0.2.1"}}
		args := append([]string{"dns", "update", "example.com", "--content", "192.0.2.2"}, extra...)
		code, _, errOut := command(t, f, filepath.Join(t.TempDir(), "config.json"), args...)
		if code != 2 || f.writes != 0 {
			t.Fatal(extra, code, f.writes, errOut)
		}
	}
}

func TestRunWithoutDefaultConfigDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"profiles":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"version"}, {"--help"}, {"dns"}, {"config", "check", "--config", path}} {
		var out, diagnostics bytes.Buffer
		code := Run(context.Background(), args, strings.NewReader(""), &out, &diagnostics)
		if code != 0 || out.Len() == 0 {
			t.Fatal(args, code, out.String(), diagnostics.String())
		}
	}
}

func TestUnknownSubcommandIsAnError(t *testing.T) {
	for _, group := range []string{"dns", "zone", "profile", "config"} {
		var out, diagnostic bytes.Buffer
		a := &app{in: strings.NewReader(""), out: &out, err: &diagnostic, path: filepath.Join(t.TempDir(), "config.json")}
		if code := a.execute(t.Context(), []string{group, "not-a-command"}); code != 2 {
			t.Fatal(group, code, out.String(), diagnostic.String())
		}
	}
}

func TestAdvancedFileFlowsThroughCLI(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, input := range []string{
		`{"type":"DNSKEY","data":{"flags":257,"protocol":3,"algorithm":13,"public_key":"AQID"}}`,
		`{"type":"NAPTR","data":{"flags":"S","order":1,"preference":1,"regex":"","replacement":"_sip._tcp.example.com","service":"SIP+D2T"}}`,
		`{"type":"URI","priority":10,"data":{"weight":20,"target":"https://example.com/Case?query=Value"}}`,
	} {
		dir := t.TempDir()
		file := filepath.Join(dir, "record.json")
		if err := os.WriteFile(file, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		f := &apiFixture{}
		code, out, errOut := command(t, f, filepath.Join(dir, "config.json"), "dns", "create", "example.com", "--file", file)
		if code != 0 || f.writes != 1 || !json.Valid([]byte(out)) {
			t.Fatal(input, code, out, errOut)
		}
	}
}

func TestMissingExplicitConfigurationNeverUsesAmbientToken(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	path := filepath.Join(t.TempDir(), "missing.json")
	for _, args := range [][]string{
		{"dns", "create", "example.com", "--type", "A", "--content", "192.0.2.1", "--config", path},
		{"config", "check"}, {"config", "show"}, {"profile", "list", "--config", path},
	} {
		f := &apiFixture{}
		code, out, diagnostics := command(t, f, path, args...)
		if code != 2 || f.writes != 0 || out != "" {
			t.Fatal(args, code, out, diagnostics)
		}
	}
	code, _, diagnostics := command(t, &apiFixture{}, path, "init", "--config", path)
	if code != 0 {
		t.Fatal("explicit initialization cannot create config", code, diagnostics)
	}
}
