// Package output encodes results without making API calls or writing diagnostics.
package output

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
)

func Valid(format string) bool {
	switch format {
	case "table", "txt", "csv", "json", "bind":
		return true
	}
	return false
}
func Render(w io.Writer, format string, value any, safe bool) error {
	if format == "bind" {
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("BIND 格式仅支持整 Zone 导出")
		}
		_, err := io.WriteString(w, text)
		return err
	}
	if format == "json" {
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		e.SetEscapeHTML(false)
		return e.Encode(value)
	}
	rows, err := objects(value)
	if err != nil {
		return err
	}
	switch format {
	case "csv":
		columns := []string{"zone_id", "zone_name", "id", "type", "name", "content", "ttl", "proxied", "proxiable", "priority", "comment", "tags", "data", "settings", "meta", "private_routing", "created_on", "modified_on", "comment_modified_on", "tags_modified_on", "record_json"}
		csvWriter := csv.NewWriter(w)
		if err = csvWriter.Write(columns); err != nil {
			return err
		}
		for _, row := range rows {
			values := make([]string, len(columns))
			for i, key := range columns {
				if key == "record_json" {
					b, _ := json.Marshal(row)
					values[i] = string(b)
				} else {
					values[i] = cell(row[key])
				}
				if safe {
					values[i] = spreadsheet(values[i])
				}
			}
			if err = csvWriter.Write(values); err != nil {
				return err
			}
		}
		csvWriter.Flush()
		return csvWriter.Error()
	case "txt":
		for i, row := range rows {
			if i > 0 {
				if _, err = fmt.Fprintln(w); err != nil {
					return err
				}
			}
			keys := make([]string, 0, len(row))
			for key := range row {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, err = fmt.Fprintf(w, "%s: %s\n", escape(key), escape(cell(row[key]))); err != nil {
					return err
				}
			}
		}
		return nil
	case "table":
		tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		if _, err = fmt.Fprintln(tw, "NAME\tTYPE\tCONTENT\tTTL\tPROXIED\tID"); err != nil {
			return err
		}
		for _, row := range rows {
			content := cell(row["content"])
			if content == "" {
				content = cell(row["data"])
			}
			if len(content) > 100 {
				content = string([]rune(content)[:min(100, len([]rune(content)))]) + "…"
			}
			if _, err = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", escape(cell(row["name"])), cell(row["type"]), escape(content), cell(row["ttl"]), cell(row["proxied"]), escape(cell(row["id"]))); err != nil {
				return err
			}
		}
		return tw.Flush()
	}
	return fmt.Errorf("未知输出格式")
}
func objects(value any) ([]map[string]json.RawMessage, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var rows []map[string]json.RawMessage
	if len(data) > 0 && data[0] == '[' {
		err = json.Unmarshal(data, &rows)
		if rows == nil {
			rows = []map[string]json.RawMessage{}
		}
		return rows, err
	}
	var object map[string]json.RawMessage
	if err = json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if raw, ok := object["records"]; ok {
		if err = json.Unmarshal(raw, &rows); err != nil {
			return nil, err
		}
		var zone map[string]json.RawMessage
		_ = json.Unmarshal(object["zone"], &zone)
		for _, row := range rows {
			row["zone_id"] = zone["id"]
			row["zone_name"] = zone["name"]
		}
		return rows, nil
	}
	return []map[string]json.RawMessage{object}, nil
}
func cell(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var out bytes.Buffer
	if json.Compact(&out, raw) == nil {
		return out.String()
	}
	return ""
}
func escape(value string) string {
	quoted := strconv.Quote(value)
	return strings.TrimSuffix(strings.TrimPrefix(quoted, "\""), "\"")
}
func spreadsheet(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}
