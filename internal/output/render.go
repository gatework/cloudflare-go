// Package output encodes results without making API calls or writing diagnostics.
package output

import (
	"bufio"
	"bytes"
	"context"
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
	case "table", "txt", "csv", "json", "jsonl", "bind":
		return true
	}
	return false
}

// Render buffers small writes and reports both encoder and final flush errors.
// DNS exports are encoded one record at a time, without rebuilding the dataset.
func Render(w io.Writer, format string, value any, safe bool) error {
	return RenderContext(context.Background(), w, format, value, safe)
}

func RenderContext(ctx context.Context, w io.Writer, format string, value any, safe bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	buffered := bufio.NewWriterSize(contextWriter{ctx, w}, 32<<10)
	if err := render(contextWriter{ctx, buffered}, format, value, safe); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return buffered.Flush()
}

// Cancellation is checked between writes, without spawning a goroutine that
// could outlive a caller-owned writer blocked in an OS operation.
type contextWriter struct {
	ctx context.Context
	out io.Writer
}

func (w contextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.out.Write(p)
}
func (w contextWriter) WriteString(s string) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return io.WriteString(w.out, s)
}
func render(w io.Writer, format string, value any, safe bool) error {
	if records, zone, ok := recordValues(value); ok {
		return renderRecords(w, format, value, records, zone, safe)
	}
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
		// Zone, profile, dry-run and delete results have their own columns. Using
		// the DNS schema here would silently drop most of their fields.
		columns := columnNames(rows)
		writer := csv.NewWriter(w)
		if err := writer.Write(columns); err != nil {
			return err
		}
		for _, row := range rows {
			values := make([]string, len(columns))
			for i, key := range columns {
				values[i] = cell(row[key])
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
		return genericTable(w, rows)
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
	return []map[string]json.RawMessage{object}, nil
}
func cell(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "null"
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

func columnNames(rows []map[string]json.RawMessage) []string {
	keys := []string{}
	seen := map[string]bool{}
	for _, row := range rows {
		for key := range row {
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

func genericTable(w io.Writer, rows []map[string]json.RawMessage) error {
	keys := columnNames(rows)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	headers := make([]string, len(keys))
	for i, key := range keys {
		headers[i] = escape(strings.ToUpper(key))
	}
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}
	for _, row := range rows {
		values := make([]string, len(keys))
		for i, key := range keys {
			values[i] = escape(cell(row[key]))
		}
		if _, err := fmt.Fprintln(tw, strings.Join(values, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}
