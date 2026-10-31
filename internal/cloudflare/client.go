// Package cloudflare adapts the official SDK without leaking generated types.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
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

type metadataKey struct{}
type metadata struct {
	status     int
	codes      []int
	requestID  string
	totalPages int
	page       int
}
type transport struct{ base http.RoundTripper }

func (t transport) RoundTrip(request *http.Request) (*http.Response, error) {
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
		return nil, errors.New("Cloudflare 单次响应超过 16 MiB")
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	m, _ := request.Context().Value(metadataKey{}).(*metadata)
	if m != nil {
		m.status = response.StatusCode
		m.requestID = response.Header.Get("Cf-Ray")
		m.codes = nil
		var envelope struct {
			Info struct {
				TotalPages int `json:"total_pages"`
				Page       int `json:"page"`
			} `json:"result_info"`
			Errors []struct {
				Code int `json:"code"`
			} `json:"errors"`
		}
		_ = json.Unmarshal(data, &envelope)
		m.totalPages, m.page = envelope.Info.TotalPages, envelope.Info.Page
		for _, e := range envelope.Errors {
			m.codes = append(m.codes, e.Code)
		}
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 && !strings.HasSuffix(request.URL.Path, "/export") {
		var envelope struct {
			Success *bool           `json:"success"`
			Result  json.RawMessage `json:"result"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.Success == nil || !*envelope.Success || len(envelope.Result) == 0 || bytes.Equal(envelope.Result, []byte("null")) {
			_ = response.Body.Close()
			return nil, &Error{Status: response.StatusCode, Kind: "protocol"}
		}
	}
	return response, nil
}

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
		base = http.DefaultTransport.(*http.Transport).Clone()
	}
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: transport{base}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("禁止 API 重定向") }}
	opts := []option.RequestOption{option.WithAPIToken(token), option.WithBaseURL(baseURL), option.WithHTTPClient(httpClient), option.WithMaxRetries(0), option.WithHeader("User-Agent", "gatework-cloudflare-go")}
	return &Client{records: sdkdns.NewRecordService(opts...), zones: zones.NewZoneService(opts...), http: httpClient}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
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
	return &Error{Status: m.status, Codes: m.codes, RequestID: m.requestID, Kind: "api/network"}
}
func (c *Client) ListZones(ctx context.Context, name, account string) ([]dns.Zone, error) {
	ctx, m := callContext(ctx)
	p := zones.ZoneListParams{}
	if name != "" {
		p.Name.Value = name
		p.Name.Present = true
	}
	p.PerPage.Value = 50
	p.PerPage.Present = true
	if account != "" {
		p.Account.Value.ID.Value = account
		p.Account.Value.ID.Present = true
		p.Account.Present = true
	}
	out := []dns.Zone{}
	seen := map[string]bool{}
	for page := 1; ; page++ {
		p.Page.Value = float64(page)
		p.Page.Present = true
		batch, err := c.zones.List(ctx, p, option.WithMaxRetries(2))
		if err != nil {
			return nil, failure(ctx, m, err)
		}
		if m.page != 0 && m.page != page {
			return nil, errors.New("Zone 分页编号不一致")
		}
		fresh := 0
		for _, z := range batch.Result {
			if z.ID == "" || z.Name == "" {
				return nil, errors.New("Zone 响应缺少身份")
			}
			if seen[z.ID] {
				continue
			}
			seen[z.ID] = true
			fresh++
			if (name == "" || z.Name == name) && (account == "" || z.Account.ID == account) {
				out = append(out, dns.Zone{ID: z.ID, Name: z.Name, AccountID: z.Account.ID, Status: string(z.Status)})
			}
		}
		if len(batch.Result) == 0 || m.totalPages > 0 && page >= m.totalPages {
			break
		}
		if fresh == 0 {
			return nil, errors.New("Zone 分页未前进")
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func (c *Client) GetZone(ctx context.Context, id string) (dns.Zone, error) {
	ctx, m := callContext(ctx)
	p := zones.ZoneGetParams{}
	p.ZoneID.Value = id
	p.ZoneID.Present = true
	z, err := c.zones.Get(ctx, p, option.WithMaxRetries(2))
	if err != nil {
		return dns.Zone{}, failure(ctx, m, err)
	}
	return dns.Zone{ID: z.ID, Name: z.Name, AccountID: z.Account.ID, Status: string(z.Status)}, nil
}
func (c *Client) ListRecords(ctx context.Context, id string, q dns.Query, maxBytes int64) ([]dns.Record, error) {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordListParams{}
	p.ZoneID.Value = id
	p.ZoneID.Present = true
	p.PerPage.Value = 100
	p.PerPage.Present = true
	if q.Domain != "" {
		p.Name.Present = true
		if q.Exact {
			p.Name.Value.Exact.Value = q.Domain
			p.Name.Value.Exact.Present = true
		} else {
			p.Name.Value.Endswith.Value = q.Domain
			p.Name.Value.Endswith.Present = true
		}
	}
	if maxBytes <= 0 {
		maxBytes = 256 << 20
	}
	var total int64
	seen := map[string]dns.Record{}
	records := []dns.Record{}
	for page := 1; ; page++ {
		p.Page.Value = float64(page)
		p.Page.Present = true
		batch, err := c.records.List(ctx, p, option.WithMaxRetries(2))
		if err != nil {
			return nil, failure(ctx, m, err)
		}
		if m.page != 0 && m.page != page {
			return nil, errors.New("DNS 分页编号不一致")
		}
		fresh := 0
		for _, value := range batch.Result {
			raw := value.JSON.RawJSON()
			total += int64(len(raw))
			if total > maxBytes {
				return nil, errors.New("记录结果超过 --max-bytes；请缩小查询或提高额度")
			}
			record, err := dns.ParseRecord([]byte(raw))
			if err != nil {
				return nil, err
			}
			if old, ok := seen[record.ID]; ok {
				if !dns.EqualJSON(old.Raw, record.Raw) {
					return nil, fmt.Errorf("%w：分页期间相同 ID 内容变化", dns.ErrConflict)
				}
				continue
			}
			seen[record.ID] = record
			fresh++
			if q.Match(record) {
				records = append(records, record)
			}
		}
		if len(batch.Result) == 0 || m.totalPages > 0 && page >= m.totalPages {
			break
		}
		if fresh == 0 {
			return nil, errors.New("DNS 分页未前进")
		}
	}

	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Content != b.Content {
			return a.Content < b.Content
		}
		return a.ID < b.ID
	})
	return records, nil
}
func (c *Client) GetRecord(ctx context.Context, zone, id string) (dns.Record, error) {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordGetParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	result, err := c.records.Get(ctx, id, p, option.WithMaxRetries(2))
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	return dns.ParseRecord([]byte(result.JSON.RawJSON()))
}
func (c *Client) CreateRecord(ctx context.Context, zone string, patch dns.RecordPatch) (dns.Record, error) {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordNewParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	body, err := json.Marshal(patch)
	if err != nil {
		return dns.Record{}, err
	}
	result, err := c.records.New(ctx, p, option.WithRequestBody("application/json", body))
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	return dns.ParseRecord([]byte(result.JSON.RawJSON()))
}
func (c *Client) UpdateRecord(ctx context.Context, zone, id string, patch dns.RecordPatch) (dns.Record, error) {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordEditParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	body, err := json.Marshal(patch)
	if err != nil {
		return dns.Record{}, err
	}
	result, err := c.records.Edit(ctx, id, p, option.WithRequestBody("application/json", body))
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	return dns.ParseRecord([]byte(result.JSON.RawJSON()))
}
func (c *Client) DeleteRecord(ctx context.Context, zone, id string) error {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordDeleteParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	_, err := c.records.Delete(ctx, id, p)
	return failure(ctx, m, err)
}
func (c *Client) ExportZone(ctx context.Context, id string) (string, error) {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordExportParams{}
	p.ZoneID.Value = id
	p.ZoneID.Present = true
	result, err := c.records.Export(ctx, p, option.WithMaxRetries(2))
	if err != nil {
		return "", failure(ctx, m, err)
	}
	return *result, nil
}
