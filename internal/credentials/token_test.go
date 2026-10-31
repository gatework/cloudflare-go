package credentials

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialSources(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "token")
	if err := os.WriteFile(file, []byte("first\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLOUDFLARE_API_TOKEN", "other")
	token, err := Read(file, "")
	if err != nil || token != "first" {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("rotated"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err = Read(file, "")
	if err != nil || token != "rotated" {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "space inside", "newline\ninside", "invalid\xff"} {
		if _, err = Validate(bad); err == nil {
			t.Fatal("invalid token accepted")
		}
	}
}
