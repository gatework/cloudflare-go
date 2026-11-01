package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestJSONLExtensionAndReadSelectors(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{record: map[string]any{"id": id, "name": "example.com", "type": "TXT", "content": "line1\nline2", "tags": []string{"team:ops", "env:prod"}, "comment": "Review"}}
	dir := t.TempDir()
	for _, extension := range []string{"jsonl", "ndjson"} {
		path := filepath.Join(dir, "records."+extension)
		code, _, diagnostic := command(t, f, filepath.Join(dir, "config.json"), "dns", "dump", "example.com", "--match-tag", "team:ops", "--match-tag", "env:prod", "--match-comment", "Review", "--output", path)
		if code != 0 {
			t.Fatal(diagnostic)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		r, err := dns.ParseRecord(raw)
		if err != nil || r.ID != id {
			t.Fatal(string(raw), err)
		}
	}
	code, out, diagnostic := command(t, f, filepath.Join(dir, "config.json"), "dns", "list", "example.com", "--format", "jsonl", "--match-comment", "review")
	if code != 0 || out != "" {
		t.Fatal(code, out, diagnostic)
	}
	code, _, _ = command(t, f, filepath.Join(dir, "config.json"), "dns", "update", "example.com", "--content", "new", "--format", "jsonl")
	if code != 2 || f.writes != 0 {
		t.Fatal("unsupported output caused mutation", code)
	}
}
