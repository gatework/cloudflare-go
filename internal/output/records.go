package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/gatework/cloudflare-go/internal/dns"
)

var recordColumns = []string{"zone_id", "zone_name", "id", "type", "name", "content", "ttl", "proxied", "proxiable", "priority", "comment", "tags", "data", "settings", "meta", "private_routing", "created_on", "modified_on", "comment_modified_on", "tags_modified_on", "record_json"}

func recordValues(value any) ([]dns.Record, dns.Zone, bool) {
	switch v := value.(type) {
	case dns.Record:
		return []dns.Record{v}, dns.Zone{}, true
	case []dns.Record:
		return v, dns.Zone{}, true
	case dns.Dump:
		return v.Records, v.Zone, true
	default:
		return nil, dns.Zone{}, false
	}
}

func renderRecords(w io.Writer, format string, value any, records []dns.Record, zone dns.Zone, safe bool) error {
	switch format {
	case "jsonl":
		var buffer bytes.Buffer
		for _, record := range records {
			buffer.Reset()
			if err := json.Compact(&buffer, record.Raw); err != nil {
				return err
			}
			buffer.WriteByte('\n')
			if _, err := w.Write(buffer.Bytes()); err != nil {
				return err
			}
		}
		return nil
	case "json":
		return recordsJSON(w, value, records)
	case "table":
		return recordsTable(w, records)
	case "txt":
		zoneID, _ := json.Marshal(zone.ID)
		zoneName, _ := json.Marshal(zone.Name)
		for i, record := range records {
			var row map[string]json.RawMessage
			if err := json.Unmarshal(record.Raw, &row); err != nil {
				return err
			}
			if zone.ID != "" {
				row["zone_id"] = zoneID
				row["zone_name"] = zoneName
			}
			if i > 0 {
				if _, err := fmt.Fprintln(w); err != nil {
					return err
				}
			}
			keys := make([]string, 0, len(row))
			for key := range row {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, err := fmt.Fprintf(w, "%s: %s\n", escape(key), escape(cell(row[key]))); err != nil {
					return err
				}
			}
		}
		return nil
	case "csv":
		writer := csv.NewWriter(w)
		if err := writer.Write(recordColumns); err != nil {
			return err
		}
		values := make([]string, len(recordColumns))
		for _, record := range records {
			var row map[string]json.RawMessage
			if err := json.Unmarshal(record.Raw, &row); err != nil {
				return err
			}
			for i, key := range recordColumns {
				switch key {
				case "zone_id":
					values[i] = zone.ID
				case "zone_name":
					values[i] = zone.Name
				case "record_json":
					values[i] = string(record.Raw)
				default:
					values[i] = cell(row[key])
				}
				if safe {
					values[i] = spreadsheet(values[i])
				}
			}
			if err := writer.Write(values); err != nil {
				return err
			}
		}
		writer.Flush()
		return writer.Error()
	default:
		return fmt.Errorf("记录不支持 %s 输出格式", format)
	}
}

func recordsJSON(w io.Writer, value any, records []dns.Record) error {
	if record, ok := value.(dns.Record); ok {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		encoder.SetEscapeHTML(false)
		return encoder.Encode(record)
	}
	prefix := ""
	if dump, ok := value.(dns.Dump); ok {
		header := struct {
			SchemaVersion int       `json:"schema_version"`
			ExportedAt    time.Time `json:"exported_at"`
			Zone          dns.Zone  `json:"zone"`
			Query         dns.Query `json:"query"`
		}{dump.SchemaVersion, dump.ExportedAt, dump.Zone, dump.Query}
		data, err := json.MarshalIndent(header, "", "  ")
		if err != nil {
			return err
		}
		if _, err = w.Write(bytes.TrimSuffix(data, []byte("\n}"))); err != nil {
			return err
		}
		if _, err = io.WriteString(w, ",\n  \"records\": "); err != nil {
			return err
		}
		prefix = "  "
	}
	if _, err := io.WriteString(w, "["); err != nil {
		return err
	}
	var buffer bytes.Buffer
	indent := prefix + "  "
	separator := "\n" + indent
	nextSeparator := ",\n" + indent
	for i, record := range records {
		buffer.Reset()
		if err := json.Indent(&buffer, record.Raw, indent, "  "); err != nil {
			return err
		}
		if i > 0 {
			separator = nextSeparator
		}
		if _, err := io.WriteString(w, separator); err != nil {
			return err
		}
		if _, err := w.Write(buffer.Bytes()); err != nil {
			return err
		}
	}
	if len(records) > 0 {
		if _, err := io.WriteString(w, "\n"+prefix); err != nil {
			return err
		}
	}
	ending := "]\n"
	if prefix != "" {
		ending = "]\n}\n"
	}
	_, err := io.WriteString(w, ending)
	return err
}

func recordsTable(w io.Writer, records []dns.Record) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "NAME\tTYPE\tCONTENT\tTTL\tPROXIED\tID"); err != nil {
		return err
	}
	for i, record := range records {
		content := record.Content
		if content == "" {
			var fields struct {
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(record.Raw, &fields); err != nil {
				return err
			}
			content = cell(fields.Data)
		}
		// Limit runes without allocating a full []rune for long TXT contents.
		count := 0
		for offset := range content {
			if count == 100 {
				content = content[:offset] + "…"
				break
			}
			count++
		}
		if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%d\t%t\t%s\n", escape(record.Name), escape(record.Type), escape(content), record.TTL, record.Proxied, escape(record.ID)); err != nil {
			return err
		}
		// tabwriter retains an entire table until Flush. A bounded block avoids
		// another full-export buffer; each block keeps its own aligned columns.
		if (i+1)%256 == 0 {
			if err := table.Flush(); err != nil {
				return err
			}
		}
	}
	return table.Flush()
}
