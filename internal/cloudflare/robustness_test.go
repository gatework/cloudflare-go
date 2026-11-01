package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

type closingTransport struct{ closed bool }

func (*closingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("unused")
}
func (t *closingTransport) CloseIdleConnections() { t.closed = true }

func TestCloseReleasesUnderlyingConnections(t *testing.T) {
	base := &closingTransport{}
	client := New("token", "", base)
	client.Close()
	if !base.closed {
		t.Fatal("wrapped pool not closed")
	}
}

func TestIncompletePaginationFails(t *testing.T) {
	for _, endpoint := range []string{"zones", "records"} {
		t.Run(endpoint, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				calls.Add(1)
				page := r.URL.Query().Get("page")
				result := `[{"id":"1","name":"example.com","type":"TXT"}]`
				if page == "2" {
					result = `[]`
				}
				fmt.Fprintf(w, `{"success":true,"result":%s,"result_info":{"page":%s,"total_pages":3}}`, result, page)
			}))
			defer server.Close()
			client := New("token", server.URL+"/", server.Client().Transport)
			defer client.Close()
			var err error
			if endpoint == "zones" {
				_, err = client.ListZones(t.Context(), "", "", 1<<20)
			} else {
				_, err = client.ListRecords(t.Context(), zoneID, dns.Query{}, 1<<20)
			}
			if err == nil || calls.Load() != 2 {
				t.Fatal("incomplete result accepted", err, calls.Load())
			}
		})
	}
}

func TestRejectMismatchedAPIIdentitiesAndShapes(t *testing.T) {
	for _, operation := range []string{"zone", "get", "update", "delete", "list"} {
		t.Run(operation, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				calls.Add(1)
				fmt.Fprint(w, envelope(`{"id":"wrong","name":"example.com","type":"TXT"}`))
			}))
			defer server.Close()
			client := New("token", server.URL+"/", server.Client().Transport)
			defer client.Close()
			var err error
			switch operation {
			case "zone":
				_, err = client.GetZone(t.Context(), zoneID)
			case "get":
				_, err = client.GetRecord(t.Context(), zoneID, recordID)
			case "update":
				_, err = client.UpdateRecord(t.Context(), zoneID, recordID, dns.RecordPatch{})
			case "delete":
				err = client.DeleteRecord(t.Context(), zoneID, recordID)
			case "list":
				_, err = client.ListRecords(t.Context(), zoneID, dns.Query{}, 1<<20)
			}
			if err == nil {
				t.Fatal("invalid identity/shape accepted")
			}
			if (operation == "update" || operation == "delete") && calls.Load() != 1 {
				t.Fatal("mutation repeated")
			}
		})
	}
}

func TestFiltersReachAPIAndStillMatchLocally(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for key, want := range map[string]string{"type": "TXT", "proxied": "false", "content.exact": "CaseSensitive", "name.endswith": "example.com", "match": "all"} {
			if q.Get(key) != want {
				t.Errorf("%s = %q, want %q", key, q.Get(key), want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"success":true,"result":[{"id":"1","type":"TXT","name":"example.com","content":"CaseSensitive"},{"id":"2","type":"TXT","name":"notexample.com","content":"CaseSensitive"},{"id":"3","type":"TXT","name":"example.com","content":"casesensitive"}],"result_info":{"page":1,"total_pages":1}}`)
	}))
	defer server.Close()
	client := New("token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	proxied, content := false, "CaseSensitive"
	records, err := client.ListRecords(t.Context(), zoneID, dns.Query{Domain: "example.com", Types: []string{"TXT"}, Proxied: &proxied, Content: &content}, 1<<20)
	if err != nil || len(records) != 1 || records[0].ID != "1" {
		t.Fatal(records, err)
	}
}

type retryTransport struct{ calls int }

func (t *retryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	t.calls++
	if t.calls > 1 {
		return nil, errors.New("credential must not leak")
	}
	return &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": []string{"0"}, "Cf-Ray": []string{"stale-request"}}, Body: io.NopCloser(strings.NewReader(`{"success":false,"errors":[{"code":1015}]}`)), Request: r}, nil
}

func TestRetriesDoNotReuseResponseMetadata(t *testing.T) {
	base := &retryTransport{}
	client := New("token", "", base)
	defer client.Close()
	_, err := client.GetRecord(context.Background(), zoneID, recordID)
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Status != 0 || apiErr.RequestID != "" || len(apiErr.Codes) != 0 {
		t.Fatal("stale metadata", err)
	}
	if strings.Contains(err.Error(), "credential") {
		t.Fatal("transport error leaked")
	}
}
