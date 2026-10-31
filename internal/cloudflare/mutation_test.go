package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestDeleteVerificationRequiresRecordNotFound(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		confirmed  bool
	}{
		{"proxy HTML", "<html>upstream not found</html>", false},
		{"router error", `{"success":false,"errors":[{"code":7003}]}`, false},
		{"malformed envelope", `{"errors":[{"code":81044}]}`, false},
		{"missing record", `{"success":false,"errors":[{"code":81044}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, writes atomic.Int32
			before, err := dns.ParseRecord([]byte(`{"id":"` + recordID + `","name":"example.com","type":"TXT","content":"original"}`))
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == http.MethodDelete {
					writes.Add(1)
					w.WriteHeader(502)
					fmt.Fprint(w, "lost write response")
					return
				}
				if reads.Add(1) == 1 {
					fmt.Fprint(w, envelope(string(before.Raw)))
					return
				}
				w.WriteHeader(404)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client := New("token", server.URL+"/", server.Client().Transport)
			defer client.Close()
			service := dns.Service{API: client}
			_, err = service.Apply(t.Context(), dns.Change{Action: "delete", Zone: dns.Zone{ID: zoneID}, RecordID: recordID, Before: &before})
			if tc.confirmed && err != nil || !tc.confirmed && !errors.Is(err, dns.ErrUncertain) {
				t.Fatal("incorrect write outcome", err)
			}
			if writes.Load() != 1 || reads.Load() != 2 {
				t.Fatal("mutation repeated or verification skipped", writes.Load(), reads.Load())
			}
		})
	}
}

func TestDefinitiveFailureRequiresAPIEnvelope(t *testing.T) {
	for _, body := range []string{`{"success":false,"errors":[{"code":10000,"message":"secret"}]}`, `<html>forbidden</html>`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(403)
			fmt.Fprint(w, body)
		}))
		client := New("token", server.URL+"/", server.Client().Transport)
		_, err := client.CreateRecord(context.Background(), zoneID, dns.RecordPatch{"name": json.RawMessage(`"example.com"`)})
		client.Close()
		server.Close()
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Definitive() != strings.HasPrefix(body, "{") || strings.Contains(err.Error(), "secret") {
			t.Fatal(err)
		}
	}
}

func TestConcurrentClientCallsKeepMetadataIndependent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		w.Header().Set("Cf-Ray", id)
		w.WriteHeader(403)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":10000}]}`)
	}))
	defer server.Close()
	client := New("token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	for i := range 16 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			// The parent test owns the server/client until all goroutines finish.
			const calls = 4
			done := make(chan error, calls)
			for n := range calls {
				go func() {
					id := fmt.Sprintf("%032x", i*calls+n)
					_, err := client.GetRecord(t.Context(), zoneID, id)
					var apiErr *Error
					if !errors.As(err, &apiErr) || apiErr.RequestID != id {
						done <- fmt.Errorf("metadata crossed requests: %v", err)
						return
					}
					done <- nil
				}()
			}
			for range calls {
				if err := <-done; err != nil {
					t.Error(err)
				}
			}
		})
	}
}
