package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/credentials"
	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/gatework/cloudflare-go/internal/fileio"
	"github.com/spf13/cobra"
)

func (a *app) addProfiles(root *cobra.Command) {
	var tokenEnv, account, defaultZone string
	var makeDefault bool
	initCmd := &cobra.Command{Use: "init", Short: "引导创建或更新命名配置；Token 不写入 JSON", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if cmd.Flags().Changed("token-env") && tokenEnv == "" {
			return usage("token-env 不能为空")
		}
		loaded, err := config.LoadOptional(a.path)
		if err != nil {
			return &usageError{err}
		}
		name := a.profile
		if name == "" {
			name = os.Getenv("CLOUDFLARE_PROFILE")
		}
		if name == "" {
			name = loaded.DefaultProfile
		}
		if name == "" {
			name = "default"
		}
		if !config.ValidProfile(name) {
			return usage("profile 名称只接受字母、数字、下划线和连字符")
		}
		if a.tokenFile != "" && tokenEnv != "" {
			return usage("token-file 与 token-env 互斥")
		}
		if account != "" && !config.ValidID(account) {
			return usage("account-id 无效")
		}

		previous, existed := loaded.Profiles[name]
		p := previous
		createdToken := ""
		defer func() {
			if createdToken != "" {
				_ = os.Remove(createdToken)
			}
		}()
		if cmd.Flags().Changed("account-id") {
			p.AccountID = account
		}
		if cmd.Flags().Changed("default-zone") {
			p.DefaultZone = defaultZone
		}
		if a.tokenFile != "" {
			p.TokenFile, err = filepath.Abs(a.tokenFile)
			if err != nil {
				return err
			}
			p.TokenEnv = ""
		}
		if tokenEnv != "" {
			p.TokenEnv = tokenEnv
			p.TokenFile = ""
		}
		if p.TokenFile != "" && !filepath.IsAbs(p.TokenFile) {
			p.TokenFile = filepath.Join(filepath.Dir(a.path), p.TokenFile)
		}

		if p.TokenFile != "" {
			if _, err := credentials.Read(p.TokenFile, ""); err != nil {
				return err
			}
			absolute, err := filepath.Abs(p.TokenFile)
			if err != nil {
				return err
			}
			p.TokenFile = absolute
		} else if p.TokenEnv != "" {
			if _, err := credentials.Read("", p.TokenEnv); err != nil {
				return err
			}
		} else if value := os.Getenv("CLOUDFLARE_API_TOKEN"); value != "" {
			if _, err := credentials.Validate(value); err != nil {
				return err
			}
			p.TokenEnv = "CLOUDFLARE_API_TOKEN"
		} else {
			if !a.interactive {
				return usage("非交互初始化需要 --token-file、--token-env 或 CLOUDFLARE_API_TOKEN")
			}
			_, _ = fmt.Fprint(a.err, "Cloudflare API Token（隐藏输入）：")
			var token string
			if file, ok := a.in.(*os.File); ok {
				token, err = readSecret(cmd.Context(), file)
			} else {
				token, err = readLine(cmd.Context(), a.in)
			}
			_, _ = fmt.Fprintln(a.err)
			if err != nil {
				return err
			}
			token, err = credentials.Validate(token)
			if err != nil {
				return err
			}
			dir, err := filepath.Abs(filepath.Join(filepath.Dir(a.path), "credentials"))
			if err != nil {
				return err
			}
			if err = os.MkdirAll(dir, 0700); err != nil {
				return err
			}
			f, err := os.CreateTemp(dir, name+"-*.token")
			if err != nil {
				return err
			}
			path := f.Name()
			_ = f.Close()
			if err = fileio.Publish(path, true, func(w io.Writer) error { _, err := io.WriteString(w, token+"\n"); return err }); err != nil {
				_ = os.Remove(path)
				return err
			}
			p.TokenFile = path
			createdToken = path
		}
		if defaultZone != "" {
			normalized, err := normalizeZone(defaultZone)
			if err != nil {
				return err
			}
			p.DefaultZone = normalized
		}
		err = config.Update(a.path, func(c *config.Config) error {
			if current, ok := c.Profiles[name]; ok != existed || current != previous {
				return fmt.Errorf("%w：初始化期间 profile 已被另一进程修改，请重新运行", dns.ErrConflict)
			}
			c.Profiles[name] = p
			if makeDefault || c.DefaultProfile == "" {
				c.DefaultProfile = name
			}
			return nil
		})
		if err != nil {
			var published *fileio.PublishedError
			if errors.As(err, &published) {
				createdToken = ""
			}
			return err
		}
		createdToken = ""
		_, err = fmt.Fprintf(a.out, "配置 %s 已保存；凭据内容未写入 JSON。\n", name)
		return err
	}}
	initCmd.Flags().StringVar(&tokenEnv, "token-env", "", "保存环境变量名作为凭据来源")
	initCmd.Flags().StringVar(&account, "account-id", "", "可选，限制账号 ID")
	initCmd.Flags().StringVar(&defaultZone, "default-zone", "", "可选，简写记录名使用的 Zone")
	initCmd.Flags().BoolVar(&makeDefault, "default", false, "设为默认配置")
	root.AddCommand(initCmd)
	profile := commandGroup("profile", "管理命名配置")
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
			return config.Update(a.path, func(c *config.Config) error {
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
			_, err = fmt.Fprintln(a.out, "配置有效；凭据权限与远端访问需通过 zone list 验证。")
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
