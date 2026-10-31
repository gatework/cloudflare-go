// Package cloudflare adapts the official SDK without leaking generated types.
package cloudflare

import (
	"errors"
	"fmt"
	"net/http"
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
}

func (e *Error) Error() string {
	hint := "Cloudflare 请求失败"
	if e.Status == 401 || e.Status == 403 {
		hint = "认证或权限不足，请检查 Token 的 Zone Read / DNS Read / DNS Write 权限"
	}
	return fmt.Sprintf("%s（%s，HTTP %d，code %v，request %s）", hint, e.Kind, e.Status, e.Codes, e.RequestID)
}
func (e *Error) Definitive() bool     { return e.Status >= 400 && e.Status < 500 && e.Status != 408 }
func (e *Error) Is(target error) bool { return target == dns.ErrNotFound && e.Status == 404 }

type Client struct {
	records *sdkdns.RecordService
	zones   *zones.ZoneService
	http    *http.Client
}

func New(token, baseURL string, base http.RoundTripper) *Client {
	if baseURL == "" {
		baseURL = BaseURL
	}
	if base == nil {
		base = http.DefaultTransport
		if defaultTransport, ok := base.(*http.Transport); ok {
			base = defaultTransport.Clone()
		}
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: transport{base}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("禁止 API 重定向") }}
	opts := []option.RequestOption{option.WithAPIToken(token), option.WithBaseURL(baseURL), option.WithHTTPClient(httpClient), option.WithMaxRetries(0), option.WithHeader("User-Agent", "gatework-cloudflare-go")}
	return &Client{records: sdkdns.NewRecordService(opts...), zones: zones.NewZoneService(opts...), http: httpClient}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
