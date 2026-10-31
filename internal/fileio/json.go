package fileio

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// JSONObject rejects duplicate keys at every depth and preserves number precision.
func JSONObject(data []byte) (map[string]json.RawMessage, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("JSON 必须使用 UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	const maxDepth = 128
	var scan func(int) error
	scan = func(depth int) error {
		if depth > maxDepth {
			return errors.New("JSON 嵌套超过 128 层")
		}
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
					name := k.(string)
					if seen[name] {
						return errors.New("JSON 包含重复字段")
					}
					seen[name] = true
					if err = scan(depth + 1); err != nil {
						return err
					}
				}
			case '[':
				for decoder.More() {
					if err := scan(depth + 1); err != nil {
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
	if err := scan(0); err != nil {
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
