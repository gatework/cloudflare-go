# 第三轮渐进式审查与改进

基线 `3b4cb77`，2026-10-31。按正确性、收集与导出、初始化体验三个阶段实现；测试使用本地假 API，没有操作生产 DNS。

## 正确性与资源管理

| 发现 | 修复与验证 |
|---|---|
| `/export` 绕过普通响应检查，SDK 可把代理 HTML 当作成功的区域文件 | 成功 BIND 响应要求 HTTP 200、text/plain、有效 UTF-8 和非空文本，拒绝 NUL、明显 HTML/JSON；不改写合法文本。错误或超过预算时不替换输出文件 |
| 编码和发布不观察取消，查询完成后按 Ctrl+C 仍可能输出全部内容 | 输出接收 context，在每次编码写入、缓冲刷新和发布前检查；配置更新也检查取消。取消发生在发布前时保留旧文件并清理临时文件 |
| count 不符、跨页统计改变或最终缺少 ID 时仍可能导出成功 | 分页字段用指针区分缺失与零，核对每页 count、稳定的 total_count/total_pages 和最终不同 ID 数量；无统计时保留前进及空页检查 |
| 标签数组中的 null 被 Go 解码为零值，空位置参数可能回退到默认目标 | 拒绝 null 标签；连接之前检查本地参数、ID、预算、格式及互斥组合 |
| IPv6/主机名等价写法、服务器增加对象成员导致写后核对误判不确定 | 完整快照比较仍严格；补丁匹配只对已定义的 IP/主机名应用等价规则，并允许 data/settings 返回额外成员。TXT、URI 路径及未知字段继续严格比较 |

新增用例先在旧实现复现失败，再实施修复。原去重测试声明 total_count=4，却只返回 3 个不同 ID；已改正 fixture，另有独立的缺失记录失败用例。

输出取消是合作式检查：不会创建一个可能遗留的 goroutine 来中断调用方 Writer。已经进入 OS 的阻塞写入或同步操作，需要其返回后才能观察取消；检查与最终发布之间仍可能存在竞态。DNS 已成功但结果输出失败时保留明确提示，写入不确定仍优先返回 5。

## 查询、导出与内存取舍

- 保留完整收集后排序；匹配记录的去重状态共享原始 JSON，不复制；未匹配记录只保留 ID 与规范化 JSON 的 SHA-256。规范化保留 json.Number，忽略对象键顺序，保留未知字段变化检测。
- Zone 列表也应用累计原始数据预算；BIND 输出检查 --max-bytes，传输仍有原有的单响应 16 MiB 上限。预算不是进程 RSS 限制。
- `dns list/get/dump --format jsonl` 输出每行一个原始记录，保留未知字段、数字精度和转义文本；空集合为零字节，无元数据行。完整导出元数据仍使用 JSON。
- 支持 `.jsonl/.ndjson` 推断格式，以及 `--match-tag`（重复值 AND）和 `--match-comment`。API 先筛选可表达的条件，再本地按大小写精确核对；写操作不增加这些参数。

### 基准方法与结果

Go 1.27.3、Linux amd64、GOMAXPROCS=2、GOMEMLIMIT=160MiB。分页 fixture 在计时前构建，不含真实网络、磁盘或 Cloudflare 限流。完整链路包含官方 SDK 解码、筛选、去重、排序和最终 JSON 编码。

| 指标 | 改进前 | 改进后 | 变化 |
|---|---:|---:|---:|
| 10,000 条、1% 匹配，在最后一页前采样的保留堆增量，中位数 | 13,702,248 B | 1,685,272 B | -87.7% |
| 1,000 条、1% 匹配，完整链路累计分配量/次，中位数 | 157,151,729 B | 165,389,766 B | +5.2% |
| 同上完整链路耗时，中位数 | 329.7 ms | 377.8 ms | +14.6% |

堆采样前执行 GC，五次取中位数；这是分页进行期间的保留堆增量，不是峰值或 RSS。吞吐复测采用每样本固定 5 次操作、五个样本，SDK 生成类型的解码占据大量累计分配。摘要需要额外解析，因此此次优化明确以额外 CPU/累计分配换取更低的保留内存，不声称整体加速。共享生产主机的时间波动仍需在同样资源条件下解释。

原始数据：[初次基线](benchmarks/collection-before.txt)、[初次改进](benchmarks/collection-after.txt)、[固定次数基线](benchmarks/collection-before-fixed.txt)、[固定次数改进](benchmarks/collection-after-fixed.txt)。初次计时每次 200ms、五个样本，单次操作已超过预算，故另做固定次数复测；不能只凭局部编码测试推断端到端收益。

```sh
GOMAXPROCS=2 GOMEMLIMIT=160MiB go test ./internal/cloudflare \
  -run '^TestCollectionRetainedHeap$' -v -count=5
GOMAXPROCS=2 GOMEMLIMIT=160MiB go test ./internal/cloudflare \
  -run '^$' -bench '^BenchmarkCollection/filtered=true$' \
  -benchmem -benchtime=5x -count=5
```

## 初始化和日常使用

- 把初始化与 profile 管理分开；交互 init 在线选择默认 Zone，并只读验证 DNS 访问。同名 Zone 的编号选择保存必要的 account_id；已有默认值可回车保留，也可跳过。
- `--offline` 跳过联网；脚本默认保持离线，`--check` 要求验证后才能保存，二者互斥。默认在线引导失败后必须明确确认才离线保存；配置仍在锁内检查同一 profile 的并发变化。
- `profile check [NAME]` 提供 credentials/zones/dns 阶段结果，支持 JSON/CSV/txt/table。无目标 Zone 时 DNS 为 skipped；DNS 检查只请求一页、一条记录，不尝试写入、不推断写权限。
- Bash/Zsh/Fish 补全只使用命令定义和本地配置；不需要 HOME，不读取 Token、不访问远端。记录类型维护单一来源，格式补全按命令能力提供。

## 验证与保留边界

回归覆盖错误 BIND 文档及超限输出、分页统计与重复 ID、原始 JSON 保真、输出/配置取消、标签及注释筛选、零/单/多个 Zone、同名账号歧义、已有默认值、在线失败、离线确认、脚本检查与补全。

本地运行 race、vet、Staticcheck、govulncheck、四类输入 fuzz、21 项 Python 发布脚本测试及 actionlint。CI 继续在四个平台原生测试和执行发行包，Linux amd64 重建检查归档可复现；增加完整收集基准和 JSONL 编码基准。

分页统计检查可发现更多不完整或并发变化，但不能将 API 分页升级为事务快照。写前比较仍不是服务端 CAS；未引入并行分页、批量写入或自动写重试。真实 API 写入验收仍需单独启用专用测试 Zone。
