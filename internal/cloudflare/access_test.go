package cloudflare

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDNSAccessCheckIsOneBoundedRead(t *testing.T) {
	calls := 0
	base := responseFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "GET" || r.URL.Query().Get("per_page") != "1" || r.URL.Query().Get("page") != "1" {
			t.Error(r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"success":true,"result":[],"result_info":{"total_pages":100}}`)), Request: r}, nil
	})
	c := New("fixture", "", base)
	defer c.Close()
	if err := c.CheckDNSRead(t.Context(), zoneID); err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
}
