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

func (c *Client) ListRecords(ctx context.Context, id string, q dns.Query) ([]dns.Record, error) {
	ctx, m, stop := callContext(ctx)
	defer stop()
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
	if q.Comment != nil {
		p.Comment.Present = true
		p.Comment.Value.Exact.Value, p.Comment.Value.Exact.Present = *q.Comment, true
	}
	if len(q.Tags) > 0 {
		p.Tag.Present = true
		p.Tag.Value.Exact.Value, p.Tag.Value.Exact.Present = q.Tags[0], true
		p.TagMatch.Value, p.TagMatch.Present = sdkdns.RecordListParamsTagMatchAll, true
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
	seen := map[string]recordSnapshot{}
	records := []dns.Record{}
	pagination := pageTracker{}
	for page := 1; ; page++ {
		p.Page.Value = float64(page)
		p.Page.Present = true
		// Bypass generated unions while keeping SDK parameters and request behavior.
		var batch struct {
			Result []json.RawMessage `json:"result"`
		}
		_, err := c.records.List(ctx, p, option.WithMaxRetries(2), option.WithResponseBodyInto(&batch))
		if err != nil {
			return nil, failure(ctx, m, err)
		}
		last, err := pagination.observe(m.info, page, len(batch.Result))
		if err != nil {
			return nil, err
		}
		fresh := 0
		for _, raw := range batch.Result {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			record, err := dns.ParseRecord(raw)
			if err != nil {
				return nil, err
			}
			if old, ok := seen[record.ID]; ok {
				equal, err := old.matches(record.Raw)
				if err != nil {
					return nil, err
				}
				if !equal {
					return nil, fmt.Errorf("%w：分页期间相同 ID 内容变化", dns.ErrConflict)
				}
				continue
			}
			selected := q.Match(record)
			state, err := snapshot(record.Raw, selected)
			if err != nil {
				return nil, err
			}
			seen[record.ID] = state
			fresh++
			if selected {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return records, nil
}
func (c *Client) GetRecord(ctx context.Context, zone, id string) (dns.Record, error) {
	ctx, m, stop := callContext(ctx)
	defer stop()
	p := sdkdns.RecordGetParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	var result recordEnvelope
	_, err := c.records.Get(ctx, id, p, option.WithMaxRetries(2), option.WithResponseBodyInto(&result))
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	record, err := dns.ParseRecord(result.Result)
	if err != nil || !strings.EqualFold(record.ID, id) {
		return dns.Record{}, protocolError(m)
	}
	return record, nil
}
func (c *Client) CreateRecord(ctx context.Context, zone string, patch dns.RecordPatch) (dns.Record, error) {
	ctx, m, stop := callContext(ctx)
	defer stop()
	p := sdkdns.RecordNewParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	body, err := json.Marshal(patch)
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	var result recordEnvelope
	_, err = c.records.New(ctx, p, option.WithRequestBody("application/json", body), option.WithResponseBodyInto(&result))
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	record, err := dns.ParseRecord(result.Result)
	if err != nil {
		return dns.Record{}, protocolError(m)
	}
	return record, nil
}
func (c *Client) UpdateRecord(ctx context.Context, zone, id string, patch dns.RecordPatch) (dns.Record, error) {
	ctx, m, stop := callContext(ctx)
	defer stop()
	p := sdkdns.RecordEditParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	body, err := json.Marshal(patch)
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	var result recordEnvelope
	_, err = c.records.Edit(ctx, id, p, option.WithRequestBody("application/json", body), option.WithResponseBodyInto(&result))
	if err != nil {
		return dns.Record{}, failure(ctx, m, err)
	}
	record, err := dns.ParseRecord(result.Result)
	if err != nil || !strings.EqualFold(record.ID, id) {
		return dns.Record{}, protocolError(m)
	}
	return record, nil
}
func (c *Client) DeleteRecord(ctx context.Context, zone, id string) error {
	ctx, m, stop := callContext(ctx)
	defer stop()
	p := sdkdns.RecordDeleteParams{}
	p.ZoneID.Value = zone
	p.ZoneID.Present = true
	var result struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	_, err := c.records.Delete(ctx, id, p, option.WithResponseBodyInto(&result))
	if err != nil {
		return failure(ctx, m, err)
	}
	if !strings.EqualFold(result.Result.ID, id) {
		return protocolError(m)
	}
	return nil
}
func (c *Client) ExportZone(ctx context.Context, id string) (string, error) {
	ctx, m, stop := callContext(ctx)
	defer stop()
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

type recordEnvelope struct {
	Result json.RawMessage `json:"result"`
}
