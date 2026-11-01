package cloudflare

import (
	"context"

	sdkdns "github.com/cloudflare/cloudflare-go/v7/dns"
	"github.com/cloudflare/cloudflare-go/v7/option"
)

// A single bounded read proves DNS read access, even for an empty Zone. It
// deliberately does not paginate or infer permission to modify DNS records.
func (c *Client) CheckDNSRead(ctx context.Context, zone string) error {
	ctx, m := callContext(ctx)
	p := sdkdns.RecordListParams{}
	p.ZoneID.Value, p.ZoneID.Present = zone, true
	p.PerPage.Value, p.PerPage.Present = 1, true
	p.Page.Value, p.Page.Present = 1, true
	_, err := c.records.List(ctx, p, option.WithMaxRetries(2))
	return failure(ctx, m, err)
}
