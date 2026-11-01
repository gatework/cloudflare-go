package cli

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/credentials"
	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/spf13/cobra"
)

func (a *app) guideProfile(cmd *cobra.Command, p *config.Profile, explicit bool) error {
	token, err := credentials.Read(p.TokenFile, p.TokenEnv)
	if err != nil {
		return err
	}
	api, close := a.factory(token)
	defer close()
	ctx, cancel, err := a.context(cmd, false)
	if err != nil {
		return err
	}
	defer cancel()
	zones, err := api.ListZones(ctx, "", p.AccountID, a.maxBytes)
	if err != nil {
		return err
	}
	candidate := *p
	if a.interactive && !explicit {
		cancel()
		chosen, err := a.chooseZone(cmd, zones, p.DefaultZone)
		if err != nil {
			return err
		}
		ctx, cancel, err = a.context(cmd, false)
		if err != nil {
			return err
		}
		defer cancel()
		candidate.DefaultZone = chosen.Name
		if chosen.ID != "" && candidate.AccountID == "" {
			count := 0
			for _, zone := range zones {
				if zone.Name == chosen.Name {
					count++
				}
			}
			if count > 1 {
				candidate.AccountID = chosen.AccountID
			}
		}
	}
	if candidate.DefaultZone == "" {
		_, err := fmt.Fprintf(a.err, "Zone 读取已验证（%d 个）；未选择默认 Zone，DNS 读取检查已跳过。\n", len(zones))
		if err == nil {
			*p = candidate
		}
		return err
	}
	zone, err := namedZone(zones, candidate.DefaultZone, candidate.AccountID)
	if err != nil {
		return err
	}
	if err := api.CheckDNSRead(ctx, zone.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(a.err, "已验证 %s 的 DNS 读取权限；写权限未检查。\n", zone.Name); err != nil {
		return err
	}
	*p = candidate
	return nil
}

func namedZone(zones []dns.Zone, name, account string) (dns.Zone, error) {
	name, err := normalizeZone(name)
	if err != nil {
		return dns.Zone{}, err
	}
	var matches []dns.Zone
	for _, z := range zones {
		if z.Name == name && (account == "" || account == z.AccountID) {
			matches = append(matches, z)
		}
	}
	if len(matches) == 0 {
		return dns.Zone{}, dns.ErrNotFound
	}
	if len(matches) > 1 {
		return dns.Zone{}, fmt.Errorf("%w：同名 Zone 属于多个账号，请指定 --account-id", dns.ErrConflict)
	}
	return matches[0], nil
}

func (a *app) chooseZone(cmd *cobra.Command, zones []dns.Zone, current string) (dns.Zone, error) {
	if len(zones) == 0 && current == "" {
		return dns.Zone{}, nil
	}
	for i, z := range zones {
		if _, err := fmt.Fprintf(a.err, "%d. %s（账号 %s）\n", i+1, z.Name, z.AccountID); err != nil {
			return dns.Zone{}, err
		}
	}
	for {
		if _, err := fmt.Fprintf(a.err, "选择默认 Zone（序号，0 跳过；回车保留 %q）：", current); err != nil {
			return dns.Zone{}, err
		}
		answer, err := readLine(cmd.Context(), a.in)
		if err != nil {
			return dns.Zone{}, err
		}
		if answer == "" {
			return dns.Zone{Name: current}, nil
		}
		if answer == "0" {
			return dns.Zone{}, nil
		}
		n, err := strconv.Atoi(answer)
		if err == nil && n >= 1 && n <= len(zones) {
			return zones[n-1], nil
		}
		if _, err := fmt.Fprintln(a.err, "请输入显示的序号，或 0 跳过。"); err != nil {
			return dns.Zone{}, err
		}
	}
}

type accessCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (a *app) profileCheckCommand() *cobra.Command {
	return &cobra.Command{Use: "check [NAME]", Short: "只读验证凭据、Zone 与所选 Zone 的 DNS 访问", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			a.profile = args[0]
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
		steps := []accessCheck{{"credentials", "ok", "凭据已从指定来源读取，未输出内容"}}
		finish := func(checkErr error) error {
			return errors.Join(checkErr, a.render(ctx, steps, false, p))
		}
		zones, err := api.ListZones(ctx, "", p.AccountID, a.maxBytes)
		if err != nil {
			steps = append(steps, accessCheck{"zones", "failed", err.Error()})
			return finish(err)
		}
		steps = append(steps, accessCheck{"zones", "ok", fmt.Sprintf("可访问 %d 个 Zone", len(zones))})
		name := a.zoneName
		if name == "" && a.zoneID == "" {
			name = p.DefaultZone
		}
		if name == "" && a.zoneID == "" {
			steps = append(steps, accessCheck{"dns", "skipped", "通过 --zone 或 --zone-id 选择目标后可检查 DNS 读取；未检查写权限"})
			return finish(nil)
		}
		var zone dns.Zone
		if a.zoneID != "" {
			service := dns.Service{API: api, AccountID: p.AccountID, MaxBytes: a.maxBytes}
			zone, err = service.Zone(ctx, "", name, a.zoneID)
		} else {
			zone, err = namedZone(zones, name, p.AccountID)
		}
		if err == nil {
			err = api.CheckDNSRead(ctx, zone.ID)
		}
		if err != nil {
			steps = append(steps, accessCheck{"dns", "failed", err.Error()})
			return finish(err)
		}
		steps = append(steps, accessCheck{"dns", "ok", zone.Name + "：读取已验证，写权限未检查"})
		return finish(nil)
	}}
}
