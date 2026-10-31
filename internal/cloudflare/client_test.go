package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gatework/cloudflare-go/internal/dns"
)

const zoneID = "11111111111111111111111111111111"
const recordID = "22222222222222222222222222222222"

func envelope(record string) string { return `{"success":true,"result":` + record + `}` }
func TestOfficialSDKCRUDAndRawFields(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer private-token" {
			t.Error("missing bearer credential")
		}
		if r.Header.Get("X-Auth-Key") != "" {
			t.Error("legacy authentication leaked")
		}
		methods = append(methods, r.Method)
		if r.Method == "POST" || r.Method == "PATCH" {
			data, _ := io.ReadAll(r.Body)
			var patch map[string]json.RawMessage
			if json.Unmarshal(data, &patch) != nil {
				t.Errorf("invalid payload %s", data)
			}
			if r.Method == "PATCH" {
				if _, ok := patch["ttl"]; ok {
					t.Error("unspecified TTL sent")
				}
				if string(patch["proxied"]) != "false" {
					t.Error("explicit false omitted")
				}
			}
		}
		if r.Method == "DELETE" {
			fmt.Fprint(w, envelope(`{"id":"`+recordID+`"}`))
			return
		}
		fmt.Fprint(w, envelope(`{"id":"`+recordID+`","type":"A","name":"app.example.com","content":"192.0.2.1","ttl":1,"future":{"counter":9007199254740993},"comment":null}`))
	}))
	defer server.Close()
	t.Setenv("CLOUDFLARE_API_KEY", "legacy-secret")
	client := New("private-token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	ctx := context.Background()
	record, err := client.CreateRecord(ctx, zoneID, dns.RecordPatch{"type": json.RawMessage(`"A"`), "name": json.RawMessage(`"app.example.com"`), "content": json.RawMessage(`"192.0.2.1"`)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(record)
	if !strings.Contains(string(encoded), "9007199254740993") || !strings.Contains(string(encoded), `"comment":null`) {
		t.Fatal("raw response lost", string(encoded))
	}
	if _, err = client.UpdateRecord(ctx, zoneID, recordID, dns.RecordPatch{"proxied": json.RawMessage(`false`)}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.GetRecord(ctx, zoneID, recordID); err != nil {
		t.Fatal(err)
	}
	if err = client.DeleteRecord(ctx, zoneID, recordID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST,PATCH,GET,DELETE" {
		t.Fatal(methods)
	}
}
func TestPaginationBoundaryAndDedup(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls.Add(1)
		page := r.URL.Query().Get("page")
		records := `{"id":"1","type":"TXT","name":"example.com","content":"first","ttl":1}`
		if page == "2" {
			records += `,{"id":"2","type":"TXT","name":"a.example.com","content":"child","ttl":1},{"id":"3","type":"TXT","name":"notexample.com","content":"outside","ttl":1}`
		}
		fmt.Fprintf(w, `{"success":true,"result":[%s],"result_info":{"page":%s,"per_page":100,"total_pages":2,"total_count":3}}`, records, map[bool]string{true: "2", false: "1"}[page == "2"])
	}))
	defer server.Close()
	client := New("token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	records, err := client.ListRecords(context.Background(), zoneID, dns.Query{Domain: "example.com"}, 1<<20)
	if err != nil || len(records) != 2 || calls.Load() != 2 {
		t.Fatal(len(records), calls.Load(), err)
	}
	if _, err = client.ListRecords(context.Background(), zoneID, dns.Query{}, 10); err == nil {
		t.Fatal("size limit ignored")
	}
}
func TestRetryPolicyAndErrorRedaction(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.Header().Set("Cf-Ray", "trace-id")
		w.WriteHeader(503)
		fmt.Fprint(w, `{"success":false,"errors":[{"code":1015,"message":"private-token"}]}`)
	}))
	defer server.Close()
	client := New("private-token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	_, err := client.GetRecord(context.Background(), zoneID, recordID)
	var apiErr *Error
	if !errors.As(err, &apiErr) || calls.Load() != 3 || apiErr.Status != 503 || strings.Contains(err.Error(), "private-token") {
		t.Fatal(calls.Load(), err)
	}
	calls.Store(0)
	_, err = client.CreateRecord(context.Background(), zoneID, dns.RecordPatch{"type": json.RawMessage(`"A"`)})
	if err == nil || calls.Load() != 1 {
		t.Fatal("write retried", calls.Load(), err)
	}
}
func TestProtocolCancellationAndBind(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/export") {
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "example.com. 1 IN A 192.0.2.1\n")
			return
		}
		fmt.Fprint(w, `{"success":true,"result":null}`)
	}))
	defer server.Close()
	client := New("token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.GetRecord(ctx, zoneID, recordID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := client.CreateRecord(context.Background(), zoneID, nil); err == nil {
		t.Fatal("null result accepted")
	}
	text, err := client.ExportZone(context.Background(), zoneID)
	if err != nil || !strings.HasPrefix(text, "example.com.") {
		t.Fatal(text, err)
	}
}

func TestPaginationPastOneHundredPagesAndUnknownTypes(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := calls.Add(1)
		fmt.Fprintf(w, `{"success":true,"result":[{"id":"%d","name":"example.com","type":"FUTURE_TYPE","new_field":null}],"result_info":{"page":%d,"total_pages":101}}`, page, page)
	}))
	defer server.Close()
	client := New("token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	records, err := client.ListRecords(context.Background(), zoneID, dns.Query{Domain: "example.com"}, 1<<20)
	if err != nil || len(records) != 101 || calls.Load() != 101 {
		t.Fatal(len(records), calls.Load(), err)
	}
	if !strings.Contains(string(records[0].Raw), `"new_field":null`) {
		t.Fatal("unknown type fields were lost")
	}
}

func TestSecondPageUsesOriginalCancellation(t *testing.T) {
	second := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			close(second)
			<-r.Context().Done()
			return
		}
		fmt.Fprint(w, `{"success":true,"result":[{"id":"1","name":"example.com","type":"TXT"}],"result_info":{"page":1,"total_pages":2}}`)
	}))
	defer server.Close()
	client := New("token", server.URL+"/", server.Client().Transport)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.ListRecords(ctx, zoneID, dns.Query{}, 1<<20); done <- err }()
	<-second
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("later page escaped cancellation", err)
	}
}
