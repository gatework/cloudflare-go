package dns

import (
	"encoding/json"
	"testing"
)

func TestPatchMatchesServerNormalization(t *testing.T) {
	for _, tc := range []struct {
		record, patch string
		match         bool
	}{
		{`{"id":"1","name":"example.com","type":"AAAA","content":"2001:0db8::1"}`, `{"content":"2001:db8::1"}`, true},
		{`{"id":"1","name":"example.com","type":"CNAME","content":"TARGET.EXAMPLE.COM."}`, `{"content":"target.example.com"}`, true},
		{`{"id":"1","name":"example.com","type":"SRV","data":{"target":"TARGET.EXAMPLE.COM.","port":443,"future":true}}`, `{"data":{"target":"target.example.com","port":443}}`, true},
		{`{"id":"1","name":"example.com","type":"SRV","data":{"target":"target.example.com","port":80}}`, `{"data":{"target":"target.example.com","port":443}}`, false},
		{`{"id":"1","name":"example.com","type":"TXT","content":"VALUE"}`, `{"content":"value"}`, false},
		{`{"id":"1","name":"example.com","type":"URI","data":{"target":"https://example.com/A"}}`, `{"data":{"target":"https://example.com/a"}}`, false},
	} {
		r := record(t, tc.record)
		p, err := DecodePatch([]byte(tc.patch))
		if err != nil {
			t.Fatal(err)
		}
		if MatchesPatch(r, p) != tc.match {
			t.Fatalf("record %s patch %s", tc.record, tc.patch)
		}
	}
}

func TestTagsRejectNullElements(t *testing.T) {
	if err := ValidatePatch(RecordPatch{"tags": json.RawMessage(`[null]`)}, false, "example.com", "TXT"); err == nil {
		t.Fatal("null tag accepted")
	}
}
