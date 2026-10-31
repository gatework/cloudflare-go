package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type metadataKey struct{}
type metadata struct {
	status     int
	codes      []int
	requestID  string
	totalPages int
	page       int
	apiError   bool
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
		*m = metadata{}
	}
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (16<<20)+1))
	_ = response.Body.Close()
	if err != nil {
		return nil, errors.New("读取 Cloudflare 响应失败")
	}
	if len(data) > 16<<20 {
		return nil, errors.New("单次 Cloudflare 响应超过 16 MiB")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	var envelope struct {
		Success *bool       `json:"success"`
		Result  resultShape `json:"result"`
		Info    struct {
			TotalPages int `json:"total_pages"`
			Page       int `json:"page"`
		} `json:"result_info"`
		Errors []struct {
			Code int `json:"code"`
		} `json:"errors"`
	}
	decodeErr := json.Unmarshal(data, &envelope)
	if m != nil {
		m.status = response.StatusCode
		m.requestID = response.Header.Get("Cf-Ray")
		m.codes = nil
		m.totalPages, m.page = envelope.Info.TotalPages, envelope.Info.Page
		for _, e := range envelope.Errors {
			m.codes = append(m.codes, e.Code)
		}
		m.apiError = decodeErr == nil && envelope.Success != nil && !*envelope.Success && len(envelope.Errors) > 0
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && !strings.HasSuffix(request.URL.Path, "/export") {
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

// An empty page before the advertised end is incomplete, not a successful dump.
// When pagination metadata is absent, continue until an empty page is observed.
func lastPage(m *metadata, page, count int) (bool, error) {
	if m.page < 0 || m.totalPages < 0 || m.page != 0 && m.page != page || m.totalPages > 0 && page > m.totalPages {
		return false, errors.New("API 分页编号不一致")
	}
	if count == 0 && m.totalPages > page {
		return false, errors.New("API 在最后一页前返回空页，结果不完整")
	}
	return count == 0 || m.totalPages > 0 && page == m.totalPages, nil
}
func callContext(ctx context.Context) (context.Context, *metadata) {
	m := &metadata{}
	return context.WithValue(ctx, metadataKey{}, m), m
}
func failure(ctx context.Context, m *metadata, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var own *Error
	if errors.As(err, &own) {
		return own
	}
	kind := "network/protocol"
	if m.apiError {
		kind = "api"
	}
	return &Error{Status: m.status, Codes: m.codes, RequestID: m.requestID, Kind: kind}
}
