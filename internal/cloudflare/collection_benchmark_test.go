package cloudflare

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/gatework/cloudflare-go/internal/output"
)

func collectionFixture(count int, lastPage func()) http.RoundTripper {
	pages := make([]string, (count+99)/100)
	for page := range pages {
		var body strings.Builder
		body.WriteString(`{"success":true,"result":[`)
		for n := 0; n < 100 && page*100+n < count; n++ {
			if n > 0 {
				body.WriteByte(',')
			}
			i := page*100 + n
			typ, content := "TXT", strings.Repeat("x", 1024)
			if i%100 == 0 {
				typ, content = "A", "192.0.2.1"
			}
			fmt.Fprintf(&body, `{"id":"%032x","name":"r%d.example.com","type":"%s","content":"%s","ttl":300,"future":{"n":9007199254740993}}`, i, i, typ, content)
		}
		fmt.Fprintf(&body, `],"result_info":{"page":%d,"total_pages":%d,"total_count":%d}}`, page+1, len(pages), count)
		pages[page] = body.String()
	}
	return responseFunc(func(r *http.Request) (*http.Response, error) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		if err != nil || page < 1 || page > len(pages) {
			return nil, fmt.Errorf("unexpected page")
		}
		if page == len(pages) && lastPage != nil {
			lastPage()
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(pages[page-1])), Request: r}, nil
	})
}

// Includes SDK decoding, pagination, filtering, sorting and final JSON output.
// Fixture construction and real network/disk latency are excluded.
func BenchmarkCollection(b *testing.B) {
	for _, filtered := range []bool{false, true} {
		b.Run(fmt.Sprintf("filtered=%t", filtered), func(b *testing.B) {
			c := New("fixture", "", collectionFixture(1000, nil))
			defer c.Close()
			q := dns.Query{Domain: "example.com"}
			if filtered {
				q.Types = []string{"A", "AAAA"}
			}
			b.ReportAllocs()
			for b.Loop() {
				records, err := c.ListRecords(b.Context(), zoneID, q)
				if err != nil {
					b.Fatal(err)
				}
				if err := output.Render(io.Discard, "json", records, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Explicit diagnostic: samples retained heap before fetching page 100. It is
// not RSS, and GC sampling must not be included in throughput measurements.
func TestCollectionRetainedHeap(t *testing.T) {
	if testing.Short() {
		t.Skip("heap diagnostic")
	}
	var baseline, atLast runtime.MemStats
	base := collectionFixture(10000, func() { runtime.GC(); runtime.ReadMemStats(&atLast) })
	c := New("fixture", "", base)
	defer c.Close()
	runtime.GC()
	runtime.ReadMemStats(&baseline)
	records, err := c.ListRecords(t.Context(), zoneID, dns.Query{Types: []string{"A", "AAAA"}})
	if err != nil || len(records) != 100 {
		t.Fatal(len(records), err)
	}
	t.Logf("collection retained heap delta: %d bytes (10,000 records, 1%% matched)", int64(atLast.HeapAlloc)-int64(baseline.HeapAlloc))
	runtime.KeepAlive(base)
}
