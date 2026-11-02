// Package cloudflare adapts the official SDK without leaking generated types.
package cloudflare

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	sdkdns "github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/zones"
	"github.com/gatework/cloudflare-go/internal/dns"
)

const BaseURL = "https://api.cloudflare.com/client/v4/"

type Error struct {
	Status    int
	Codes     []int
	RequestID string
	Kind      string
	notSent   bool
	cause     error
}

func (e *Error) Error() string {
	hint := "Cloudflare 请求失败"
	if errors.Is(e.cause, ErrResponseBudget) {
		hint = ErrResponseBudget.Error()
	}
	if e.notSent {
		if errors.Is(e.cause, ErrResponseBudget) {
			hint = "Cloudflare 请求未发送：" + hint
		} else {
			hint = "Cloudflare 请求未发送"
		}
	}
	if e.Status == 401 || e.Status == 403 {
		hint = "认证或权限不足，请检查 Token 的 Zone Read / DNS Read / DNS Write 权限"
	}
	return fmt.Sprintf("%s（%s，HTTP %d，code %v，request %q）", hint, e.Kind, e.Status, e.Codes, e.RequestID)
}
func (e *Error) Definitive() bool {
	return e.notSent || e.Kind == "api" && e.Status >= 400 && e.Status < 500 && e.Status != 408
}
func (e *Error) Unwrap() error { return e.cause }
func (e *Error) Is(target error) bool {
	// A proxy/router 404 does not prove the selected DNS record was deleted.
	return target == dns.ErrNotFound && e.Kind == "api" && e.Status == 404 && slices.Contains(e.Codes, 81044)
}

type Client struct {
	records *sdkdns.RecordService
	zones   *zones.ZoneService
	http    *http.Client
	owned   bool
}

// New owns the default transport clone, and borrows an explicitly supplied base.
func New(token, baseURL string, base http.RoundTripper) *Client {
	if baseURL == "" {
		baseURL = BaseURL
	}
	owned := false
	if base == nil {
		base = http.DefaultTransport
		if defaultTransport, ok := base.(*http.Transport); ok {
			base = defaultTransport.Clone()
			owned = true
		}
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: transport{base}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("禁止 API 重定向") }}
	opts := []option.RequestOption{option.WithAPIToken(token), option.WithBaseURL(baseURL), option.WithHTTPClient(httpClient), option.WithMaxRetries(0), option.WithHeader("User-Agent", "gatework-cloudflare-go")}
	return &Client{records: sdkdns.NewRecordService(opts...), zones: zones.NewZoneService(opts...), http: httpClient, owned: owned}
}
func (c *Client) Close() {
	if c.owned {
		c.http.CloseIdleConnections()
	}
}
