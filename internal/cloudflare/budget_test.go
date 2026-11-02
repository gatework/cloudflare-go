package cloudflare

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestResponseBudgetCountsAutomaticallyDecodedBody(t *testing.T) {
	body := `{"success":true,"result":{"id":"` + zoneID + `","name":"example.com"},"padding":"` + strings.Repeat("x", 4096) + `"}`
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(w, body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		w.Write(compressed.Bytes())
	}))
	defer server.Close()
	c := New("fixture", server.URL+"/", nil)
	defer c.Close()
	for _, limit := range []int64{int64(len(body)), int64(len(body) - 1)} {
		ctx, stop := WithResponseBudget(t.Context(), limit)
		_, err := c.GetZone(ctx, zoneID)
		stop()
		if limit == int64(len(body)) && err != nil || limit < int64(len(body)) && !errors.Is(err, ErrResponseBudget) {
			t.Fatal("budget must count decoded body bytes", limit, compressed.Len(), err)
		}
	}
}

func TestResponseBudgetIncludesEnvelopeAndStopsFollowingCalls(t *testing.T) {
	body := envelope(`{"id":"` + zoneID + `","name":"example.com"}`)
	calls := 0
	base := responseFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	c := New("fixture", "", base)
	defer c.Close()
	ctx, stop := WithResponseBudget(t.Context(), int64(len(body)))
	defer stop()
	if _, err := c.GetZone(ctx, zoneID); err != nil {
		t.Fatal("exact budget rejected", err)
	}
	if _, err := c.GetZone(ctx, zoneID); !errors.Is(err, ErrResponseBudget) {
		t.Fatal("spent budget allowed another request", err)
	}
	if calls != 1 {
		t.Fatal("sent a request with no remaining budget", calls)
	}
}

func TestBudgetCountsRetryErrorsAndClosesBothBodies(t *testing.T) {
	errorBody := `{"success":false,"errors":[{"code":1015}]}`
	successBody := envelope(`{"id":"` + zoneID + `","name":"example.com"}`)
	calls, closes := 0, 0
	base := responseFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		status, body := 503, errorBody
		if calls > 1 {
			status, body = 200, successBody
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"0"}}, Body: &observedBody{Reader: strings.NewReader(body), closed: func() { closes++ }}, Request: r}, nil
	})
	c := New("fixture", "", base)
	defer c.Close()
	ctx, stop := WithResponseBudget(t.Context(), int64(len(errorBody)+len(successBody)-1))
	defer stop()
	_, err := c.GetZone(ctx, zoneID)
	if !errors.Is(err, ErrResponseBudget) || errors.Is(err, context.Canceled) || calls != 2 || closes != 2 {
		t.Fatal("retry bypassed the shared budget or leaked a body", err, calls, closes)
	}
}

type observedBody struct {
	io.Reader
	closed func()
}

func (b *observedBody) Close() error { b.closed(); return nil }

func TestSpentBudgetPreventsMutationWithoutUncertainResult(t *testing.T) {
	body := envelope(`{"id":"` + zoneID + `","name":"example.com"}`)
	calls := 0
	c := New("fixture", "", responseFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	defer c.Close()
	ctx, stop := WithResponseBudget(t.Context(), int64(len(body)))
	defer stop()
	if _, err := c.GetZone(ctx, zoneID); err != nil {
		t.Fatal(err)
	}
	s := dns.Service{API: c}
	_, err := s.Apply(ctx, dns.Change{Action: "create", Zone: dns.Zone{ID: zoneID}, Changes: dns.RecordPatch{"type": json.RawMessage(`"TXT"`)}})
	if !errors.Is(err, ErrResponseBudget) || errors.Is(err, dns.ErrUncertain) || calls != 1 {
		t.Fatal("spent budget sent a mutation or lost its classification", err, calls)
	}
}

func TestBudgetAcceptsMaximumInt64(t *testing.T) {
	c := New("fixture", "", responseFixture(envelope(`{"id":"`+zoneID+`","name":"example.com"}`)))
	defer c.Close()
	ctx, stop := WithResponseBudget(t.Context(), math.MaxInt64)
	defer stop()
	if _, err := c.GetZone(ctx, zoneID); err != nil {
		t.Fatal(err)
	}
}
