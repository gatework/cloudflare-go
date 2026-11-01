package cli

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/spf13/cobra"
)

// Validate what is known locally before selecting credentials or calling APIs.
func (a *app) preflight(cmd *cobra.Command, args []string) error {
	for _, arg := range args {
		if arg == "" || !utf8.ValidString(arg) {
			return usage("位置参数必须是非空 UTF-8 文本")
		}
	}
	if cmd.Flags().Changed("timeout") && a.timeout < time.Millisecond || a.maxBytes < 1 {
		return usage("timeout 和 max-bytes 必须大于零")
	}
	if cmd.Name() == "init" {
		if cmd.Flags().Changed("offline") && cmd.Flags().Changed("check") {
			return usage("offline 与 check 互斥")
		}
		for _, flag := range []string{"format", "output", "spreadsheet-safe", "zone", "zone-id"} {
			if cmd.Flags().Changed(flag) {
				return usage("init 不接受 --" + flag + "；默认域名请用 --default-zone")
			}
		}
	}
	if err := a.validateOutput(); err != nil {
		return err
	}
	format := a.formatFor(cmd.Name() == "dump", config.Profile{})
	if format == "bind" && cmd.CommandPath() != "cloudflare-go dns dump" {
		return usage("BIND 格式仅支持整 Zone dump")
	}
	if format == "jsonl" && (cmd.Parent() == nil || cmd.Parent().Name() != "dns" || cmd.Name() != "list" && cmd.Name() != "get" && cmd.Name() != "dump") {
		return usage("JSONL 仅支持 dns list/get/dump")
	}
	if cmd.Flags().Changed("match-tag") {
		tags, _ := cmd.Flags().GetStringArray("match-tag")
		for _, tag := range tags {
			if !utf8.ValidString(tag) || strings.IndexFunc(tag, unicode.IsControl) >= 0 || !strings.Contains(tag, ":") || strings.HasPrefix(tag, ":") {
				return usage("match-tag 需要 name:value 标签")
			}
		}
	}
	if cmd.Flags().Changed("match-comment") {
		comment, _ := cmd.Flags().GetString("match-comment")
		if !utf8.ValidString(comment) {
			return usage("match-comment 必须是 UTF-8 文本")
		}
	}
	if a.spreadsheet && (a.format != "" || a.output != "") && format != "csv" {
		return usage("spreadsheet-safe 只用于 CSV")
	}
	if cmd.Flags().Changed("zone") {
		name, err := normalizeZone(a.zoneName)
		if err != nil {
			return err
		}
		a.zoneName = name
	}
	for _, name := range []string{"id", "zone-id"} {
		if cmd.Flags().Changed(name) {
			value, _ := cmd.Flags().GetString(name)
			if !config.ValidID(value) {
				return usage(name + " 应为 32 位十六进制 ID")
			}
		}
	}
	if cmd.Flags().Changed("type") {
		values, _ := cmd.Flags().GetStringSlice("type")
		if len(values) == 0 {
			return usage("type 不能为空")
		}
		if (cmd.Name() == "create" || cmd.Name() == "update") && len(values) != 1 {
			return usage("单条写入只允许一种 type")
		}
		for _, value := range values {
			if _, err := dns.Type(value); err != nil {
				return &usageError{err}
			}
		}
	}
	if cmd.Flags().Changed("file") {
		value, _ := cmd.Flags().GetString("file")
		if value == "" {
			return usage("file 路径不能为空")
		}
		for _, flag := range strings.Fields("content content-file ttl proxied comment tag clear-tags new-name target params priority weight port caa-flags caa-tag caa-value") {
			if cmd.Flags().Changed(flag) {
				return usage("--file 不能与 --" + flag + " 混用")
			}
		}
	}
	if cmd.Flags().Changed("content") && cmd.Flags().Changed("content-file") {
		return usage("content 与 content-file 互斥")
	}
	return nil
}
