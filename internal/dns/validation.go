package dns

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"github.com/gatework/cloudflare-go/internal/fileio"
)

func Type(t string) (string, error) {
	t = strings.ToUpper(t)
	switch t {
	case "A", "AAAA", "CNAME", "TXT", "MX", "NS", "PTR", "CAA", "SRV", "HTTPS", "SVCB", "CERT", "DNSKEY", "DS", "LOC", "NAPTR", "SMIMEA", "SSHFP", "TLSA", "URI", "OPENPGPKEY":
		return t, nil
	default:
		return "", errors.New("不支持的 DNS 记录类型")
	}
}
func DecodePatch(data []byte) (RecordPatch, error) {
	object, err := fileio.JSONObject(data)
	if err != nil {
		return nil, err
	}
	return RecordPatch(object), nil
}
func ValidatePatch(p RecordPatch, create bool, zone, existingType string) error {
	if len(p) == 0 {
		return errors.New("没有需要修改的字段")
	}
	allowed := map[string]bool{}
	for _, key := range strings.Fields("name type content ttl proxied priority comment tags data settings private_routing") {
		allowed[key] = true
	}
	for k, v := range p {
		if !allowed[k] || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fmt.Errorf("不支持、只读或空值字段：%s", k)
		}
	}
	getString := func(key string) (string, error) {
		var s string
		if json.Unmarshal(p[key], &s) != nil {
			return "", fmt.Errorf("%s 需要字符串", key)
		}
		return s, nil
	}
	typ := existingType
	if _, ok := p["type"]; ok {
		value, err := getString("type")
		if err != nil {
			return err
		}
		typ, err = Type(value)
		if err != nil {
			return err
		}
		p["type"] = json.RawMessage(quote(typ))
	}
	if create && typ == "" {
		return errors.New("创建记录需要 --type")
	}
	if _, ok := p["name"]; ok {
		value, err := getString("name")
		if err != nil {
			return err
		}
		name, err := Name(value, zone, true)
		if err != nil {
			return err
		}
		if !Within(name, zone) {
			return errors.New("记录名称不属于所选 Zone")
		}
		p["name"] = json.RawMessage(quote(name))
	}
	if raw, ok := p["content"]; ok {
		var content string
		if json.Unmarshal(raw, &content) != nil {
			return errors.New("content 需要字符串")
		}
		if typ == "A" || typ == "AAAA" {
			ip, err := netip.ParseAddr(content)
			if err != nil || typ == "A" && !ip.Is4() || typ == "AAAA" && !ip.Is6() || ip.Zone() != "" {
				return errors.New("IP 地址与记录类型不匹配")
			}
			p["content"] = json.RawMessage(quote(ip.String()))
		}
		if typ == "CNAME" || typ == "MX" || typ == "NS" || typ == "PTR" {
			if content != "." {
				name, err := Name(content, "", false)
				if err != nil {
					return err
				}
				p["content"] = json.RawMessage(quote(name))
			}
		}
	}
	for _, key := range []string{"ttl", "priority"} {
		if raw, ok := p[key]; ok {
			if key == "priority" && typ != "MX" && typ != "URI" {
				return errors.New("顶层 priority 仅用于 MX/URI，其他类型使用 data.priority")
			}
			n, err := strconv.ParseInt(string(raw), 10, 64)
			if err != nil || key == "ttl" && n != 1 && (n < 30 || n > 86400) || key == "priority" && (n < 0 || n > 65535) {
				return fmt.Errorf("%s 数值无效；TTL 使用 auto 或 30–86400 秒（套餐限制由 API 校验）", key)
			}
		}
	}
	for _, key := range []string{"proxied", "private_routing"} {
		if raw, ok := p[key]; ok {
			var b bool
			if json.Unmarshal(raw, &b) != nil {
				return fmt.Errorf("%s 需要布尔值", key)
			}
			if key == "proxied" && b && typ != "A" && typ != "AAAA" && typ != "CNAME" {
				return errors.New("此记录类型不支持代理")
			}
		}
	}
	if raw, ok := p["comment"]; ok {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return errors.New("comment 需要字符串")
		}
	}
	if raw, ok := p["tags"]; ok {
		var tags []string
		if json.Unmarshal(raw, &tags) != nil || tags == nil {
			return errors.New("tags 需要字符串数组")
		}
		for _, tag := range tags {
			if strings.IndexFunc(tag, unicode.IsControl) >= 0 {
				return errors.New("标签包含控制字符")
			}
		}
	}
	for _, key := range []string{"data", "settings"} {
		if raw, ok := p[key]; ok {
			if _, err := fileio.JSONObject(raw); err != nil {
				return fmt.Errorf("%s 需要 JSON 对象", key)
			}
		}
	}
	if create {
		switch typ {
		case "A", "AAAA", "CNAME", "TXT", "MX", "NS", "PTR", "OPENPGPKEY":
			if _, ok := p["content"]; !ok {
				return errors.New("此类型需要 content")
			}
		default:
			if _, ok := p["data"]; !ok {
				return errors.New("此类型需要结构化 data；可使用类型参数或 --file")
			}
		}
		if typ == "MX" || typ == "URI" {
			if _, ok := p["priority"]; !ok {
				return fmt.Errorf("%s 需要 priority", typ)
			}
		}
		if _, ok := p["ttl"]; !ok {
			p["ttl"] = json.RawMessage("1")
		}
	}
	return validateData(p, create, typ)
}
