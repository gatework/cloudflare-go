package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/cloudflare"
	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestCommandBudgetIncludesZoneAndRecordResponses(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{record: map[string]any{"id": id, "name": "app.example.com", "type": "TXT", "content": strings.Repeat("x", 120)}}
	dir := t.TempDir()
	output := filepath.Join(dir, "records.json")
	if err := os.WriteFile(output, []byte("previous output"), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, diagnostic := command(t, f, filepath.Join(dir, "config.json"), "dns", "dump", "example.com", "--zone-id", zone, "--max-bytes", "350", "--output", output, "--overwrite")
	if code != 1 || !strings.Contains(diagnostic, "max-bytes") {
		t.Fatal("command budget was reset or mistaken for cancellation", code, diagnostic)
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "previous output" || f.writes != 0 {
		t.Fatal("budget failure changed published output or DNS", err)
	}
}

func TestDeleteConfirmationDoesNotResetResponseBudget(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{record: map[string]any{"id": id, "name": "app.example.com", "type": "TXT", "content": "before"}}
	responseSize := func(path string) int {
		w := httptest.NewRecorder()
		f.serve(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w.Body.Len()
	}
	limit := responseSize("/zones/"+zone) + 2*responseSize("/zones/"+zone+"/dns_records/"+id) - 1
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	defer server.Close()
	var output, diagnostic bytes.Buffer
	a := &app{in: strings.NewReader("y\n"), out: &output, err: &diagnostic, interactive: true, path: filepath.Join(t.TempDir(), "config.json"), factory: func(token string) (dns.API, func()) {
		c := cloudflare.New(token, server.URL+"/", server.Client().Transport)
		return c, c.Close
	}}
	code := a.execute(t.Context(), []string{"dns", "delete", "--id", id, "--zone-id", zone, "--max-bytes", strconv.Itoa(limit)})
	if code != 1 || f.writes != 0 || output.Len() != 0 || !strings.Contains(diagnostic.String(), "max-bytes") || !strings.Contains(diagnostic.String(), "[y/N]") {
		t.Fatal("confirmation refreshed the budget or changed DNS", code, f.writes, output.String(), diagnostic.String())
	}
}

func TestResponseBudgetAfterMutationRetainsUncertainExit(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{}
	// Each preliminary response fits; the combined create response does not.
	code, output, diagnostic := command(t, f, filepath.Join(t.TempDir(), "config.json"), "dns", "create", "app.example.com", "--zone-id", zone, "--type", "TXT", "--content", strings.Repeat("x", 1000), "--max-bytes", "700")
	if code != 5 || f.writes != 1 || output != "" {
		t.Fatal("budget cancellation hid an uncertain write", code, f.writes, output, diagnostic)
	}
}

func TestInitZoneSelectionDoesNotResetResponseBudget(t *testing.T) {
	t.Setenv("CLOUDFLARE_PROFILE", "")
	dir := t.TempDir()
	token := filepath.Join(dir, "source.token")
	if err := os.WriteFile(token, []byte("fixture-token"), 0600); err != nil {
		t.Fatal(err)
	}
	f := &apiFixture{}
	recorder := httptest.NewRecorder()
	f.serve(recorder, httptest.NewRequest(http.MethodGet, "/zones", nil))
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	defer server.Close()
	var output, diagnostic bytes.Buffer
	path := filepath.Join(dir, "config.json")
	a := &app{in: strings.NewReader("1\n"), out: &output, err: &diagnostic, interactive: true, path: path, factory: func(token string) (dns.API, func()) {
		c := cloudflare.New(token, server.URL+"/", server.Client().Transport)
		return c, c.Close
	}}
	code := a.execute(t.Context(), []string{"init", "--token-file", token, "--max-bytes", strconv.Itoa(recorder.Body.Len() + 1)})
	if code != 1 || !strings.Contains(diagnostic.String(), "max-bytes") {
		t.Fatal("zone selection refreshed the command budget", code, diagnostic.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unverified profile saved after budget failure", err)
	}
}
