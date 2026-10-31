package dns

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPrepareOwnsPatch(t *testing.T) {
	f := &fakeAPI{}
	s := Service{API: f}
	patch := RecordPatch{"type": json.RawMessage(`"a"`), "content": json.RawMessage(`"192.0.2.1"`)}
	change, err := s.Prepare(t.Context(), Zone{Name: "example.com"}, "create", Selector{Query: Query{Domain: "app.example.com"}}, patch)
	if err != nil {
		t.Fatal(err)
	}
	if len(patch) != 2 || string(patch["type"]) != `"a"` {
		t.Fatal("validation mutated caller patch", patch)
	}
	patch["content"][1] = '9'
	if string(change.Changes["content"]) != `"192.0.2.1"` {
		t.Fatal("prepared change aliases input")
	}
}

func TestCancellationBeforeWriteDoesNotReportUncertain(t *testing.T) {
	f := &fakeAPI{}
	s := Service{API: f}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Apply(ctx, Change{Action: "create"})
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUncertain) || f.writes != 0 {
		t.Fatal(err, f.writes)
	}
}

func TestNullStructuredValuesAreInvalid(t *testing.T) {
	for _, data := range []string{`{"priority":null}`, `{"weight":null}`, `{"port":null}`, `{"target":null}`} {
		if err := ValidatePatch(RecordPatch{"data": json.RawMessage(data)}, false, "example.com", "SRV"); err == nil {
			t.Fatal(data)
		}
	}
	for _, pair := range [][2]string{{`{} trailing`, `{}`}, {`null null`, `null`}, {`garbage`, `garbage`}} {
		if EqualJSON([]byte(pair[0]), []byte(pair[1])) {
			t.Fatal("invalid JSON accepted", pair)
		}
	}
	if !EqualJSON([]byte(`{"b":1,"a":[false,null]}`), []byte(`{"a":[false,null],"b":1}`)) {
		t.Fatal("object key order affected equality")
	}
}
