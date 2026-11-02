package cloudflare

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type responseFixture []byte

func (body responseFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), Header: http.Header{"Content-Type": []string{"application/json"}}, Request: request}, nil
}

func BenchmarkResponseValidation(b *testing.B) {
	var body strings.Builder
	body.WriteString(`{"success":true,"result":[`)
	for i := range 100 {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":"%032x","name":"r%d.example.com","type":"TXT","content":"%s","future":{"counter":9007199254740993}}`, i, i, strings.Repeat("x", 1024))
	}
	body.WriteString(`],"result_info":{"page":1,"total_pages":10}}`)
	base := transport{base: responseFixture(body.String())}
	request, err := http.NewRequestWithContext(b.Context(), http.MethodGet, BaseURL+"zones/zone/dns_records", nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(body.Len()))
	for b.Loop() {
		ctx, stop := WithResponseBudget(b.Context(), DefaultMaxBytes)
		ctx = context.WithValue(ctx, metadataKey{}, &metadata{})
		response, err := base.RoundTrip(request.Clone(ctx))
		stop()
		if err != nil {
			b.Fatal(err)
		}
		response.Body.Close()
	}
}
