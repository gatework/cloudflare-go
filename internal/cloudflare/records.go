package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	sdkdns "github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/gatework/cloudflare-go/internal/dns"
)

func (c *Client) ListRecords(ctx context.Context, id string, q dns.Query, maxBytes int64) ([]dns.Record, error) {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordListParams{}
	p.ZoneID.Value = id
	p.ZoneID.Present = true
	p.PerPage.Value = 100
	p.PerPage.Present = true
	p.Match.Value = sdkdns.RecordListParamsMatchAll
	p.Match.Present = true
	// Push supported conjunctive filters down to the API, retaining local checks
	// for exact content and DNS label boundaries. Multiple types remain local OR.
	if len(q.Types) == 1 {
		p.Type.Value = sdkdns.RecordListParamsType(q.Types[0])
		p.Type.Present = true
	}
	if q.Proxied != nil {
		p.Proxied.Value = *q.Proxied
		p.Proxied.Present = true
	}
	if q.Content != nil {
		p.Content.Present = true
		p.Content.Value.Exact.Value = *q.Content
		p.Content.Value.Exact.Present = true
	}
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
	seen := map[string]json.RawMessage{}
	records := []dns.Record{}
	pagination := pageTracker{}
	for page := 1; ; page++ {
		p.Page.Value = float64(page)
		p.Page.Present = true
		batch, err := c.records.List(ctx, p, option.WithMaxRetries(2))
		if err != nil {
			return nil, failure(ctx, m, err)
		}
		last, err := pagination.observe(m.info, page, len(batch.Result))
		if err != nil {
			return nil, err
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
				if !dns.EqualJSON(old, record.Raw) {
					return nil, fmt.Errorf("%w：分页期间相同 ID 内容变化", dns.ErrConflict)
				}
				continue
			}
			seen[record.ID] = record.Raw
			fresh++
			if q.Match(record) {
				records = append(records, record)
			}
		}
		if last {
			if err := pagination.complete(len(seen)); err != nil {
				return nil, err
			}
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
	if result == nil {
		return dns.Record{}, protocolError(m)
	}
	record, err := dns.ParseRecord([]byte(result.JSON.RawJSON()))
	if err != nil || !strings.EqualFold(record.ID, id) {
		return dns.Record{}, protocolError(m)
	}
	return record, nil
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
	if result == nil {
		return dns.Record{}, protocolError(m)
	}
	record, err := dns.ParseRecord([]byte(result.JSON.RawJSON()))
	if err != nil {
		return dns.Record{}, protocolError(m)
	}
	return record, nil
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
	if result == nil {
		return dns.Record{}, protocolError(m)
	}
	record, err := dns.ParseRecord([]byte(result.JSON.RawJSON()))
	if err != nil || !strings.EqualFold(record.ID, id) {
		return dns.Record{}, protocolError(m)
	}
	return record, nil
}
func (c *Client) DeleteRecord(ctx context.Context, zone, id string) error {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordDeleteParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	result, err := c.records.Delete(ctx, id, p)
	if err != nil {
		return failure(ctx, m, err)
	}
	if result == nil || !strings.EqualFold(result.ID, id) {
		return protocolError(m)
	}
	return nil
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
	if result == nil {
		return "", protocolError(m)
	}
	return *result, nil
}
