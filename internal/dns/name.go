package dns

import (
	"errors"
	"strings"

	"golang.org/x/net/idna"
)

func Within(name, zone string) bool { return name == zone || strings.HasSuffix(name, "."+zone) }
func Name(value, zone string, record bool) (string, error) {
	// IDNA maps these Unicode full stops before determining label boundaries.
	value = strings.Map(func(r rune) rune {
		switch r {
		case '\u3002', '\uff0e', '\uff61':
			return '.'
		}
		return r
	}, value)
	value = strings.TrimSuffix(strings.TrimSpace(value), ".")
	if value == "@" {
		value = zone
	}
	if !strings.Contains(value, ".") && zone != "" && value != "" {
		value += "." + zone
	}
	labels := strings.Split(strings.ToLower(value), ".")
	for i, label := range labels {
		if record && label == "*" && i == 0 {
			continue
		}
		if label == "" {
			return "", errors.New("DNS 名称包含空标签")
		}
		ascii := label
		// ASCII record owners may contain underscores anywhere. Hostnames and
		// Zone names retain strict IDNA rules; Unicode owner labels retain the
		// conventional leading underscore prefix before IDNA conversion.
		if !record || !strings.Contains(label, "_") || !asciiOwner(label) {
			prefix := ""
			if record {
				prefix = label[:len(label)-len(strings.TrimLeft(label, "_"))]
				label = strings.TrimLeft(label, "_")
			}
			var err error
			ascii, err = idna.Lookup.ToASCII(label)
			if err != nil {
				return "", errors.New("DNS 名称无效")
			}
			ascii = prefix + ascii
		}
		label = strings.ToLower(ascii)
		if len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return "", errors.New("DNS 标签长度或连字符无效")
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || record && r == '_') {
				return "", errors.New("DNS 标签含非法字符")
			}
		}
		labels[i] = label
	}
	name := strings.Join(labels, ".")
	if len(labels) < 2 || len(name) > 253 {
		return "", errors.New("需要完整 DNS 名称")
	}
	return name, nil
}

func asciiOwner(label string) bool {
	for _, r := range label {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
