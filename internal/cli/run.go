// Package cli is a thin, testable command boundary. Libraries never exit.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gatework/cloudflare-go/internal/cloudflare"
	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/credentials"
	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/gatework/cloudflare-go/internal/fileio"
	"github.com/gatework/cloudflare-go/internal/output"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var Version = "dev"
var Commit = "unknown"

type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }
func usage(text string) error       { return &usageError{errors.New(text)} }

type app struct {
	in                                                         io.Reader
	out, err                                                   io.Writer
	interactive                                                bool
	explicitConfig                                             bool
	path, profile, tokenFile, zoneName, zoneID, format, output string
	timeout                                                    time.Duration
	overwrite, spreadsheet                                     bool
	maxBytes                                                   int64
	factory                                                    func(string) (dns.API, func())
}

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	// Help/version and an explicit --config remain usable without HOME/XDG.
	path, _ := config.DefaultPath()
	a := &app{in: in, out: out, err: errOut, path: path}
	if f, ok := in.(*os.File); ok {
		a.interactive = term.IsTerminal(int(f.Fd()))
	}
	a.factory = func(token string) (dns.API, func()) { c := cloudflare.New(token, "", nil); return c, c.Close }
	return a.execute(ctx, args)
}
func (a *app) execute(ctx context.Context, args []string) int {
	root := a.command()
	root.SetArgs(args)
	root.SetIn(a.in)
	root.SetOut(a.out)
	root.SetErr(a.err)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	_, _ = fmt.Fprintln(a.err, err)
	// Cancellation after sending a mutation cannot establish that it failed.
	if errors.Is(err, dns.ErrUncertain) {
		return 5
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return 130
	}
	var bad *usageError
	switch {
	case errors.Is(err, dns.ErrInvalid), errors.As(err, &bad), strings.HasPrefix(err.Error(), "unknown command"), strings.Contains(err.Error(), "arg(s)"):
		return 2
	case errors.Is(err, dns.ErrConflict):
		return 4
	case errors.Is(err, dns.ErrNotFound):
		return 3
	}
	return 1
}
func (a *app) command() *cobra.Command {
	root := &cobra.Command{Use: "cloudflare-go", Short: "Cloudflare DNS 记录管理与完整导出", SilenceErrors: true, SilenceUsage: true, RunE: func(c *cobra.Command, _ []string) error { return c.Help() }}
	root.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		if err := a.preflight(cmd, args); err != nil {
			return err
		}
		a.explicitConfig = cmd.Flags().Changed("config")
		if a.path == "" && !cmd.HasSubCommands() && cmd.Name() != "version" {
			return usage("无法定位配置文件，请通过 --config 指定路径")
		}
		if a.overwrite && a.output == "" {
			return usage("overwrite 需要同时指定 output")
		}
		for _, name := range []string{"config", "profile", "token-file", "zone", "zone-id", "format", "output"} {
			if cmd.Flags().Changed(name) {
				value, _ := cmd.Flags().GetString(name)
				if value == "" {
					return usage("--" + name + " 不能为空")
				}
			}
		}
		return nil
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &usageError{err} })
	f := root.PersistentFlags()
	f.StringVar(&a.path, "config", a.path, "配置文件")
	f.StringVar(&a.profile, "profile", "", "命名配置（也可设置 CLOUDFLARE_PROFILE）")
	f.StringVar(&a.tokenFile, "token-file", "", "显式 Token 文件，覆盖所选配置的凭据来源")
	f.StringVar(&a.zoneName, "zone", "", "Zone 域名，用于简写或消除歧义")
	f.StringVar(&a.zoneID, "zone-id", "", "明确的 Zone ID")
	f.StringVar(&a.format, "format", "", "table、txt、csv、json；DNS 查询另支持 jsonl，整 Zone dump 支持 bind")
	f.StringVarP(&a.output, "output", "o", "", "输出到文件，默认不覆盖")
	f.BoolVar(&a.overwrite, "overwrite", false, "允许原子替换已有输出文件")
	f.BoolVar(&a.spreadsheet, "spreadsheet-safe", false, "CSV 对公式前缀增加单引号，显式改变单元格值")
	f.DurationVar(&a.timeout, "timeout", 0, "远端操作总预算，默认 2m，dump 为 10m")
	f.Int64Var(&a.maxBytes, "max-bytes", 256<<20, "记录收集字节上限，超过时失败，不截断")
	root.AddCommand(&cobra.Command{Use: "version", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		_, err := fmt.Fprintf(a.out, "cloudflare-go %s (%s), cloudflare/cloudflare-go v7.13.1\n", Version, Commit)
		return err
	}})
	a.addProfiles(root)
	a.addZones(root)
	a.addDNS(root)
	return root
}

func commandGroup(name, description string) *cobra.Command {
	// Cobra's non-runnable parent commands otherwise display help and return
	// success even for an unknown subcommand (e.g. "dns udpate").
	return &cobra.Command{Use: name, Short: description, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() }}
}
func (a *app) settings() (config.Config, config.Profile, error) {
	c, err := a.loadConfig(false)
	if err != nil {
		return c, config.Profile{}, &usageError{err}
	}
	name := a.profile
	if name == "" {
		name = os.Getenv("CLOUDFLARE_PROFILE")
	}
	p, err := c.Select(name, filepath.Dir(a.path))
	if err != nil {
		return c, p, &usageError{err}
	}
	return c, p, nil
}

func (a *app) loadConfig(required bool) (config.Config, error) {
	load := config.LoadOptional
	if required || a.explicitConfig {
		load = config.Load
	}
	c, err := load(a.path)
	if err != nil {
		return c, &usageError{err}
	}
	return c, nil
}
func (a *app) connect() (dns.API, func(), config.Profile, error) {
	_, p, err := a.settings()
	if err != nil {
		return nil, nil, p, err
	}
	source := p.TokenFile
	environment := p.TokenEnv
	if a.tokenFile != "" {
		source = a.tokenFile
		environment = ""
	}
	token, err := credentials.Read(source, environment)
	if err != nil {
		return nil, nil, p, &usageError{err}
	}
	api, close := a.factory(token)
	return api, close, p, nil
}
func (a *app) context(c *cobra.Command, dump bool) (context.Context, context.CancelFunc, error) {
	duration := a.timeout
	if !c.Flags().Changed("timeout") {
		duration = 2 * time.Minute
		if dump {
			duration = 10 * time.Minute
		}
	}
	if duration < time.Millisecond || a.maxBytes < 1 {
		return nil, nil, usage("timeout 和 max-bytes 必须大于零")
	}
	ctx, cancel := context.WithTimeout(c.Context(), duration)
	return ctx, cancel, nil
}
func (a *app) formatFor(dump bool, p config.Profile) string {
	format := a.format
	if format == "" && a.output != "" {
		format = map[string]string{".json": "json", ".jsonl": "jsonl", ".ndjson": "jsonl", ".csv": "csv", ".txt": "txt", ".zone": "bind"}[strings.ToLower(filepath.Ext(a.output))]
	}
	if format == "" {
		if dump {
			format = "json"
		} else if p.Format != "" {
			format = p.Format
		} else {
			format = "json"
			if f, ok := a.out.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
				format = "table"
			}
		}
	}
	return format
}
func (a *app) render(ctx context.Context, value any, dump bool, p config.Profile) error {
	format := a.formatFor(dump, p)
	if !output.Valid(format) {
		return usage("format 应为 table、txt、csv、json 或 bind")
	}
	if a.spreadsheet && format != "csv" {
		return usage("spreadsheet-safe 只用于 CSV")
	}
	encode := func(w io.Writer) error { return output.RenderContext(ctx, w, format, value, a.spreadsheet) }
	if a.output == "" {
		return encode(a.out)
	}
	destination, err := fileio.NewOutput(a.output, a.overwrite)
	if err != nil {
		return err
	}
	defer destination.Close()
	if err := encode(destination); err != nil {
		return err
	}
	return destination.CommitContext(ctx)
}
func (a *app) confirm(ctx context.Context, text string) error {
	if !a.interactive {
		return usage("非交互删除需要 --yes；可先使用 --dry-run")
	}
	if _, err := fmt.Fprint(a.err, text+" [y/N] "); err != nil {
		return err
	}
	answer, err := readLine(ctx, a.in)
	if err != nil {
		return err
	}
	if !strings.EqualFold(answer, "y") {
		return usage("操作已取消")
	}
	return nil
}
func (a *app) validateOutput() error {
	if a.format != "" && !output.Valid(a.format) {
		return usage("未知输出格式")
	}
	if a.output != "" {
		if info, err := os.Lstat(a.output); err == nil {
			if !info.Mode().IsRegular() {
				return usage("输出目标必须是普通文件")
			}
			if !a.overwrite {
				return usage("输出文件已存在，使用 --overwrite 显式覆盖")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	return nil
}
func raw(value any) json.RawMessage { b, _ := json.Marshal(value); return b }
