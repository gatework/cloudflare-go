package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cloudflare/cloudflare-go/v7/option"
	"github.com/cloudflare/cloudflare-go/v7/zones"
	"github.com/gatework/cloudflare-go/internal/dns"
)

func (c *Client) ListZones(ctx context.Context, name, account string) ([]dns.Zone, error) {
	ctx, m, stop := callContext(ctx)
	defer stop()
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
	seen := map[string]dns.Zone{}
	pagination := pageTracker{}
	for page := 1; ; page++ {
		p.Page.Value = float64(page)
		p.Page.Present = true
		var batch struct {
			Result []zoneResponse `json:"result"`
		}
		_, err := c.zones.List(ctx, p, option.WithMaxRetries(2), option.WithResponseBodyInto(&batch))
		if err != nil {
			return nil, failure(ctx, m, err)
		}
		last, err := pagination.observe(m.info, page, len(batch.Result))
		if err != nil {
			return nil, err
		}
		fresh := 0
		for _, z := range batch.Result {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if z.ID == "" || z.Name == "" {
				return nil, errors.New("区域响应缺少身份")
			}
			zone := dns.Zone{ID: z.ID, Name: z.Name, AccountID: z.Account.ID, Status: z.Status}
			if old, ok := seen[z.ID]; ok {
				if old != zone {
					return nil, fmt.Errorf("%w：分页期间相同 Zone ID 内容变化", dns.ErrConflict)
				}
				continue
			}
			seen[z.ID] = zone
			fresh++
			if (name == "" || z.Name == name) && (account == "" || z.Account.ID == account) {
				out = append(out, zone)
			}
		}
		if last {
			if err := pagination.complete(len(seen)); err != nil {
				return nil, err
			}
			break
		}
		if fresh == 0 {
			return nil, errors.New("区域分页未前进")
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
	ctx, m, stop := callContext(ctx)
	defer stop()
	p := zones.ZoneGetParams{}
	p.ZoneID.Value = id
	p.ZoneID.Present = true
	var result struct {
		Result zoneResponse `json:"result"`
	}
	_, err := c.zones.Get(ctx, p, option.WithMaxRetries(2), option.WithResponseBodyInto(&result))
	if err != nil {
		return dns.Zone{}, failure(ctx, m, err)
	}
	z := result.Result
	if !strings.EqualFold(z.ID, id) || z.Name == "" {
		return dns.Zone{}, protocolError(m)
	}
	return dns.Zone{ID: z.ID, Name: z.Name, AccountID: z.Account.ID, Status: z.Status}, nil
}

type zoneResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Account struct {
		ID string `json:"id"`
	} `json:"account"`
}
