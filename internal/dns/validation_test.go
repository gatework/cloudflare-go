package dns

import (
	"encoding/json"
	"testing"
)

func TestAdvancedRecordFieldSemantics(t *testing.T) {
	for _, input := range []string{
		`{"name":"example.com","type":"DNSKEY","data":{"flags":257,"protocol":3,"algorithm":13,"public_key":"AQID"}}`,
		`{"name":"example.com","type":"NAPTR","data":{"flags":"S","order":10,"preference":20,"regex":"","replacement":"_sip._tcp.example.com","service":"SIP+D2T"}}`,
		`{"name":"_https._tcp.example.com","type":"URI","priority":10,"data":{"weight":20,"target":"https://example.com/Case?query=Value"}}`,
	} {
		patch, err := DecodePatch([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidatePatch(patch, true, "example.com", ""); err != nil {
			t.Errorf("valid advanced record rejected: %s: %v", input, err)
		}
	}
}

func TestTypedDataRejectsWrongFields(t *testing.T) {
	for _, input := range []string{
		`{"type":"SRV","data":{"priority":1,"weight":1,"port":443,"target":"example.com","value":"ignored"}}`,
		`{"type":"TXT","priority":10,"content":"text"}`,
		`{"type":"CAA","data":{"flags":256,"tag":"issue","value":"example.com"}}`,
		`{"type":"DNSKEY","data":{"flags":"257","algorithm":13,"protocol":3,"public_key":"AQID"}}`,
		`{"type":"NAPTR","data":{"flags":1}}`,
	} {
		patch, err := DecodePatch([]byte(input))
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidatePatch(patch, false, "example.com", ""); err == nil {
			t.Errorf("invalid record accepted: %s", input)
		}
	}
}

func TestNamesSupportOwnerUnderscoresAndUnicodeDots(t *testing.T) {
	for input, want := range map[string]string{"a_b.example.com": "a_b.example.com", "_.example.com": "_.example.com", "BÜCHER。example．": "xn--bcher-kva.example"} {
		got, err := Name(input, "", true)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := Name("a_b.example.com", "", false); err == nil {
		t.Fatal("hostname accepted underscore")
	}
}

func TestIPv6PatchNormalization(t *testing.T) {
	patch := RecordPatch{"content": json.RawMessage(`"2001:0DB8:0000:0000:0000:0000:0000:0001"`)}
	if err := ValidatePatch(patch, false, "example.com", "AAAA"); err != nil {
		t.Fatal(err)
	}
	if string(patch["content"]) != `"2001:db8::1"` {
		t.Fatal("IPv6 content not normalized", string(patch["content"]))
	}
}

func TestUpdatePreservesFutureDataFields(t *testing.T) {
	before := record(t, `{"id":"r","name":"_sip._tcp.example.com","type":"SRV","data":{"priority":1,"weight":2,"port":443,"target":"example.com","future":{"value":9007199254740993}}}`)
	service := Service{API: &fakeAPI{records: []Record{before}}}
	change, err := service.Prepare(t.Context(), Zone{Name: "example.com"}, "update", Selector{ID: "r"}, RecordPatch{"data": json.RawMessage(`{"weight":3}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"priority":1,"weight":3,"port":443,"target":"example.com","future":{"value":9007199254740993}}`)
	if !EqualJSON(change.Changes["data"], want) {
		t.Fatal("future field lost", string(change.Changes["data"]))
	}
}

func FuzzRecordPatch(f *testing.F) {
	for _, input := range []string{
		`{"type":"A","content":"192.0.2.1"}`, `{"type":"AAAA","content":"2001:0db8::1"}`,
		`{"type":"DNSKEY","data":{"flags":257}}`, `{"type":"NAPTR","data":{"flags":"S"}}`,
		`{"type":"URI","priority":1,"data":{"weight":1,"target":"https://example.com/Case"}}`,
		`{"type":"SRV","data":{"weight":null}}`, `{"type":"TXT","content":""}`,
	} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		patch, err := DecodePatch([]byte(input))
		if err != nil || ValidatePatch(patch, false, "example.com", "TXT") != nil {
			return
		}
		before, err := json.Marshal(patch)
		if err != nil {
			t.Fatal(err)
		}
		if err = ValidatePatch(patch, false, "example.com", "TXT"); err != nil {
			t.Fatal("validation not stable", err)
		}
		after, err := json.Marshal(patch)
		if err != nil || !EqualJSON(before, after) {
			t.Fatal("normalization not stable", string(before), string(after), err)
		}
	})
}
