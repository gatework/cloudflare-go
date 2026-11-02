package cloudflare

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

type responseFunc func(*http.Request) (*http.Response, error)

func (f responseFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestExportRejectsErrorDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, media, body string
		status            int
		valid             bool
	}{
		{"zone", "text/plain; charset=utf-8", "; zone\nexample.com. 300 IN A 192.0.2.1\n", 200, true},
		{"HTML", "text/html", "<html>Bad gateway</html>", 200, false},
		{"disguised HTML", "text/plain", " <!DOCTYPE html><html>Error</html>", 200, false},
		{"JSON error", "text/plain", `{"success":false,"errors":[{"code":1000}]}`, 200, false},
		{"empty", "text/plain", " \n", 200, false},
		{"binary", "text/plain", "zone\x00file", 200, false},
		{"invalid encoding", "text/plain", "zone\xff", 200, false},
		{"partial", "text/plain", "example.com. IN A 192.0.2.1", 206, false},
		{"redirect", "text/plain", "redirect", 302, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := responseFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.media}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})
			request, _ := http.NewRequestWithContext(t.Context(), "GET", BaseURL+"zones/id/dns_records/export", nil)
			response, err := (transport{base}).RoundTrip(request)
			if response != nil {
				defer response.Body.Close()
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestPaginationChecksStatistics(t *testing.T) {
	for _, tc := range []struct {
		name, first, second string
		valid               bool
	}{
		{"missing metadata", `{}`, `{}`, true},
		{"count mismatch", `{"page":1,"count":2,"total_pages":1}`, `{}`, false},
		{"explicit zero page", `{"page":0}`, `{}`, false},
		{"zero total with records", `{"total_count":0}`, `{}`, false},
		{"changing total", `{"page":1,"total_count":2,"total_pages":2}`, `{"page":2,"total_count":3,"total_pages":2}`, false},
		{"changing page total", `{"page":1,"total_pages":3}`, `{"page":2,"total_pages":2}`, false},
		{"missing record", `{"page":1,"total_count":3,"total_pages":2}`, `{"page":2,"total_count":3,"total_pages":2}`, false},
		{"valid", `{"page":1,"count":1,"total_count":2,"total_pages":2}`, `{"page":2,"count":1,"total_count":2,"total_pages":2}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			base := responseFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				info, result := tc.first, fmt.Sprintf(`[{"id":"%032x","name":"example.com","type":"TXT"}]`, calls)
				if calls > 1 {
					info = tc.second
				}
				if calls > 2 {
					result = `[]`
				}
				body := fmt.Sprintf(`{"success":true,"result":%s,"result_info":%s}`, result, info)
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			c := New("fixture", "", base)
			defer c.Close()
			_, err := c.ListRecords(t.Context(), zoneID, dns.Query{})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}
