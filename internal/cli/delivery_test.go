package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/cloudflare"
	"github.com/gatework/cloudflare-go/internal/dns"
)

type createClient struct {
	dns.API
	mutation *cloudflare.Client
}

func (a createClient) CreateRecord(ctx context.Context, zone string, patch dns.RecordPatch) (dns.Record, error) {
	return a.mutation.CreateRecord(ctx, zone, patch)
}

func TestUnsentWriteExitCodes(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "fixture-token")
	t.Setenv("CLOUDFLARE_PROFILE", "")
	for _, canceled := range []bool{false, true} {
		f := &apiFixture{}
		server := httptest.NewServer(http.HandlerFunc(f.serve))
		ctx, cancel := context.WithCancel(t.Context())
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.Proxy = nil
		base.DialContext = func(context.Context, string, string) (net.Conn, error) {
			if canceled {
				cancel()
			}
			return nil, errors.New("transport credential must not leak")
		}
		var output, diagnostic bytes.Buffer
		a := &app{in: strings.NewReader(""), out: &output, err: &diagnostic, path: filepath.Join(t.TempDir(), "config.json"), factory: func(token string) (dns.API, func()) {
			read := cloudflare.New(token, server.URL+"/", server.Client().Transport)
			write := cloudflare.New(token, "https://unused.example/", base)
			return createClient{read, write}, func() { read.Close(); write.Close() }
		}}
		code := a.execute(ctx, []string{"dns", "create", "app.example.com", "--type", "A", "--content", "192.0.2.1"})
		cancel()
		base.CloseIdleConnections()
		server.Close()
		expected := 1
		if canceled {
			expected = 130
		}
		if code != expected || f.writes != 0 || output.Len() != 0 || strings.Contains(diagnostic.String(), "transport credential") {
			t.Fatal(code, expected, f.writes, diagnostic.String())
		}
	}
}
