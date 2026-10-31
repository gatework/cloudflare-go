package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gatework/cloudflare-go/internal/cloudflare"
	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestExplicitZoneIDOverridesProfileDefault(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Update(path, func(c *config.Config) error {
		c.DefaultProfile = "test"
		c.Profiles["test"] = config.Profile{DefaultZone: "different.example.org"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := &apiFixture{}
	for _, args := range [][]string{{"zone", "get", "--zone-id", zone}, {"dns", "list", "--zone-id", zone}} {
		code, out, errOut := command(t, f, path, args...)
		if code != 0 || !json.Valid([]byte(out)) {
			t.Fatal(args, code, out, errOut)
		}
	}
	code, _, _ := command(t, f, path, "dns", "list", "--zone-id", zone, "--zone", "different.example.org")
	if code != 4 {
		t.Fatal("explicit name/ID conflict ignored", code)
	}
}

func TestUnusableOutputPreventsMutation(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{}
	dir := t.TempDir()
	code, _, errOut := command(t, f, filepath.Join(dir, "config.json"), "dns", "create", "app.example.com", "--type", "A", "--content", "192.0.2.1", "--output", filepath.Join(dir, "missing-parent", "result.json"))
	if code == 0 || f.writes != 0 {
		t.Fatal("mutation preceded output validation", code, f.writes, errOut)
	}
}

type lostCreateResponse struct {
	dns.API
	cancel context.CancelFunc
}

func (a lostCreateResponse) CreateRecord(ctx context.Context, zone string, patch dns.RecordPatch) (dns.Record, error) {
	if _, err := a.API.CreateRecord(ctx, zone, patch); err != nil {
		return dns.Record{}, err
	}
	a.cancel()
	return dns.Record{}, context.Canceled
}

func TestCanceledMutationRetainsUncertainExitCode(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out, diagnostic bytes.Buffer
	a := &app{in: strings.NewReader(""), out: &out, err: &diagnostic, path: filepath.Join(t.TempDir(), "config.json"), factory: func(token string) (dns.API, func()) {
		client := cloudflare.New(token, server.URL+"/", server.Client().Transport)
		return lostCreateResponse{client, cancel}, client.Close
	}}
	code := a.execute(ctx, []string{"dns", "create", "app.example.com", "--type", "A", "--content", "192.0.2.1"})
	if code != 5 || f.writes != 1 || out.Len() != 0 {
		t.Fatal(code, f.writes, out.String(), diagnostic.String())
	}
}

func TestInitTokenPathUsesWorkingDirectory(t *testing.T) {
	t.Setenv("CLOUDFLARE_PROFILE", "")
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile("token", []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings", "config.json")
	code, _, errOut := command(t, &apiFixture{}, path, "init", "--token-file", "token")
	if code != 0 {
		t.Fatal(code, errOut)
	}
	c, err := config.Load(path)
	if err != nil || c.Profiles["default"].TokenFile != filepath.Join(dir, "token") {
		t.Fatal(c, err)
	}
}

type readHook struct {
	io.Reader
	once sync.Once
	hook func()
}

func (r *readHook) Read(p []byte) (int, error) { r.once.Do(r.hook); return r.Reader.Read(p) }

func TestInitDetectsConcurrentProfileChange(t *testing.T) {
	t.Setenv("CLOUDFLARE_PROFILE", "")
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Update(path, func(c *config.Config) error {
		c.DefaultProfile = "work"
		c.Profiles["work"] = config.Profile{DefaultZone: "example.com"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	input := &readHook{Reader: strings.NewReader("fixture-token\n"), hook: func() {
		if err := config.Update(path, func(c *config.Config) error {
			c.Profiles["work"] = config.Profile{DefaultZone: "other.example.org", Format: "csv"}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}}
	var diagnostic bytes.Buffer
	a := &app{in: input, out: io.Discard, err: &diagnostic, interactive: true, path: path}
	if code := a.execute(t.Context(), []string{"init"}); code != 4 {
		t.Fatal(code, diagnostic.String())
	}
	c, err := config.Load(path)
	if err != nil || c.Profiles["work"].Format != "csv" {
		t.Fatal("concurrent config overwritten", c, err)
	}
	files, err := os.ReadDir(filepath.Join(filepath.Dir(path), "credentials"))
	if err != nil || len(files) != 0 {
		t.Fatal("unused credential not cleaned up", files, err)
	}
}

func TestProfileShowHonorsEnvironmentSelection(t *testing.T) {
	t.Setenv("CLOUDFLARE_PROFILE", "work")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Update(path, func(c *config.Config) error {
		c.DefaultProfile = "personal"
		c.Profiles["personal"] = config.Profile{}
		c.Profiles["work"] = config.Profile{TokenEnv: "WORK_TOKEN"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := command(t, &apiFixture{}, path, "profile", "show")
	if code != 0 || !strings.Contains(out, `"name": "work"`) {
		t.Fatal(code, out, errOut)
	}
}
