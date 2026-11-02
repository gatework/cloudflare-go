package cloudflare

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

func TestFilteredSnapshotPreservesJSONSemantics(t *testing.T) {
	s, err := snapshot([]byte(`{"future":{"n":9007199254740993},"text":"\u0061"}`), false)
	if err != nil || s.raw != nil {
		t.Fatal("filtered raw body retained", err)
	}
	for _, tc := range []struct {
		raw   string
		equal bool
	}{
		{`{"text":"a","future":{"n":9007199254740993}}`, true},
		{`{"text":"a","future":{"n":9007199254740992}}`, false},
		{`{"text":"a","future":{"n":9007199254740993},"added":true}`, false},
	} {
		equal, err := s.matches([]byte(tc.raw))
		if err != nil || equal != tc.equal {
			t.Fatal(tc.raw, equal, err)
		}
	}
}

func TestFilteredDuplicateStillDetectsConcurrentChange(t *testing.T) {
	for _, changed := range []bool{false, true} {
		base := responseFunc(func(r *http.Request) (*http.Response, error) {
			page := r.URL.Query().Get("page")
			n := "9007199254740993"
			if page == "2" && changed {
				n = "9007199254740992"
			}
			record := fmt.Sprintf(`{"id":"1","name":"example.com","type":"TXT","future":{"n":%s}}`, n)
			if page == "2" {
				record = fmt.Sprintf(`{"future":{"n":%s},"type":"TXT","name":"example.com","id":"1"}`, n)
			}
			body := fmt.Sprintf(`{"success":true,"result":[%s],"result_info":{"page":%s,"total_pages":2,"total_count":1}}`, record, page)
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})
		c := New("fixture", "", base)
		result, err := c.ListRecords(t.Context(), zoneID, dns.Query{Types: []string{"A", "AAAA"}})
		c.Close()
		if changed && !errors.Is(err, dns.ErrConflict) || !changed && (err != nil || len(result) != 0) {
			t.Fatal(changed, result, err)
		}
	}
}

func TestTagAndCommentFiltersUseAPIAndLocalChecks(t *testing.T) {
	base := responseFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if q.Get("tag.exact") != "owner:Ops" || q.Get("comment.exact") != "Review" || q.Get("tag_match") != "all" {
			t.Error(q)
		}
		body := `{"success":true,"result":[
		{"id":"1","name":"example.com","type":"A","tags":["owner:Ops","env:prod"],"comment":"Review"},
		{"id":"2","name":"example.com","type":"A","tags":["owner:Ops"],"comment":"Review"},
		{"id":"3","name":"example.com","type":"A","tags":["owner:Ops","env:prod"],"comment":"review"}],"result_info":{"page":1,"total_pages":1,"total_count":3}}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})
	c := New("fixture", "", base)
	defer c.Close()
	comment := "Review"
	result, err := c.ListRecords(t.Context(), zoneID, dns.Query{Tags: []string{"owner:Ops", "env:prod"}, Comment: &comment})
	if err != nil || len(result) != 1 || result[0].ID != "1" {
		t.Fatal(result, err)
	}
}

func TestZoneListHonorsSizeBudget(t *testing.T) {
	base := responseFixture(`{"success":true,"result":[{"id":"1","name":"example.com"}],"result_info":{"page":1,"total_pages":1}}`)
	c := New("fixture", "", base)
	defer c.Close()
	limited, stop := WithResponseBudget(t.Context(), 10)
	defer stop()
	if _, err := c.ListZones(limited, "", ""); err == nil {
		t.Fatal("zone budget ignored")
	}
}
