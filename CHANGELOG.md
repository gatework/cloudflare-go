# 变更记录

## v0.1.0

- 首个版本：Cloudflare DNS 单条增删改查、Zone 发现与查询。
- 支持命名 profile、Token 文件或环境变量，配置不保存凭据明文。
- 域名及子域完整 dump；table/txt/csv/json，以及整 Zone BIND 导出。
- 原始 JSON 和未知字段保留、原子文件发布、精确记录选择、dry-run。
- 写请求不自动重试；处理并发变化和写入结果不确定。
- Linux/macOS amd64/arm64 原生测试、可复现 Linux 归档与自动 Release。
