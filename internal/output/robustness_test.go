package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestCSVUsesActualResultFields(t *testing.T) {
	for _, value := range []any{
		[]dns.Zone{{ID: "z", Name: "example.com", AccountID: "account", Status: "active"}},
		dns.Change{Action: "delete", Zone: dns.Zone{Name: "example.com"}, RecordID: "record"},
		map[string]any{"id": "record", "deleted": true},
		[]map[string]any{{"name": "work", "profile": map[string]string{"token_env": "WORK_TOKEN"}}},
	} {
		var buffer bytes.Buffer
		if err := Render(&buffer, "csv", value, false); err != nil {
			t.Fatal(err)
		}
		rows, err := csv.NewReader(&buffer).ReadAll()
		if err != nil || len(rows) != 2 {
			t.Fatal(rows, err)
		}
		for i, header := range rows[0] {
			if header == "record_json" {
				t.Fatal("non-record output used DNS schema")
			}
			if rows[1][i] == "" {
				t.Fatalf("field %q silently lost", header)
			}
		}
	}
}

func TestRecordJSONRemainsOriginalInCSV(t *testing.T) {
	raw := `{"id":"a","name":"example.com","type":"TXT","content":"x","comment":null,"counter":9007199254740993}`
	record, err := dns.ParseRecord([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err = Render(&buffer, "csv", dns.Dump{Zone: dns.Zone{ID: "zone", Name: "example.com"}, Records: []dns.Record{record}}, false); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buffer).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if rows[1][0] != "zone" || rows[1][1] != "example.com" || rows[1][len(rows[1])-1] != raw {
		t.Fatal("raw record was changed", rows)
	}
	if cell(json.RawMessage(`null`)) != "null" || cell(json.RawMessage(`""`)) != "" {
		t.Fatal("null collapsed into empty string")
	}
}

func TestStreamingJSONMatchesDomainValues(t *testing.T) {
	record, err := dns.ParseRecord([]byte(`{"id":"r","name":"example.com","type":"TXT","content":"<unicode> 中文\n\t","future":{"nested":[null,9007199254740993]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{record, []dns.Record{}, []dns.Record{record, record}, dns.Dump{SchemaVersion: 1, Records: []dns.Record{}}, dns.Dump{SchemaVersion: 1, Records: []dns.Record{record}}} {
		var buffer bytes.Buffer
		if err = Render(&buffer, "json", value, false); err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(value)
		if err != nil || !dns.EqualJSON(want, buffer.Bytes()) {
			t.Fatal(string(want), buffer.String(), err)
		}
	}
}

func TestBufferedOutputPropagatesErrors(t *testing.T) {
	record, err := dns.ParseRecord([]byte(`{"id":"r","name":"example.com","type":"TXT","content":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "csv", "txt", "table"} {
		if err := Render(broken{}, format, []dns.Record{record}, false); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(format, err)
		}
	}
}

func TestTableHandlesLargeUnicodeContents(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"id": "r", "name": "example.com", "type": "TXT", "content": strings.Repeat("中", 101)})
	record, err := dns.ParseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	records := make([]dns.Record, 600)
	for i := range records {
		records[i] = record
	}
	var buffer bytes.Buffer
	if err := Render(&buffer, "table", records, false); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buffer.String(), "example.com") != 600 || strings.Count(buffer.String(), "中") != 600*100 {
		t.Fatal("table truncated records or Unicode incorrectly")
	}
}
