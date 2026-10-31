package dns

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
)

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }
func EqualJSON(a, b []byte) bool {
	if bytes.Equal(a, b) {
		return json.Valid(a)
	}
	var x, y any
	da, db := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	da.UseNumber()
	db.UseNumber()
	if da.Decode(&x) != nil || db.Decode(&y) != nil {
		return false
	}
	if da.Decode(new(any)) != io.EOF || db.Decode(new(any)) != io.EOF {
		return false
	}
	return reflect.DeepEqual(x, y)
}
func MatchesPatch(r Record, patch RecordPatch) bool {
	var data map[string]json.RawMessage
	_ = json.Unmarshal(r.Raw, &data)
	for k, v := range patch {
		if !EqualJSON(v, data[k]) {
			return false
		}
	}
	return true
}
