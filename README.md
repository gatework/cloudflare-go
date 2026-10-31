# cloudflare-go

面向日常运维的 Cloudflare DNS 命令行工具。Go 单文件程序，支持命名配置、单条记录 CRUD、完整域名 dump，以及 table、txt、csv、json 和整 Zone 的 BIND 导出。

这是 gatework 的独立项目，不是 Cloudflare 官方 CLI。底层使用官方 `cloudflare/cloudflare-go/v7 v7.13.1`，不需要 Node.js，也不安装后台服务。组织与错误处理方式参考 `gatework/derper-go`。

## 开始使用

从 [Releases](https://github.com/gatework/cloudflare-go/releases) 下载 Linux/macOS、amd64/arm64 对应归档与 `checksums.txt`，校验后解压：

```sh
sha256sum --ignore-missing -c checksums.txt
# macOS 可用：shasum -a 256 --check checksums.txt
# 只下载一个平台时，仅核对该归档对应的那一行。
tar -xzf cloudflare-go_v0.1.1_linux_amd64.tar.gz
cd cloudflare-go_v0.1.1_linux_amd64
./cloudflare-go version
./cloudflare-go init --profile personal
```

`init` 隐藏输入 Token，写入独立的 0600 凭据文件。也可直接引用已有文件或环境变量，不要求初始化：

```sh
cloudflare-go init --profile personal --token-file /secure/cloudflare.token
cloudflare-go --token-file /secure/cloudflare.token dns list example.com
```

只读权限需要所选 Zone 的 **Zone Read + DNS Read**；创建、修改和删除另需 **DNS Write**。不要将 Token 作为普通命令行参数；工具不支持 `--token VALUE`。macOS 发行包未经 Apple 公证。

## 查询与导出

```sh
cloudflare-go zone list
cloudflare-go zone get example.com
cloudflare-go dns list example.com
cloudflare-go dns list app.example.com --exact
cloudflare-go dns get app.example.com --type A
cloudflare-go dns get --zone example.com --id RECORD_ID

cloudflare-go dns dump example.com --output records.json
cloudflare-go dns dump example.com --format csv --output records.csv
cloudflare-go dns dump app.example.com --format txt
cloudflare-go dns dump app.example.com --exact --type TXT --format json
cloudflare-go dns dump example.com --format bind --output example.com.zone
```

list/dump 默认包括输入名称及其子域，`--exact` 仅匹配该名称。范围限于选定 Zone；不跨独立托管的子 Zone，不扫描公网 DNS。名称比较使用标签边界，不会把 `notexample.com` 当作 `example.com` 的子域。

JSON 保留 API 原始记录，包括未知字段、空值、`data/settings/meta/tags` 和时间戳。CSV 包含完整 `record_json` 列；txt 展示完整键值详情。table 仅为简表，可截断长内容；table/txt 会转义终端控制字符。CSV 默认不改变真实值，`--spreadsheet-safe` 显式给公式前缀增加单引号。

DNS CSV 的 `record_json` 保留原始记录，不混入导出的 Zone 元数据；Zone、profile 和变更预览的 CSV 使用各自实际字段。txt/CSV 将 JSON null 显示为 `null`，与空字符串区分。

BIND 调用官方整 Zone 导出接口，不能搭配记录筛选。它保留 Cloudflare 的区域文件内容，但不能替代带 ID、时间戳等详情的 JSON 导出。

普通查询在终端默认 table，管道默认 JSON；dump 默认 JSON。显式 `--format` 优先，否则输出文件的 `.json/.csv/.txt/.zone` 扩展名可决定格式。stdout 只输出数据，诊断与确认写 stderr。

导出分页失败时不输出半份结果。文件在完整编码后原子发布，默认 0600、不覆盖已有文件；显式 `--overwrite` 才替换旧文件。Cloudflare 分页不提供事务快照保证：导出期间记录变化可能导致不同时间点的数据。

分页收集完成后逐条编码，避免复制整份导出数据。`--max-bytes` 限制累计原始记录字节数，并非进程内存上限。大量记录的 table 每 256 行刷新，列宽在各块内对齐。

## 单条记录管理

```sh
cloudflare-go dns create app.example.com --type A --content 192.0.2.10
cloudflare-go dns update app.example.com --type A --content 192.0.2.20 --ttl 300
cloudflare-go dns update app.example.com --id RECORD_ID --proxied=false
cloudflare-go dns update app.example.com --type A --new-name service.example.com
cloudflare-go dns delete app.example.com --id RECORD_ID --dry-run
cloudflare-go dns delete app.example.com --id RECORD_ID --yes

cloudflare-go dns create example.com --type MX --content mail.example.com --priority 10
cloudflare-go dns create example.com --type CAA --caa-flags 0 --caa-tag issue --caa-value letsencrypt.org
cloudflare-go dns create _sip._tcp.example.com --type SRV --priority 10 --weight 5 --port 5060 --target sip.example.com
cloudflare-go dns create app.example.com --type HTTPS --priority 1 --target . --params 'alpn="h2,h3"'
cloudflare-go dns create _verification.example.com --type TXT --content-file verification.txt
```

create/update 直接执行；delete 在交互终端确认，非交互环境必须 `--yes`。全部写操作支持 `--dry-run`。同名多记录不会自动选第一条，需要 `--id` 或 `--type/--match-content` 消除歧义。记录类型不支持原地转换。

update 只提交显式字段；嵌套 data/settings 合并原记录，保留未指定成员。`--comment ''` 清空注释，`--clear-tags` 清空标签，指定 `--tag` 替换完整标签列表。TTL 创建默认 auto，`--ttl auto` 映射 API 数值 1；普通 TTL 为秒数，30 秒是否可用取决于 Cloudflare 套餐。

高级类型可通过单记录 JSON 文件创建或修改；不支持批量导入/恢复：

```sh
cloudflare-go dns create _tls.example.com --file tls-record.json
cloudflare-go dns update _tls.example.com --id RECORD_ID --file patch.json
```

文件只接受 `name/type/content/ttl/proxied/priority/comment/tags/data/settings/private_routing`，拒绝只读和未知顶层字段、重复键及顶层 null。`--file` 不与内容参数混用。高级 data/settings 字段由 Cloudflare 校验。

写请求不自动重试。连接中断可能发生在服务端已经写入之后；工具进行只读核对，无法确认时返回退出码 5。不要把该结果理解为“肯定没有写入”。确认后重新读取能发现部分并发变化，但不提供服务端原子比较交换。

写入后取消且无法核对结果时仍返回 5；发起写入前取消返回 130。指定输出文件时先创建临时文件，提前检查路径和权限；DNS 已成功但随后输出失败时会明确提示已发生变更。

## 配置与自动化

见 [配置参考](docs/CONFIGURATION.md)、[测试与发布](docs/VALIDATION.md)、[代码审查与基准](docs/REVIEW.md)、[开源调研](docs/RESEARCH.md) 和 [变更记录](CHANGELOG.md)。

```sh
cloudflare-go init --profile work --token-env CLOUDFLARE_WORK_TOKEN --default-zone example.org
cloudflare-go profile list
cloudflare-go profile use work
cloudflare-go profile show
cloudflare-go --profile personal dns list example.com --format json
cloudflare-go config show
cloudflare-go config check
```

退出码：0 成功；1 运行/API/输出失败；2 用法或配置错误；3 未找到；4 歧义或冲突；5 写入结果不确定；130 主动取消。

本地构建需要 Go 1.27.3 与 Python 3.11+：`make build`、`make test`、`make race`。程序不会管理 Zone 生命周期、修改现有服务、更新其他项目配置或在普通 CI 中操作生产 DNS。
