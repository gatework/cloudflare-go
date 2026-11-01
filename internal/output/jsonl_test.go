package output

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestJSONLPreservesEachOriginalRecord(t *testing.T) {
	r, err := dns.ParseRecord([]byte(`{ "id":"1", "name":"example.com", "type":"TXT", "content":"first\nsecond", "future":{"n":9007199254740993,"value":null} }`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{r, []dns.Record{r}, dns.Dump{Records: []dns.Record{r}}} {
		var out bytes.Buffer
		if err := Render(&out, "jsonl", value, false); err != nil {
			t.Fatal(err)
		}
		if strings.Count(out.String(), "\n") != 1 || !dns.EqualJSON(out.Bytes(), r.Raw) {
			t.Fatal(out.String())
		}
	}
	var empty bytes.Buffer
	if err := Render(&empty, "jsonl", []dns.Record{}, false); err != nil || empty.Len() != 0 {
		t.Fatal(empty.String(), err)
	}
}
