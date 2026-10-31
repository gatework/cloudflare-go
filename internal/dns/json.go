package dns

import (
	"bytes"
	"encoding/json"
	"io"
	"net/netip"
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
	if json.Unmarshal(r.Raw, &data) != nil || data == nil {
		return false
	}
	for k, v := range patch {
		if !matchesField(r.Type, k, v, data[k]) {
			return false
		}
	}
	return true
}

func matchesField(typ, field string, desired, actual json.RawMessage) bool {
	if EqualJSON(desired, actual) {
		return true
	}
	if field == "data" || field == "settings" {
		var want, got map[string]json.RawMessage
		if json.Unmarshal(desired, &want) != nil || json.Unmarshal(actual, &got) != nil || want == nil || got == nil {
			return false
		}
		for key, value := range want {
			if !matchesField(typ, field+"."+key, value, got[key]) {
				return false
			}
		}
		return true
	}
	var want, got string
	if json.Unmarshal(desired, &want) != nil || json.Unmarshal(actual, &got) != nil {
		return false
	}
	if field == "content" && (typ == "A" || typ == "AAAA") {
		a, errA := netip.ParseAddr(want)
		b, errB := netip.ParseAddr(got)
		return errA == nil && errB == nil && a == b
	}
	hostname := field == "content" && (typ == "CNAME" || typ == "MX" || typ == "NS" || typ == "PTR") || field == "data.target" && (typ == "SRV" || typ == "HTTPS" || typ == "SVCB")
	if field == "name" || hostname {
		a, errA := Name(want, "", field == "name")
		b, errB := Name(got, "", field == "name")
		return errA == nil && errB == nil && a == b
	}
	return false
}
