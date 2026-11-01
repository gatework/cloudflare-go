package output

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gatework/cloudflare-go/internal/dns"
)

// Keep fixtures outside the timer: this measures rendering an already collected
// export, not API transfer or record decoding.
func BenchmarkDump(b *testing.B) {
	records := make([]dns.Record, 1000)
	for i := range records {
		raw := fmt.Sprintf(`{"id":"%032x","name":"host%d.example.com","type":"TXT","content":"%s","ttl":300,"proxied":false,"future":{"n":9007199254740993},"comment":null}`, i, i, strings.Repeat("x", 1024))
		var err error
		records[i], err = dns.ParseRecord([]byte(raw))
		if err != nil {
			b.Fatal(err)
		}
	}
	dump := dns.Dump{SchemaVersion: 1, ExportedAt: time.Unix(0, 0).UTC(), Zone: dns.Zone{ID: "zone", Name: "example.com"}, Records: records}
	for _, format := range []string{"json", "jsonl", "csv", "txt", "table"} {
		b.Run(format, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := Render(io.Discard, format, dump, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
