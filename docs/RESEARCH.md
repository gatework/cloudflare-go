# 开源调研与选型

核查日期：2026-10-31。所有项目独立保留其许可证；本项目没有复制第三方 CLI。

- 官方 SDK：https://github.com/cloudflare/cloudflare-go ，采用 v7.13.1（Apache-2.0）。提供完整 DNS/Zone 接口与原始响应 JSON。只实例化 DNS/Zone 服务，避免根客户端引入全部产品；不使用 SDK 自动分页，后续页显式传递原 context；写请求覆盖默认重试为 0。
- 官方 CLI：https://github.com/cloudflare/cf ，TypeScript/Node.js、Beta，默认 JSON，覆盖整个公共 API。本项目借鉴 stdout/stderr 分离，目标是独立 Go 二进制与 DNS 领域友好参数。
- https://github.com/btafoya/cfdns （MIT），基础 Go CLI，支持 A/AAAA/CNAME/TXT 和 JSON。参考其直接 CRUD 的使用方式，未复制实现。
- https://github.com/danielpigott/cloudflare-cli （MIT），JavaScript CLI，作为已有方案对照，不引入运行依赖。
- https://github.com/libdns/cloudflare ，通用 Provider 抽象更适合跨 DNS 服务商；本项目需要保留 Cloudflare 专有记录元数据，直接采用官方 SDK。
- https://github.com/DNSControl/dnscontrol ，面向声明式多提供商同步，超出本轮单条 CRUD 与导出范围。
- https://github.com/gatework/derper-go ，参考领域模块组织、context 生命周期、凭据保护、错误脱敏、原子文件写入和发行流程。文件读取与终端逻辑沿用其 MIT 实现。

API 参考：

- https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/
- https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/edit/
- https://developers.cloudflare.com/dns/manage-dns-records/how-to/import-and-export/
- https://developers.cloudflare.com/fundamentals/api/reference/permissions/
