package dns

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeAPI struct {
	zones    []Zone
	records  []Record
	writes   int
	writeErr error
	last     RecordPatch
}

func (f *fakeAPI) ListZones(_ context.Context, name, account string) ([]Zone, error) {
	out := []Zone{}
	for _, z := range f.zones {
		if (name == "" || z.Name == name) && (account == "" || z.AccountID == account) {
			out = append(out, z)
		}
	}
	return out, nil
}
func (f *fakeAPI) GetZone(_ context.Context, id string) (Zone, error) {
	for _, z := range f.zones {
		if z.ID == id {
			return z, nil
		}
	}
	return Zone{}, ErrNotFound
}
func (f *fakeAPI) ListRecords(_ context.Context, _ string, q Query) ([]Record, error) {
	out := []Record{}
	for _, r := range f.records {
		if q.Match(r) {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeAPI) GetRecord(_ context.Context, _, id string) (Record, error) {
	for _, r := range f.records {
		if r.ID == id {
			return r, nil
		}
	}
	return Record{}, ErrNotFound
}
func (f *fakeAPI) CreateRecord(_ context.Context, _ string, p RecordPatch) (Record, error) {
	f.writes++
	f.last = p
	return Record{}, f.writeErr
}
func (f *fakeAPI) UpdateRecord(_ context.Context, _, _ string, p RecordPatch) (Record, error) {
	f.writes++
	f.last = p
	return Record{}, f.writeErr
}
func (f *fakeAPI) DeleteRecord(context.Context, string, string) error { f.writes++; return f.writeErr }
func (f *fakeAPI) CheckDNSRead(context.Context, string) error         { return nil }
func (f *fakeAPI) ExportZone(context.Context, string) (string, error) { return "", nil }
func record(t *testing.T, text string) Record {
	t.Helper()
	r, err := ParseRecord([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestZoneDiscoveryAndAmbiguity(t *testing.T) {
	f := &fakeAPI{zones: []Zone{{ID: "parent", Name: "example.co.uk"}, {ID: "child", Name: "app.example.co.uk"}}}
	s := Service{API: f}
	z, err := s.Zone(context.Background(), "a.app.example.co.uk", "", "")
	if err != nil || z.ID != "child" {
		t.Fatal(z, err)
	}
	f.zones = append(f.zones, Zone{ID: "duplicate", Name: "app.example.co.uk"})
	if _, err = s.Zone(context.Background(), "a.app.example.co.uk", "", ""); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.Zone(context.Background(), "foreign.example.org", "", "parent"); err == nil {
		t.Fatal("foreign name accepted")
	}
}
func TestSelectorNeverChoosesFirstRecord(t *testing.T) {
	f := &fakeAPI{records: []Record{record(t, `{"id":"a","name":"app.example.com","type":"A","content":"192.0.2.1"}`), record(t, `{"id":"b","name":"app.example.com","type":"A","content":"192.0.2.2"}`)}}
	s := Service{API: f}
	selector := Selector{Query: Query{Domain: "app.example.com", Types: []string{"A"}}}
	if _, err := s.Select(context.Background(), Zone{}, selector); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	selector.ID = "a"
	selector.Domain = "other.example.com"
	if _, err := s.Select(context.Background(), Zone{}, selector); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
func TestPatchPreservesMetadataAndDetectsChanges(t *testing.T) {
	original := record(t, `{"id":"a","name":"_sip._tcp.example.com","type":"SRV","ttl":300,"data":{"port":443,"priority":10,"weight":20,"target":"service.example.com"},"future":true}`)
	f := &fakeAPI{records: []Record{original}}
	s := Service{API: f}
	selector := Selector{ID: "a"}
	change, err := s.Prepare(context.Background(), Zone{Name: "example.com"}, "update", selector, RecordPatch{"data": json.RawMessage(`{"weight":0}`)})
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	_ = json.Unmarshal(change.Changes["data"], &d)
	if d["port"] != float64(443) || d["weight"] != float64(0) {
		t.Fatal(d)
	}
	if _, ok := change.Changes["ttl"]; ok {
		t.Fatal("TTL overwritten")
	}
	f.records[0] = record(t, `{"id":"a","name":"_sip._tcp.example.com","type":"SRV","ttl":600}`)
	if _, err = s.Apply(context.Background(), change); !errors.Is(err, ErrConflict) || f.writes != 0 {
		t.Fatal(err, f.writes)
	}
}
func TestLostCreateResponseNeverRepeatsWrite(t *testing.T) {
	f := &fakeAPI{writeErr: errors.New("connection lost")}
	s := Service{API: f}
	_, err := s.Apply(context.Background(), Change{Action: "create", Changes: RecordPatch{"type": json.RawMessage(`"A"`)}})
	if !errors.Is(err, ErrUncertain) || f.writes != 1 {
		t.Fatal(err, f.writes)
	}
}
func TestNamesAndValidation(t *testing.T) {
	for input, want := range map[string]string{"_sip._tcp.Example.com.": "_sip._tcp.example.com", "*.example.com": "*.example.com", "BÜCHER.example": "xn--bcher-kva.example", "@": "example.com", "www": "www.example.com"} {
		got, err := Name(input, "example.com", true)
		if err != nil || got != want {
			t.Fatal(input, got, err)
		}
	}
	for _, input := range []string{"evil/example.com", "a..example.com", "x.*.example.com", "-a.example.com"} {
		if _, err := Name(input, "", true); err == nil {
			t.Fatal(input)
		}
	}
	if Within("notexample.com", "example.com") {
		t.Fatal("label boundary bypass")
	}
	for _, text := range []string{`{"type":"A","content":"::1"}`, `{"id":"read-only"}`, `{"ttl":0}`, `{"proxied":null}`, `{"data":[]}`} {
		patch, err := DecodePatch([]byte(text))
		if err == nil {
			err = ValidatePatch(patch, false, "example.com", "A")
		}
		if err == nil {
			t.Fatal(text)
		}
	}
	if EqualJSON([]byte(`9007199254740993`), []byte(`9007199254740992`)) {
		t.Fatal("large integers collapsed")
	}
}
func FuzzRecordNames(f *testing.F) {
	for _, s := range []string{"example.com", "_acme-challenge.example.com", "*.example.com", "bücher.example", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		name, err := Name(s, "", true)
		if err == nil {
			again, e := Name(name, "", true)
			if e != nil || again != name {
				t.Fatal("normalization not stable")
			}
		}
	})
}
