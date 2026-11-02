package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/gatework/cloudflare-go/internal/fileio"
	"github.com/gatework/cloudflare-go/internal/output"
	"github.com/spf13/cobra"
)

func normalizeName(value, zone string, record bool) (string, error) {
	name, err := dns.Name(value, zone, record)
	if err != nil {
		return "", &usageError{err}
	}
	return name, nil
}

type recordFlags struct {
	patch        dns.RecordPatch
	types        []string
	exact        bool
	id           string
	matchContent string
	matchTags    []string
	matchComment string

	yes         bool
	dryRun      bool
	proxied     bool
	content     string
	contentFile string
	ttl         string
	comment     string
	tags        []string
	clearTags   bool
	file        string
	newName     string

	target   string
	params   string
	priority int
	weight   int
	port     int
	caaFlags int
	caaTag   string
	caaValue string
}

func (a *app) addDNS(root *cobra.Command) {
	group := commandGroup("dns", "单条记录增删改查与完整导出")
	for _, action := range []string{"list", "get", "create", "update", "delete", "dump"} {
		flags := &recordFlags{}
		command := &cobra.Command{Use: action + " [DOMAIN]", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error { return a.records(cmd, args, action, flags) }}
		command.PreRunE = func(cmd *cobra.Command, _ []string) error { return prepareRecordInput(cmd, flags, action) }
		f := command.Flags()
		if action == "list" || action == "get" || action == "dump" {
			f.StringArrayVar(&flags.matchTags, "match-tag", nil, "完整标签精确匹配，可重复，多个标签按 AND")
			f.StringVar(&flags.matchComment, "match-comment", "", "按注释精确筛选，区分大小写")
		}
		f.StringSliceVar(&flags.types, "type", nil, "记录类型，可重复；写入时只允许一种")
		f.BoolVar(&flags.proxied, "proxied", false, "筛选代理状态；创建或修改时设置代理状态")
		if action == "list" || action == "dump" {
			f.BoolVar(&flags.exact, "exact", false, "只匹配精确 DNS 名称")
		}
		if action == "get" || action == "update" || action == "delete" {
			f.StringVar(&flags.id, "id", "", "精确记录 ID，避免同名多记录歧义")
		}
		if action != "create" {
			f.StringVar(&flags.matchContent, "match-content", "", "按内容精确筛选原记录")
		}
		if action == "create" || action == "update" {
			f.StringVar(&flags.content, "content", "", "记录内容")
			f.StringVar(&flags.contentFile, "content-file", "", "从普通文件读取内容，保留原有空白")
			f.StringVar(&flags.ttl, "ttl", "", "auto 或 TTL 秒数；创建默认 auto")
			f.StringVar(&flags.comment, "comment", "", "注释；空字符串清除")
			f.StringSliceVar(&flags.tags, "tag", nil, "标签；指定后替换完整标签列表，逗号需通过文件表达")
			f.BoolVar(&flags.clearTags, "clear-tags", false, "显式清空标签")
			f.StringVar(&flags.file, "file", "", "单个记录或补丁 JSON 文件，不与内容字段参数混用")
			f.StringVar(&flags.target, "target", "", "SRV/HTTPS/SVCB 目标")
			f.StringVar(&flags.params, "params", "", "HTTPS/SVCB 服务参数，映射 data.value")
			f.IntVar(&flags.priority, "priority", 0, "MX/SRV/HTTPS/SVCB 优先级")
			f.IntVar(&flags.weight, "weight", 0, "SRV 权重")
			f.IntVar(&flags.port, "port", 0, "SRV 端口")
			f.IntVar(&flags.caaFlags, "caa-flags", 0, "CAA flags，0–255")
			f.StringVar(&flags.caaTag, "caa-tag", "", "CAA tag，例如 issue")
			f.StringVar(&flags.caaValue, "caa-value", "", "CAA value")
			if action == "update" {
				f.StringVar(&flags.newName, "new-name", "", "重命名记录；仍需属于相同 Zone")
			}
		}
		if action == "create" || action == "update" || action == "delete" {
			f.BoolVar(&flags.dryRun, "dry-run", false, "预览变更，不发送写请求")
		}
		if action == "delete" {
			f.BoolVar(&flags.yes, "yes", false, "明确执行删除，适用于非交互环境")
		}
		group.AddCommand(command)
	}
	root.AddCommand(group)
}
func (a *app) records(cmd *cobra.Command, args []string, action string, f *recordFlags) error {
	format := a.format
	if format == "" && strings.ToLower(filepath.Ext(a.output)) == ".zone" {
		format = "bind"
	}

	api, close, p, err := a.connect()
	if err != nil {
		return err
	}
	defer close()
	if a.spreadsheet && a.formatFor(action == "dump", p) != "csv" {
		return usage("spreadsheet-safe 只用于 CSV")
	}
	zoneName := a.zoneName
	if zoneName == "" && a.zoneID == "" {
		zoneName = p.DefaultZone
	}
	if zoneName != "" {
		zoneName, err = normalizeZone(zoneName)
		if err != nil {
			return err
		}
	}
	domain := ""
	if len(args) > 0 {
		domain = args[0]
		if domain == "" {
			return usage("域名参数不能为空")
		}
	}
	ctx, cancel, err := a.context(cmd, action == "dump")
	if err != nil {
		return err
	}
	defer cancel()
	service := dns.Service{API: api, AccountID: p.AccountID}
	// An explicit Zone ID can resolve apex/relative names without guessing.
	var zone dns.Zone
	if a.zoneID != "" {
		zone, err = service.Zone(ctx, "", zoneName, a.zoneID)
		if err != nil {
			return err
		}
		zoneName = zone.Name
	}
	if domain == "" {
		if f.id == "" {
			domain = zoneName
			if domain == "" {
				return usage("需要完整域名，或配置默认 Zone")
			}
		}
	} else {
		domain, err = normalizeName(domain, zoneName, true)
		if err != nil {
			return err
		}
	}
	if a.zoneID == "" {
		zone, err = service.Zone(ctx, domain, zoneName, "")
		if err != nil {
			return err
		}
	} else if domain != "" && !dns.Within(domain, zone.Name) {
		return usage("记录名称不属于所选 Zone")
	}
	query := dns.Query{Domain: domain, Exact: f.exact, Types: f.types, Tags: f.matchTags}
	if cmd.Flags().Changed("match-comment") {
		query.Comment = &f.matchComment
	}
	if cmd.Flags().Changed("proxied") && action != "create" && action != "update" {
		query.Proxied = &f.proxied
	}
	if cmd.Flags().Changed("match-content") {
		query.Content = &f.matchContent
	}
	if action == "list" || action == "dump" {
		if format == "bind" {
			if domain != zone.Name || f.exact || len(f.types) > 0 || query.Proxied != nil || query.Content != nil || len(query.Tags) > 0 || query.Comment != nil {
				return usage("BIND 只能导出整个 Zone，不能附加记录筛选")
			}
			text, err := api.ExportZone(ctx, zone.ID)
			if err != nil {
				return err
			}
			return a.render(ctx, text, true, p)
		}
		records, err := api.ListRecords(ctx, zone.ID, query)
		if err != nil {
			return err
		}
		if action == "list" {
			return a.render(ctx, records, false, p)
		}
		return a.render(ctx, dns.Dump{SchemaVersion: 1, ExportedAt: time.Now().UTC(), Zone: zone, Query: query, Records: records}, true, p)
	}
	selector := dns.Selector{Query: query, ID: f.id}
	selector.Exact = true
	if action == "get" {
		record, err := service.Select(ctx, zone, selector)
		if err != nil {
			return err
		}
		return a.render(ctx, record, false, p)
	}
	if action == "create" && domain == "" {
		return usage("创建记录需要名称")
	}
	patch := f.patch
	if action == "create" {
		if given, ok := patch["name"]; ok {
			var name string
			if json.Unmarshal(given, &name) != nil {
				return usage("name 需要字符串")
			}
			name, err = normalizeName(name, zone.Name, true)
			if err != nil {
				return err
			}
			if name != domain {
				return usage("文件中的 name 与命令目标不一致")
			}
		}
	}
	change, err := service.Prepare(ctx, zone, action, selector, patch)
	if err != nil {
		return err
	}
	if action == "create" || action == "update" {
		typ := ""
		if change.Before != nil {
			typ = change.Before.Type
		} else {
			_ = json.Unmarshal(change.Changes["type"], &typ)
		}
		if err := validateRecordFlags(cmd, typ); err != nil {
			return err
		}
	}
	if f.dryRun {
		return a.render(ctx, change, false, p)
	}
	// Reserve the output before a mutation, so a missing/unwritable parent
	// directory cannot cause a successful DNS change followed by a local failure.
	var destination *fileio.Output
	if a.output != "" {
		destination, err = fileio.NewOutput(a.output, a.overwrite)
		if err != nil {
			return err
		}
		defer destination.Close()
	}
	if action == "delete" && !f.yes {
		cancel()
		if err = a.confirm(cmd.Context(), fmt.Sprintf("删除 %s %s（ID %s）？", change.Before.Name, change.Before.Type, change.RecordID)); err != nil {
			return err
		}
		ctx, cancel, err = a.context(cmd, false)
		if err != nil {
			return err
		}
		defer cancel()
	}
	value, err := service.Apply(ctx, change)
	if err != nil {
		return err
	}
	if destination != nil {
		err = output.RenderContext(ctx, destination, a.formatFor(false, p), value, a.spreadsheet)
		if err == nil {
			err = destination.CommitContext(ctx)
		}
	} else {
		err = a.render(ctx, value, false, p)
	}
	if err != nil {
		return fmt.Errorf("DNS %s 已成功，但结果输出失败；请查询记录确认：%w", action, err)
	}
	return nil
}
func prepareRecordInput(cmd *cobra.Command, f *recordFlags, action string) error {
	if cmd.Flags().Changed("type") && len(f.types) == 0 {
		return usage("type 不能为空")
	}
	if (action == "create" || action == "update") && len(f.types) > 1 {
		return usage("单条写入只允许一种 type")
	}
	for i, typ := range f.types {
		value, err := dns.Type(typ)
		if err != nil {
			return &usageError{err}
		}
		f.types[i] = value
	}
	if action != "create" && action != "update" {
		return nil
	}
	patch, err := buildPatch(cmd, f, action)
	if err != nil {
		return &usageError{err}
	}
	typ := ""
	if len(f.types) == 1 {
		typ = f.types[0]
	}
	if err := dns.ValidatePatchInput(patch, action == "create", typ); err != nil {
		return &usageError{err}
	}
	if value, ok := patch["type"]; ok {
		_ = json.Unmarshal(value, &typ)
	}
	if typ != "" {
		if err := validateRecordFlags(cmd, typ); err != nil {
			return err
		}
	}
	f.patch = patch
	return nil
}

func buildPatch(cmd *cobra.Command, f *recordFlags, action string) (dns.RecordPatch, error) {
	changed := cmd.Flags().Changed
	for _, value := range append([]string{f.content, f.comment, f.target, f.params, f.caaTag, f.caaValue, f.newName}, f.tags...) {
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("记录参数必须是 UTF-8 文本，未替换非法字节")
		}
	}
	keys := []string{"content", "content-file", "ttl", "proxied", "comment", "tag", "clear-tags", "new-name", "target", "params", "priority", "weight", "port", "caa-flags", "caa-tag", "caa-value"}
	if changed("file") {
		if f.file == "" {
			return nil, fmt.Errorf("file 路径不能为空")
		}
		for _, key := range keys {
			if changed(key) {
				return nil, fmt.Errorf("--file 不能与 --%s 混用", key)
			}
		}
		data, err := fileio.ReadRegularFile(f.file, 1<<20)
		if err != nil {
			return nil, err
		}
		patch, err := dns.DecodePatch(data)
		if err != nil {
			return nil, err
		}
		if action == "create" && len(f.types) > 0 {
			if old, ok := patch["type"]; ok && !dns.EqualJSON(old, raw(f.types[0])) {
				return nil, fmt.Errorf("文件类型与 --type 不一致")
			}
			patch["type"] = raw(f.types[0])
		}
		return patch, nil
	}
	patch := dns.RecordPatch{}
	if action == "create" {
		if len(f.types) != 1 {
			return nil, fmt.Errorf("创建需要 --type 或 --file")
		}
		patch["type"] = raw(f.types[0])
	}
	if changed("content") && changed("content-file") {
		return nil, fmt.Errorf("content 与 content-file 互斥")
	}
	if changed("content") {
		patch["content"] = raw(f.content)
	}
	if changed("content-file") {
		data, err := fileio.ReadRegularFile(f.contentFile, 1<<20)
		if err != nil {
			return nil, err
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("content-file 必须是 UTF-8 文本，未替换非法字节")
		}
		patch["content"] = raw(string(data))
	}
	if changed("ttl") {
		if f.ttl == "auto" {
			patch["ttl"] = raw(1)
		} else {
			patch["ttl"] = json.RawMessage(f.ttl)
			if !json.Valid(patch["ttl"]) {
				return nil, fmt.Errorf("ttl 应为 auto 或整数秒")
			}
		}
	}
	if changed("proxied") {
		patch["proxied"] = raw(f.proxied)
	}
	if changed("comment") {
		patch["comment"] = raw(f.comment)
	}
	if changed("tag") && f.clearTags {
		return nil, fmt.Errorf("tag 与 clear-tags 互斥")
	}
	if changed("tag") {
		patch["tags"] = raw(f.tags)
	} else if f.clearTags {
		patch["tags"] = raw([]string{})
	}
	if changed("new-name") {
		patch["name"] = raw(f.newName)
	}
	data := map[string]any{}
	for key, value := range map[string]any{"target": f.target, "weight": f.weight, "port": f.port} {
		if changed(key) {
			data[key] = value
		}
	}
	if changed("priority") {
		if len(f.types) == 1 && f.types[0] == "MX" {
			patch["priority"] = raw(f.priority)
		} else {
			data["priority"] = f.priority
		}
	}
	if changed("params") {
		data["value"] = f.params
	}
	if changed("caa-flags") {
		data["flags"] = f.caaFlags
	}
	if changed("caa-tag") {
		data["tag"] = f.caaTag
	}
	if changed("caa-value") {
		if changed("params") {
			return nil, fmt.Errorf("params 与 caa-value 互斥")
		}
		data["value"] = f.caaValue
	}
	if len(data) > 0 {
		patch["data"] = raw(data)
	}
	return patch, nil
}

func validateRecordFlags(cmd *cobra.Command, typ string) error {
	for _, rule := range []struct {
		flag  string
		types string
	}{
		{"target", " SRV HTTPS SVCB "}, {"params", " HTTPS SVCB "},
		{"priority", " MX SRV HTTPS SVCB "}, {"weight", " SRV "}, {"port", " SRV "},
		{"caa-flags", " CAA "}, {"caa-tag", " CAA "}, {"caa-value", " CAA "},
	} {
		if cmd.Flags().Changed(rule.flag) && !strings.Contains(rule.types, " "+typ+" ") {
			return usage(fmt.Sprintf("--%s 不适用于 %s 记录", rule.flag, typ))
		}
	}
	return nil
}
