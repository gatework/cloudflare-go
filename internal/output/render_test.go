package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"github.com/gatework/cloudflare-go/internal/dns"
	"io"
	"strings"
	"testing"
)

func TestFormatsPreserveFullRecord(t *testing.T) {
	r, err := dns.ParseRecord([]byte(`{"id":"a","type":"TXT","name":"a.example.com","content":"=SUM(1,2)\nquoted \"text\"","future":{"n":9007199254740993},"data":{"value":"x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"txt", "csv", "json", "table"} {
		var b bytes.Buffer
		if err = Render(&b, format, []dns.Record{r}, false); err != nil {
			t.Fatal(format, err)
		}
		if format == "json" && !json.Valid(b.Bytes()) {
			t.Fatal("invalid JSON")
		}
		if format == "csv" {
			rows, e := csv.NewReader(&b).ReadAll()
			if e != nil || len(rows) != 2 {
				t.Fatal(e, rows)
			}
			if !strings.Contains(rows[1][len(rows[1])-1], "9007199254740993") {
				t.Fatal("unknown field lost")
			}
		}
	}
	var b bytes.Buffer
	if err = Render(&b, "csv", []dns.Record{r}, true); err != nil {
		t.Fatal(err)
	}
	rows, _ := csv.NewReader(&b).ReadAll()
	if !strings.HasPrefix(rows[1][5], "'=SUM") {
		t.Fatal("unsafe CSV", rows)
	}
}

type broken struct{}

func (broken) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestOutputErrorsAndTerminalEscapes(t *testing.T) {
	if err := Render(broken{}, "table", []map[string]string{{"content": "a"}}, false); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
	var b bytes.Buffer
	_ = Render(&b, "txt", map[string]string{"content": "\x1b]0;evil\a"}, false)
	if strings.ContainsRune(b.String(), '\x1b') {
		t.Fatal("terminal control not escaped")
	}
}
