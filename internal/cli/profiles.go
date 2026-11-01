package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/spf13/cobra"
)

func (a *app) addProfiles(root *cobra.Command) {
	root.AddCommand(a.initCommand())
	profile := commandGroup("profile", "管理命名配置")
	profile.AddCommand(a.profileCheckCommand())
	profile.AddCommand(&cobra.Command{Use: "list", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		c, err := a.loadConfig(false)
		if err != nil {
			return &usageError{err}
		}
		names := make([]string, 0, len(c.Profiles))
		for name := range c.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := []map[string]any{}
		for _, name := range names {
			rows = append(rows, map[string]any{"name": name, "default": name == c.DefaultProfile, "profile": c.Profiles[name]})
		}
		return a.render(cmd.Context(), rows, false, config.Profile{})
	}})
	profile.AddCommand(&cobra.Command{Use: "show [NAME]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		name := a.profile
		if len(args) > 0 {
			name = args[0]
		}
		if name == "" {
			name = os.Getenv("CLOUDFLARE_PROFILE")
		}
		c, err := a.loadConfig(false)
		if err != nil {
			return err
		}
		if name == "" {
			name = c.DefaultProfile
		}
		p, ok := c.Profiles[name]
		if !ok {
			return usage("profile 不存在")
		}
		return a.render(cmd.Context(), map[string]any{"name": name, "profile": p}, false, config.Profile{})
	}})
	for _, action := range []string{"use", "remove"} {
		profile.AddCommand(&cobra.Command{Use: action + " NAME", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			return config.UpdateContext(cmd.Context(), a.path, func(c *config.Config) error {
				if _, ok := c.Profiles[args[0]]; !ok {
					return usage("profile 不存在")
				}
				if action == "use" {
					c.DefaultProfile = args[0]
				} else {
					delete(c.Profiles, args[0])
					if c.DefaultProfile == args[0] {
						c.DefaultProfile = ""
					}
				}
				return nil
			})
		}})
	}
	root.AddCommand(profile)
	group := commandGroup("config", "查看并检查本地配置（不连接 API）")
	for _, action := range []string{"show", "check"} {
		group.AddCommand(&cobra.Command{Use: action, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.loadConfig(true)
			if err != nil {
				return &usageError{err}
			}
			if action == "show" {
				return a.render(cmd.Context(), c, false, config.Profile{})
			}
			for _, p := range c.Profiles {
				if p.DefaultZone != "" {
					if _, err := normalizeZone(p.DefaultZone); err != nil {
						return err
					}
				}
			}
			_, err = fmt.Fprintln(a.out, "配置有效；远端读取权限可通过 profile check 验证。")
			return err
		}})
	}
	root.AddCommand(group)
}
func normalizeZone(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("区域名称不能为空")
	}
	return normalizeName(value, "", false)
}
