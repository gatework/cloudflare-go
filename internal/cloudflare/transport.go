package cloudflare

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

type metadataKey struct{}
type metadata struct {
	status    int
	codes     []int
	requestID string
	info      pageInfo
	apiError  bool
	attempted bool
	notSent   bool
}
type transport struct{ base http.RoundTripper }

// Only the envelope's result shape is needed here. Retaining another copy of
// every record duplicates a full page before the SDK even starts decoding it.
type resultShape byte

func (s *resultShape) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return errors.New("API result 为空")
	}
	*s = resultShape(data[0])
	return nil
}

func (t transport) CloseIdleConnections() {
	if closer, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (t transport) RoundTrip(request *http.Request) (*http.Response, error) {
	m, _ := request.Context().Value(metadataKey{}).(*metadata)
	if m != nil {
		*m = metadata{attempted: true}
	}
	budget, _ := request.Context().Value(budgetKey{}).(*responseBudget)
	if budget != nil && !budget.available() {
		if m != nil {
			m.notSent = true
		}
		budget.cancel(ErrResponseBudget)
		return nil, ErrResponseBudget
	}
	if err := request.Context().Err(); err != nil {
		if m != nil {
			m.notSent = true
		}
		return nil, err
	}
	// Require the standard connection path, not merely a missing GotConn event:
	// registered protocols may bypass it, and custom TLS dialers can omit traces.
	base, standard := t.base.(*http.Transport)
	//lint:ignore SA1019 The legacy TLS hook can bypass traces too; inspect it without invoking it.
	traceable := standard && base.DialTLS == nil && base.DialTLSContext == nil
	var acquiring, connected atomic.Bool
	if traceable {
		trace := &httptrace.ClientTrace{
			GetConn: func(string) { acquiring.Store(true) },
			GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
			TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
				// A negotiated alternate protocol need not emit GotConn.
				if err == nil {
					connected.Store(true)
				}
			},
		}
		request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		if m != nil {
			m.notSent = traceable && acquiring.Load() && !connected.Load() && response == nil
		}
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	if response == nil || response.Body == nil {
		return nil, protocolError(m)
	}
	if m != nil {
		m.status = response.StatusCode
		m.requestID = response.Header.Get("Cf-Ray")
	}
	var reader io.Reader = response.Body
	if budget != nil {
		reader = budgetReader{reader, budget}
	}
	data, err := io.ReadAll(io.LimitReader(reader, (16<<20)+1))
	_ = response.Body.Close()
	if err != nil {
		if errors.Is(err, ErrResponseBudget) {
			return nil, err
		}
		return nil, errors.New("读取 Cloudflare 响应失败")
	}
	if len(data) > 16<<20 {
		return nil, errors.New("单次 Cloudflare 响应超过 16 MiB")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	if !utf8.Valid(data) {
		return nil, protocolError(m)
	}
	var envelope struct {
		Success *bool       `json:"success"`
		Result  resultShape `json:"result"`
		Info    pageInfo    `json:"result_info"`
		Errors  []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	decodeErr := json.Unmarshal(data, &envelope)
	if m != nil {
		m.info = envelope.Info
		for _, e := range envelope.Errors {
			m.codes = append(m.codes, e.Code)
		}
		m.apiError = decodeErr == nil && envelope.Success != nil && !*envelope.Success && len(envelope.Errors) > 0
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 && response.StatusCode < 400 {
		return nil, protocolError(m)
	}
	if strings.HasSuffix(request.URL.Path, "/export") && response.StatusCode < 400 {
		media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
		trimmed := bytes.TrimSpace(data)
		// This is an opaque BIND document, not a JSON envelope or proxy page.
		if err != nil || media != "text/plain" || response.StatusCode != http.StatusOK || len(trimmed) == 0 || bytes.IndexByte(data, 0) >= 0 || trimmed[0] == '<' || json.Valid(trimmed) {
			return nil, protocolError(m)
		}
		return response, nil
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		if decodeErr != nil || envelope.Success == nil || !*envelope.Success {
			_ = response.Body.Close()
			return nil, protocolError(m)
		}
		want := byte('{')
		if request.Method == http.MethodGet && (strings.HasSuffix(request.URL.Path, "/zones") || strings.HasSuffix(request.URL.Path, "/dns_records")) {
			want = '['
		}
		if byte(envelope.Result) != want {
			return nil, protocolError(m)
		}
	}
	return response, nil
}

func protocolError(m *metadata) error {
	if m == nil {
		return &Error{Kind: "protocol"}
	}
	return &Error{Status: m.status, Codes: m.codes, RequestID: m.requestID, Kind: "protocol"}
}

func callContext(ctx context.Context) (context.Context, *metadata, context.CancelFunc) {
	stop := func() {}
	if ctx.Value(budgetKey{}) == nil {
		ctx, stop = WithResponseBudget(ctx, DefaultMaxBytes)
	}
	m := &metadata{}
	return context.WithValue(ctx, metadataKey{}, m), m, stop
}
func failure(ctx context.Context, m *metadata, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(context.Cause(ctx), ErrResponseBudget) {
		return &Error{Status: m.status, Codes: m.codes, RequestID: m.requestID, Kind: "budget", notSent: !m.attempted || m.notSent, cause: ErrResponseBudget}
	}
	var own *Error
	if errors.As(err, &own) {
		return own
	}
	kind := "network/protocol"
	if m.apiError {
		kind = "api"
	}
	classified := &Error{Status: m.status, Codes: m.codes, RequestID: m.requestID, Kind: kind, notSent: !m.attempted || m.notSent, cause: ctx.Err()}
	if ctx.Err() != nil && !classified.Definitive() {
		return ctx.Err()
	}
	return classified
}
