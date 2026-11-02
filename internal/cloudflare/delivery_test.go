package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestUnsentMutationIsDefinitiveAndPreservesCancellation(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		var calls atomic.Int32
		base := http.DefaultTransport.(*http.Transport).Clone()
		base.Proxy = nil
		base.DialContext = func(context.Context, string, string) (net.Conn, error) {
			calls.Add(1)
			if canceled {
				cancel()
			}
			return nil, errors.New("fixture credential must not leak")
		}
		client := New("fixture", "https://unused.example/", base)
		service := dns.Service{API: client}
		_, err := service.Apply(ctx, dns.Change{Action: "create", Zone: dns.Zone{ID: zoneID}, Changes: dns.RecordPatch{"type": json.RawMessage(`"TXT"`)}})
		cancel()
		client.Close()
		base.CloseIdleConnections()
		if err == nil || errors.Is(err, dns.ErrUncertain) || calls.Load() != 1 || strings.Contains(err.Error(), "fixture credential") {
			t.Fatal("unsent request was uncertain, retried or leaked diagnostics", err, calls.Load())
		}
		if canceled && !errors.Is(err, context.Canceled) {
			t.Fatal("lost cancellation", err)
		}
	}
}

func TestUnknownTransportCannotProveMutationWasUnsent(t *testing.T) {
	client := New("fixture", "", responseFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("untraced failure")
	}))
	defer client.Close()
	service := dns.Service{API: client}
	_, err := service.Apply(t.Context(), dns.Change{Action: "create", Zone: dns.Zone{ID: zoneID}})
	if !errors.Is(err, dns.ErrUncertain) {
		t.Fatal("untraced failure was treated as proof", err)
	}
}

func TestRegisteredProtocolCannotProveMutationWasUnsent(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport).Clone()
	defer base.CloseIdleConnections()
	base.RegisterProtocol("https", responseFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("alternate protocol lost response")
	}))
	c := New("fixture", "", base)
	defer c.Close()
	s := dns.Service{API: c}
	_, err := s.Apply(t.Context(), dns.Change{Action: "create", Zone: dns.Zone{ID: zoneID}})
	if !errors.Is(err, dns.ErrUncertain) {
		t.Fatal("registered protocol supplied no connection evidence", err)
	}
}

func TestCloseReleasesOwnedDefaultPool(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(envelope(`{"id":"` + recordID + `","name":"example.com","type":"TXT"}`)))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			select {
			case closed <- struct{}{}:
			default:
			}
		}
	}
	server.Start()
	defer server.Close()
	client := New("fixture", server.URL+"/", nil)
	defer client.Close()
	if _, err := client.GetRecord(t.Context(), zoneID, recordID); err != nil {
		t.Fatal(err)
	}
	client.Close()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("owned idle connection remained open")
	}
}

func TestMutationWithLostResponseRemainsUncertain(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writes.Add(1)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	client := New("fixture", server.URL+"/", server.Client().Transport)
	defer client.Close()
	service := dns.Service{API: client}
	_, err := service.Apply(t.Context(), dns.Change{Action: "create", Zone: dns.Zone{ID: zoneID}, Changes: dns.RecordPatch{"type": json.RawMessage(`"TXT"`)}})
	if !errors.Is(err, dns.ErrUncertain) || writes.Load() != 1 {
		t.Fatal("sent mutation was repeated or reported as definite", err, writes.Load())
	}
}

func TestLocalEncodingFailureDoesNotBecomeUncertain(t *testing.T) {
	client := New("fixture", "", responseFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("encoded invalid input reached transport")
		return nil, nil
	}))
	defer client.Close()
	s := dns.Service{API: client}
	_, err := s.Apply(t.Context(), dns.Change{Action: "create", Zone: dns.Zone{ID: zoneID}, Changes: dns.RecordPatch{"content": json.RawMessage(`not-json`)}})
	if err == nil || errors.Is(err, dns.ErrUncertain) {
		t.Fatal("local encoding failure was uncertain", err)
	}
}

func TestTLSFailureIsDefinitivelyUnsent(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = nil
	defer base.CloseIdleConnections()
	c := New("fixture", server.URL+"/", base)
	defer c.Close()
	s := dns.Service{API: c}
	_, err := s.Apply(t.Context(), dns.Change{Action: "create", Zone: dns.Zone{ID: zoneID}})
	if err == nil || errors.Is(err, dns.ErrUncertain) || requests.Load() != 0 {
		t.Fatal("TLS failure lost its unsent classification", err, requests.Load())
	}
}
