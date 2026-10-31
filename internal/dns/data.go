package dns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode"
)

// Field meanings belong to record types: e.g. CAA and DNSKEY flags are
// different-width integers, while NAPTR flags are text.
func validateData(p RecordPatch, create bool, typ string) error {
	raw, ok := p["data"]
	if !ok {
		return nil
	}
	switch typ {
	case "A", "AAAA", "CNAME", "TXT", "MX", "NS", "PTR", "OPENPGPKEY":
		return errors.New("此类型不接受结构化 data，请使用 content/priority")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("data 需要对象")
	}
	var required []string
	switch typ {
	case "CAA":
		required = []string{"flags", "tag", "value"}
	case "SRV":
		required = []string{"priority", "weight", "port", "target"}
	case "HTTPS", "SVCB":
		required = []string{"priority", "target", "value"}
	case "URI":
		required = []string{"weight", "target"}
	}
	if create {
		for _, key := range required {
			if _, ok := fields[key]; !ok {
				return fmt.Errorf("data.%s 为必填项", key)
			}
		}
	}
	for key, value := range fields {
		if len(required) > 0 && !slices.Contains(required, key) {
			return fmt.Errorf("%s 不支持 data.%s", typ, key)
		}
		maxValue := -1
		isText, hostname := false, false
		switch key {
		case "priority", "weight", "port":
			maxValue = 65535
		case "flags":
			switch typ {
			case "CAA":
				maxValue = 255
			case "DNSKEY":
				maxValue = 65535
			case "NAPTR":
				isText = true
			}
		case "algorithm", "protocol":
			if typ == "DNSKEY" {
				maxValue = 255
			}
		case "order", "preference":
			if typ == "NAPTR" {
				maxValue = 65535
			}
		case "target":
			isText = typ == "SRV" || typ == "HTTPS" || typ == "SVCB" || typ == "URI"
			hostname = isText && typ != "URI"
		case "tag", "value":
			isText = typ == "CAA" || typ == "HTTPS" || typ == "SVCB"
		case "public_key":
			isText = typ == "DNSKEY"
		case "regex", "replacement", "service":
			isText = typ == "NAPTR"
		}
		if maxValue >= 0 {
			var n int
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &n) != nil || n < 0 || n > maxValue {
				return fmt.Errorf("%s data.%s 需要 0–%d 的整数", typ, key, maxValue)
			}
		}
		if !isText {
			continue
		}
		var text string
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &text) != nil {
			return fmt.Errorf("data.%s 需要字符串", key)
		}
		if hostname && text != "." {
			normalized, err := Name(text, "", false)
			if err != nil {
				return err
			}
			fields[key] = json.RawMessage(quote(normalized))
		}
		if typ == "URI" && key == "target" {
			target, err := url.Parse(text)
			if err != nil || target.Scheme == "" || strings.IndexFunc(text, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
				return errors.New("URI data.target 需要包含 scheme 的有效 URI")
			}
		}
		if typ == "CAA" && key == "tag" && text == "" {
			return errors.New("CAA tag 不能为空")
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	p["data"] = encoded
	return nil
}
