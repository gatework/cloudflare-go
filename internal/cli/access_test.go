package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/dns"
)

type accessFixture struct {
	dns.API
	zones           []dns.Zone
	listErr, dnsErr error
	lists, reads    int
	onRead          func()
}

func (f *accessFixture) ListZones(_ context.Context, name, account string) ([]dns.Zone, error) {
	f.lists++
	zones := []dns.Zone{}
	for _, z := range f.zones {
		if (name == "" || z.Name == name) && (account == "" || z.AccountID == account) {
			zones = append(zones, z)
		}
	}
	return zones, f.listErr
}
func (f *accessFixture) GetZone(_ context.Context, id string) (dns.Zone, error) {
	for _, z := range f.zones {
		if z.ID == id {
			return z, nil
		}
	}
	return dns.Zone{}, dns.ErrNotFound
}
func (f *accessFixture) CheckDNSRead(context.Context, string) error {
	f.reads++
	if f.onRead != nil {
		f.onRead()
	}
	return f.dnsErr
}
func runAccess(t *testing.T, f *accessFixture, path, input string, interactive bool, args ...string) (int, string, string) {
	t.Helper()
	var out, diagnostic bytes.Buffer
	a := &app{in: strings.NewReader(input), out: &out, err: &diagnostic, path: path, interactive: interactive, factory: func(token string) (dns.API, func()) {
		if token != "fixture-token" {
			t.Fatal("unexpected credential source")
		}
		return f, func() {}
	}}
	code := a.execute(t.Context(), args)
	if strings.Contains(out.String()+diagnostic.String(), "fixture-token") {
		t.Fatal("token leaked")
	}
	return code, out.String(), diagnostic.String()
}

func TestInteractiveInitSelectsZoneAndDisambiguatesAccount(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	account1, account2 := strings.Repeat("3", 32), strings.Repeat("4", 32)
	for _, tc := range []struct {
		name, input, wantZone, wantAccount string
		zones                              []dns.Zone
	}{
		{"none", "", "", "", nil},
		{"one", "1\n", "example.com", "", []dns.Zone{{ID: zone, Name: "example.com", AccountID: account1}}},
		{"skip", "0\n", "", "", []dns.Zone{{ID: zone, Name: "example.com", AccountID: account1}}},
		{"multiple", "bad\n2\n", "example.org", "", []dns.Zone{{ID: zone, Name: "example.com", AccountID: account1}, {ID: id, Name: "example.org", AccountID: account2}}},
		{"duplicate", "2\n", "example.com", account2, []dns.Zone{{ID: zone, Name: "example.com", AccountID: account1}, {ID: id, Name: "example.com", AccountID: account2}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			f := &accessFixture{zones: tc.zones}
			code, _, diagnostic := runAccess(t, f, path, tc.input, true, "init")
			if code != 0 {
				t.Fatal(code, diagnostic)
			}
			c, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			p := c.Profiles["default"]
			if p.DefaultZone != tc.wantZone || p.AccountID != tc.wantAccount || f.lists != 1 || (f.reads == 1) != (tc.wantZone != "") {
				t.Fatal(p, f.lists, f.reads)
			}
		})
	}
}

func TestInitOfflineAndRequiredCheck(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, tc := range []struct {
		name, input string
		args        []string
		interactive bool
		fail        error
		code        int
		saved       bool
		lists       int
	}{
		{"script", "", []string{"init"}, false, nil, 0, true, 0},
		{"offline", "", []string{"init", "--offline"}, true, nil, 0, true, 0},
		{"required success", "", []string{"init", "--check", "--default-zone", "example.com"}, false, nil, 0, true, 1},
		{"required failure", "", []string{"init", "--check"}, false, errors.New("denied"), 1, false, 1},
		{"decline fallback", "n\n", []string{"init"}, true, errors.New("denied"), 1, false, 1},
		{"accept fallback", "y\n", []string{"init"}, true, errors.New("denied"), 0, true, 1},
		{"incompatible", "", []string{"init", "--check", "--offline"}, false, nil, 2, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			f := &accessFixture{zones: []dns.Zone{{ID: zone, Name: "example.com"}}, listErr: tc.fail}
			code, _, diagnostic := runAccess(t, f, path, tc.input, tc.interactive, tc.args...)
			if code != tc.code || f.lists != tc.lists {
				t.Fatal(code, f.lists, diagnostic)
			}
			_, err := os.Stat(path)
			if (err == nil) != tc.saved {
				t.Fatal("unexpected publication", err)
			}
		})
	}
}

func TestProfileCheckReportsReadAccessWithoutWriting(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, fail := range []bool{false, true} {
		f := &accessFixture{zones: []dns.Zone{{ID: zone, Name: "example.com"}}}
		if fail {
			f.dnsErr = errors.New("DNS read denied")
		}
		code, out, diagnostic := runAccess(t, f, filepath.Join(t.TempDir(), "config.json"), "", false, "profile", "check", "--zone", "example.com")
		if (code == 0) == fail || f.reads != 1 {
			t.Fatal(code, f.reads, diagnostic)
		}
		var steps []accessCheck
		if json.Unmarshal([]byte(out), &steps) != nil || len(steps) != 3 || (steps[2].Status == "ok") == fail {
			t.Fatal(out)
		}
	}
}

func TestInitCanceledDuringAccessDoesNotPublish(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	path := filepath.Join(t.TempDir(), "config.json")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f := &accessFixture{zones: []dns.Zone{{ID: zone, Name: "example.com"}}, onRead: cancel}
	var diagnostic bytes.Buffer
	a := &app{in: strings.NewReader(""), out: &bytes.Buffer{}, err: &diagnostic, path: path, factory: func(string) (dns.API, func()) { return f, func() {} }}
	if code := a.execute(ctx, []string{"init", "--check", "--default-zone", "example.com"}); code != 130 {
		t.Fatal(code, diagnostic.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestInitPreservesCurrentZoneAndRejectsDNSReadFailure(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Update(path, func(c *config.Config) error {
		c.DefaultProfile = "work"
		c.Profiles["work"] = config.Profile{TokenEnv: "CLOUDFLARE_API_TOKEN", DefaultZone: "example.com"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f := &accessFixture{zones: []dns.Zone{{ID: zone, Name: "example.com"}}}
	if code, _, diagnostic := runAccess(t, f, path, "\n", true, "init"); code != 0 || f.reads != 1 {
		t.Fatal(code, diagnostic)
	}
	before, _ := os.ReadFile(path)
	f.dnsErr = errors.New("DNS read denied")
	if code, _, diagnostic := runAccess(t, f, path, "", false, "init", "--check"); code != 1 {
		t.Fatal(code, diagnostic)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed required check changed config")
	}
}

func TestProfileCheckFormatsAndSkippedDNS(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, format := range []string{"json", "csv", "txt", "table"} {
		f := &accessFixture{}
		code, out, diagnostic := runAccess(t, f, filepath.Join(t.TempDir(), "config.json"), "", false, "profile", "check", "--format", format)
		if code != 0 || f.reads != 0 || !strings.Contains(out, "skipped") {
			t.Fatal(format, code, out, diagnostic)
		}
	}
}
