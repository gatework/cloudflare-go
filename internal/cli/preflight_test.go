package cli

import (
	"bytes"
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
