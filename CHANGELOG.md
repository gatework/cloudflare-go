# 变更记录

## Unreleased

- 逐条编码 DNS 导出、复用输出缓冲区，避免重建整份 JSON/map 数据集；简表按有限行数刷新。为导出增加可复现基准与 CI 基准附件。
- 将单类型、代理状态和内容筛选传给 API，同时保留本地精确匹配；减小分页去重索引并减少重复 JSON 编解码。
- 修复 HTTP 连接池清理、重试间错误元数据残留、提前空页被误当作导出成功；校验响应结构及返回的 Zone/记录 ID。
- 修复写入后取消被误报为普通取消；写入前预建原子输出文件，写入成功后输出失败时明确报告 DNS 已变更。
- 修复 Zone/profile/dry-run/delete 的 CSV 字段丢失；record_json 保持原记录，null 与空字符串分别显示。
- 修复显式 Zone ID 受默认 Zone 干扰、重复 Zone 查询、init 相对 Token 路径、profile 环境选择及并发配置覆盖。
- 准备变更时复制调用方补丁；拒绝结构化数值/字符串的 null；JSON 配置严格匹配字段名称并限制嵌套深度，profile 名称仍区分大小写。
- 按传输、区域、记录、选择和变更拆分实现，统一 Go 导入组织并补齐失败路径回归。

## v0.1.1

- 修复 list/dump 对下划线服务记录及通配符所有者名称的处理；Zone 发现跳过不可能的服务标签，避免将其当作 Zone 查询。
- 增加 ACME TXT、SRV 名称及通配符记录的完整 CLI 导出回归。

## v0.1.0

- 首个版本：Cloudflare DNS 单条增删改查、Zone 发现与查询。
- 支持命名 profile、Token 文件或环境变量，配置不保存凭据明文。
- 域名及子域完整 dump；table/txt/csv/json，以及整 Zone BIND 导出。
- 原始 JSON 和未知字段保留、原子文件发布、精确记录选择、dry-run。
- 写请求不自动重试；处理并发变化和写入结果不确定。
- Linux/macOS amd64/arm64 原生测试、可复现 Linux 归档与自动 Release。
