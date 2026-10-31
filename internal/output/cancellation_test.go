package output

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

type cancelWriter struct {
	cancel context.CancelFunc
	calls  int
}

func (w *cancelWriter) Write(p []byte) (int, error) { w.calls++; w.cancel(); return len(p), nil }

func TestOutputStopsAfterCancellation(t *testing.T) {
	r, err := dns.ParseRecord([]byte(fmt.Sprintf(`{"id":"a","name":"example.com","type":"TXT","content":"%s"}`, strings.Repeat("x", 1024))))
	if err != nil {
		t.Fatal(err)
	}
	records := make([]dns.Record, 1000)
	for i := range records {
		records[i] = r
	}
	for _, format := range []string{"json", "csv", "txt", "table"} {
		ctx, cancel := context.WithCancel(t.Context())
		w := &cancelWriter{cancel: cancel}
		err := RenderContext(ctx, w, format, records, false)
		cancel()
		if !errors.Is(err, context.Canceled) || w.calls != 1 {
			t.Fatalf("%s calls=%d err=%v", format, w.calls, err)
		}
	}
}
