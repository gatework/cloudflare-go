package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/config"
)

func TestCompletionNeedsNeitherHomeNorCredentials(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var out, diagnostic bytes.Buffer
		if code := Run(t.Context(), []string{"completion", shell}, strings.NewReader(""), &out, &diagnostic); code != 0 || !strings.Contains(out.String(), "cloudflare-go") {
			t.Fatal(shell, code, diagnostic.String())
		}
	}
	for _, args := range [][]string{{"__complete", "dns", "dump", "--format", "j"}, {"__complete", "dns", "create", "--type", "AA"}} {
		var out, diagnostic bytes.Buffer
		if code := Run(t.Context(), args, strings.NewReader(""), &out, &diagnostic); code != 0 {
			t.Fatal(args, code, diagnostic.String())
		}
		if len(out.String()) < 4 {
			t.Fatal("no completions", args, out.String())
		}
	}
}

func TestProfileCompletionDoesNotReadCredentialFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Update(path, func(c *config.Config) error {
		c.Profiles["work"] = config.Profile{TokenFile: "does-not-exist.token"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if code := Run(t.Context(), []string{"--config", path, "__complete", "--profile", "w"}, strings.NewReader(""), &out, &diagnostic); code != 0 || !strings.Contains(out.String(), "work") {
		t.Fatal(code, out.String(), diagnostic.String())
	}
}
