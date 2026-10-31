package cli

import (
	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/spf13/cobra"
)

func (a *app) addZones(root *cobra.Command) {
	group := commandGroup("zone", "查询 Zone，不修改 Zone 本身")
	group.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if err := a.validateOutput(); err != nil {
			return err
		}
		api, close, p, err := a.connect()
		if err != nil {
			return err
		}
		defer close()
		ctx, cancel, err := a.context(cmd, false)
		if err != nil {
			return err
		}
		defer cancel()
		zones, err := api.ListZones(ctx, "", p.AccountID)
		if err != nil {
			return err
		}
		return a.render(zones, false, p)
	}})
	group.AddCommand(&cobra.Command{Use: "get [DOMAIN]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if err := a.validateOutput(); err != nil {
			return err
		}
		api, close, p, err := a.connect()
		if err != nil {
			return err
		}
		defer close()
		zoneName := a.zoneName
		if len(args) > 0 {
			zoneName = args[0]
		}
		if zoneName == "" && a.zoneID == "" {
			zoneName = p.DefaultZone
		}
		if zoneName == "" && a.zoneID == "" {
			return usage("需要 Zone 名称或 --zone-id")
		}
		if a.zoneID != "" && !config.ValidID(a.zoneID) {
			return usage("zone-id 无效")
		}
		if zoneName != "" {
			zoneName, err = normalizeZone(zoneName)
			if err != nil {
				return err
			}
		}
		ctx, cancel, err := a.context(cmd, false)
		if err != nil {
			return err
		}
		defer cancel()
		service := dns.Service{API: api, AccountID: p.AccountID, MaxBytes: a.maxBytes}
		zone, err := service.Zone(ctx, "", zoneName, a.zoneID)
		if err != nil {
			return err
		}
		return a.render(zone, false, p)
	}})
	root.AddCommand(group)
}
