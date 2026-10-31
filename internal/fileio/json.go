package fileio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// JSONObject rejects duplicate keys at every depth and preserves number precision.
func JSONObject(data []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("JSON 必须使用 UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				seen := map[string]bool{}
				for decoder.More() {
					k, err := decoder.Token()
					if err != nil {
						return err
					}
					name := strings.ToLower(k.(string))
					if seen[name] {
						return errors.New("JSON 包含重复字段")
					}
					seen[name] = true
					if err = scan(); err != nil {
						return err
					}
				}
			case '[':
				for decoder.More() {
					if err := scan(); err != nil {
						return err
					}
				}
			default:
				return errors.New("JSON 分隔符无效")
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := scan(); err != nil {
		return nil, errors.New("JSON 格式错误或存在重复字段")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("需要单个 JSON 对象")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return nil, errors.New("需要 JSON 对象")
	}
	return object, nil
}
