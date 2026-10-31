package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

const zone = "11111111111111111111111111111111"
const id = "22222222222222222222222222222222"

type apiFixture struct {
	mu        sync.Mutex
	record    map[string]any
	writes    int
	duplicate bool
}

func (f *apiFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var result any
	switch {
	case r.URL.Path == "/zones":
		result = []any{map[string]any{"id": zone, "name": "example.com", "account": map[string]string{"id": "33333333333333333333333333333333"}}}
	case r.URL.Path == "/zones/"+zone:
		result = map[string]any{"id": zone, "name": "example.com", "account": map[string]string{"id": "33333333333333333333333333333333"}}
	case strings.HasSuffix(r.URL.Path, "/dns_records") && r.Method == "GET":
		result = []any{}
		if f.record != nil {
			result = []any{f.record}
			if f.duplicate {
				copy := map[string]any{}
				for k, v := range f.record {
					copy[k] = v
				}
				copy["id"] = "44444444444444444444444444444444"
				result = []any{f.record, copy}
			}
		}
	case r.Method == "POST" || r.Method == "PATCH":
		var patch map[string]any
		if json.NewDecoder(r.Body).Decode(&patch) != nil {
			w.WriteHeader(400)
			return
		}
		f.writes++
		if r.Method == "POST" {
			f.record = map[string]any{"id": id, "ttl": 1, "proxied": false}
		}
		for k, v := range patch {
			f.record[k] = v
		}
		result = f.record
	case r.Method == "DELETE":
		f.writes++
		f.record = nil
		result = map[string]string{"id": id}
	default:
		result = f.record
	}
	if result == nil {
		w.WriteHeader(404)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":81044}]}`)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result, "result_info": map[string]int{"page": 1, "total_pages": 1}})
}
func command(t *testing.T, fixture *apiFixture, path string, args ...string) (int, string, string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	defer server.Close()
	var out, errOut bytes.Buffer
	a := &app{in: strings.NewReader(""), out: &out, err: &errOut, path: path, factory: func(token string) (dns.API, func()) {
		if token != "fixture-token" {
			t.Error("wrong credentials")
		}
		client := cloudflare.New(token, server.URL+"/", server.Client().Transport)
		return client, client.Close
	}}
	code := a.execute(context.Background(), args)
	return code, out.String(), errOut.String()
}
func TestCLIEndToEndCRUDAndDump(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{}
	path := filepath.Join(t.TempDir(), "config.json")
	for _, args := range [][]string{{"dns", "create", "app.example.com", "--type", "A", "--content", "192.0.2.1", "--dry-run"}, {"dns", "create", "app.example.com", "--type", "A", "--content", "192.0.2.1"}, {"dns", "get", "app.example.com"}, {"dns", "update", "app.example.com", "--content", "192.0.2.2", "--proxied=false"}} {
		code, out, errOut := command(t, f, path, args...)
		if code != 0 || !json.Valid([]byte(out)) {
			t.Fatal(args, code, out, errOut)
		}
	}
	if f.writes != 2 {
		t.Fatal("dry run wrote", f.writes)
	}
	target := filepath.Join(t.TempDir(), "records.json")
	code, _, errOut := command(t, f, path, "dns", "dump", "example.com", "--output", target)
	if code != 0 {
		t.Fatal(code, errOut)
	}
	data, _ := os.ReadFile(target)
	var dump dns.Dump
	if json.Unmarshal(data, &dump) != nil || len(dump.Records) != 1 || dump.SchemaVersion != 1 {
		t.Fatal(string(data))
	}
	code, _, _ = command(t, f, path, "dns", "delete", "app.example.com")
	if code != 2 || f.writes != 2 {
		t.Fatal("non-interactive delete executed")
	}
	code, _, errOut = command(t, f, path, "dns", "delete", "app.example.com", "--yes")
	if code != 0 || f.writes != 3 {
		t.Fatal(code, errOut)
	}
}
func TestCLIAmbiguityAndValidationBeforeWrites(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	f := &apiFixture{record: map[string]any{"id": id, "name": "app.example.com", "type": "A", "content": "192.0.2.1", "ttl": 300}, duplicate: true}
	path := filepath.Join(t.TempDir(), "config.json")
	code, _, _ := command(t, f, path, "dns", "update", "app.example.com", "--content", "192.0.2.2")
	if code != 4 || f.writes != 0 {
		t.Fatal("ambiguous target changed", code)
	}
	for _, args := range [][]string{{"dns", "update", "app.example.com", "--id", id, "--format", "bind", "--content", "192.0.2.2"}, {"dns", "delete", "app.example.com", "--id", id, "--yes", "--format", "invalid"}, {"dns", "create", "x.example.com", "--type", "A", "--content", "::1"}} {
		code, _, _ = command(t, f, path, args...)
		if code == 0 || f.writes != 0 {
			t.Fatal(args, code)
		}
	}
}
func TestInitPreservesOtherProfileFields(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	path := filepath.Join(t.TempDir(), "config.json")
	f := &apiFixture{}
	code, _, errOut := command(t, f, path, "init", "--profile", "personal", "--default-zone", "example.com")
	if code != 0 {
		t.Fatal(errOut)
	}
	code, _, errOut = command(t, f, path, "init", "--profile", "work", "--token-env", "CLOUDFLARE_API_TOKEN")
	if code != 0 {
		t.Fatal(errOut)
	}
	code, _, errOut = command(t, f, path, "init", "--profile", "personal")
	if code != 0 {
		t.Fatal(errOut)
	}
	c, err := config.Load(path)
	if err != nil || len(c.Profiles) != 2 || c.Profiles["personal"].DefaultZone != "example.com" {
		t.Fatal(c, err)
	}
	code, _, _ = command(t, f, path, "profile", "use", "work")
	if code != 0 {
		t.Fatal(code)
	}
	c, _ = config.Load(path)
	if c.DefaultProfile != "work" {
		t.Fatal(c)
	}
}

func TestDumpSupportsServiceAndWildcardOwners(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, owner := range []string{"_acme-challenge.example.com", "_sip._tcp.example.com", "*.example.com"} {
		f := &apiFixture{record: map[string]any{"id": id, "name": owner, "type": "TXT", "content": "verification", "ttl": 1}}
		code, out, errOut := command(t, f, filepath.Join(t.TempDir(), "config.json"), "dns", "dump", owner, "--exact")
		if code != 0 {
			t.Fatal(owner, code, errOut)
		}
		var dump dns.Dump
		if json.Unmarshal([]byte(out), &dump) != nil || len(dump.Records) != 1 || dump.Query.Domain != owner {
			t.Fatal("owner was not exported", out)
		}
	}
}
