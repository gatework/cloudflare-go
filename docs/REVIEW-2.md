# 第二轮代码审查

基线 `f38c7d7`，日期 2026-10-31。重新检查 CLI、领域规则、凭据、配置、SDK 传输、导出、文件发布和 CI/Release。针对新增发现实现修复；上轮已修复的问题继续由原回归覆盖。本轮没有执行生产 DNS 写入。

## 发现与处理

| 优先级 | 触发条件与原行为 | 修复及验证 |
|---|---|---|
| 高 | 写请求响应丢失，随后 GET 收到代理或路由 404；通用 HTTP 404 被当作记录不存在，删除可能误报成功 | 仅有效 Cloudflare 错误结构且 HTTP 404/code 81044 才能确认记录消失；真实 SDK 假服务覆盖 HTML、路由错误、缺失 success 与明确不存在四种结果，确认只写一次 |
| 高 | `--config` 拼错路径时加载空配置，可能使用环境 Token；`config check` 对不存在的文件仍返回有效 | `Load` 必须存在；仅隐式默认配置和初始化使用 `LoadOptional`，失效符号链接不回退。完整 CLI 回归验证没有写请求 |
| 高 | 打包从工作目录复制 docs/examples，并用传入 commit 标识当前工作树 | 构建、文档和示例均来自指定提交的独立快照，要求 HEAD 一致且已跟踪文件干净，禁止快照链接；未跟踪和被忽略的文件不会进入包，测试包含模拟 token 文件 |
| 中 | 所有 data.flags 都按 8 位数字处理，DNSKEY 257 和 NAPTR "S" 被拒绝；URI 的 URL 被当作主机名 | 校验规则按记录类型区分；DNSKEY、NAPTR、URI 经完整 CLI → SDK 写入假 API 回归；URI 的路径、大小写和 query 保留 |
| 中 | CAA 参数可被用于修改 HTTPS 的 value；TXT 的顶层 priority 和 SRV 的未知 data 字段可能被忽略 | 类型专用 CLI 参数核对实际记录类型，常用 data 结构拒绝错误字段；先校验输入再合并已有 data/settings，保留未来未知响应字段 |
| 中 | `--id ''`、`--type ''`、空凭据来源或零超时悄悄使用默认值 | 明确拒绝，避免脚本空变量改变目标或认证身份；空 TXT 内容与清除 comment 仍是合法操作 |
| 中 | `content-file` 内非法 UTF-8 被 JSON 编码器替换后写入 | 内容文件和文本参数先验证 UTF-8，凭据也拒绝非法编码；回归确认错误时写入次数为零 |
| 中 | owner 中间下划线、纯下划线标签、IDNA Unicode 点号被拒绝；IPv6 等价写法未规范化 | 区分记录 owner 和 hostname 规则；支持 ASCII 下划线、映射 Unicode 分隔符、规范化 IPv6；新增稳定性与非法输入测试 |
| 中 | HOME/XDG 不存在时连 version/help 或显式配置也无法使用 | 默认目录解析延后到需要配置的命令，无目录时仍可使用 help/version/--config |
| 中 | 非可运行父命令对未知子命令显示帮助并返回成功，脚本可能误判执行结果 | 给命令组设置明确参数校验和帮助动作；四个组的拼写错误均返回退出码 2 |
| 性能 | HTTP envelope 校验将每页 result 再完整复制一次，随后 SDK 又解码同一页 | 仅保存 result 的数组/对象类型标记，仍用 encoding/json 验证完整响应；保持 16 MiB 响应边界和原 context 生命周期 |

## Go 实现取舍

- 名称规范化、通用补丁与类型 data 校验分别放在 `name.go`、`validation.go`、`data.go`，避免给同名但不同含义的字段套一套规则。
- 选择显式 `Load`/`LoadOptional` 表达配置缺失语义，不通过捕获任意读取错误来兜底到其他身份。
- 传输只保留校验所需的轻量信息，不绕过官方 SDK，不使用 unsafe，不引入缓存或后台 goroutine。
- 变更准备先校验调用方输入，再合并原记录字段；用户错误和 API 未来扩展分别处理。
- API 失败分类保留 `errors.Is`/`errors.As`，明确失败与不确定结果各有含义。请求 ID 按引用字符串输出，避免诊断包含终端控制字符。

类型规则对照 [Cloudflare Create DNS Record](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/create/) 和已锁定 SDK v7.13.1；URI 目标语义参照 [RFC 7553](https://datatracker.ietf.org/doc/html/rfc7553#section-4.4)。记录不存在的 81044 错误可见 [Cloudflare 官方 provider 的问题记录](https://github.com/cloudflare/terraform-provider-cloudflare/issues/1621)。本地校验仅覆盖明确约束，套餐限制及其他高级字段仍由 Cloudflare 决定。

## 传输基准

同一 Linux amd64 主机、Go 1.27.3、`GOMAXPROCS=2`、`GOMEMLIMIT=160MiB`。每页 100 条 TXT，每条 1 KiB 内容；每次 500ms，重复 5 次取中位数。只测已经在内存中的 HTTP 响应校验，不含网络、SDK 记录解码、排序或导出。

| 指标 | 基线 | 改进版 | 变化 |
|---|---:|---:|---:|
| 每页校验耗时 | 357,860 ns | 327,987 ns | -8.3% |
| 每页分配字节 | 417,496 B | 294,580 B | -29.4% |
| 每页分配次数 | 30 | 29 | -3.3% |

原始结果：[基线](benchmarks/transport-before.txt)、[改进版](benchmarks/transport-after.txt)。复现命令：

```sh
GOMAXPROCS=2 GOMEMLIMIT=160MiB go test ./internal/cloudflare \
  -run '^$' -bench BenchmarkResponseValidation -benchmem -benchtime=500ms -count=5
```

## 验证与边界

新增实际 SDK 写后核对、并发请求元数据隔离、合法高级记录 CLI、配置路径错误、非 UTF-8 和补丁稳定性测试；CI 增加 `FuzzRecordPatch` 和传输基准，Python 发布测试从 19 项增至 21 项。四个平台继续原生运行 Go 测试，并执行打包后的同一二进制；Linux amd64 检查归档逐字节复现。

高级记录与错误分支使用假 API，不能替代 Cloudflare 测试 Zone 的实际验收。分页仍不是事务快照，写前检查仍不是服务端 CAS；结果不确定时仍不自动重放写请求。对 API 未明确记录的错误码采用保守失败，不假定所有 404 都表示目标已删除。
