package cli

import (
	"sort"
	"strings"

	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/spf13/cobra"
)

func (a *app) addCompletion(root *cobra.Command) {
	group := commandGroup("completion", "生成 Bash、Zsh 或 Fish 补全脚本（无需凭据）")
	// Completion generation must also work without a config directory or Token.
	group.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
	for _, shell := range []string{"bash", "zsh", "fish"} {
		group.AddCommand(&cobra.Command{Use: shell, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			switch shell {
			case "bash":
				return root.GenBashCompletionV2(cmd.OutOrStdout(), true)
			case "zsh":
				return root.GenZshCompletion(cmd.OutOrStdout())
			default:
				return root.GenFishCompletion(cmd.OutOrStdout(), true)
			}
		}})
	}
	root.AddCommand(group)
	_ = root.RegisterFlagCompletionFunc("profile", a.completeProfiles)
	_ = root.RegisterFlagCompletionFunc("format", func(cmd *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
		values := []string{"table", "txt", "csv", "json"}
		if cmd.Parent() != nil && cmd.Parent().Name() == "dns" {
			if cmd.Name() == "list" || cmd.Name() == "get" || cmd.Name() == "dump" {
				values = append(values, "jsonl")
			}
			if cmd.Name() == "dump" {
				values = append(values, "bind")
			}
		}
		return matching(values, prefix), cobra.ShellCompDirectiveNoFileComp
	})
	var register func(*cobra.Command)
	register = func(cmd *cobra.Command) {
		if cmd.Flags().Lookup("type") != nil {
			_ = cmd.RegisterFlagCompletionFunc("type", func(_ *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
				return matching(dns.RecordTypes(), prefix), cobra.ShellCompDirectiveNoFileComp
			})
		}
		if cmd.Parent() != nil && cmd.Parent().Name() == "profile" && cmd.Name() != "list" {
			cmd.ValidArgsFunction = a.completeProfiles
		}
		for _, child := range cmd.Commands() {
			register(child)
		}
	}
	register(root)
}

func (a *app) completeProfiles(cmd *cobra.Command, _ []string, prefix string) ([]string, cobra.ShellCompDirective) {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		path = a.path
	}
	c, err := config.LoadOptional(path)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return matching(names, prefix), cobra.ShellCompDirectiveNoFileComp
}

func matching(values []string, prefix string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}
