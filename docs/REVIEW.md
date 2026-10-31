# 代码审查与改进记录

2026-10-31，以 v0.1.1（`ffcc521`）为基线。审查覆盖 CLI、凭据、配置、领域逻辑、官方 SDK 适配、文件发布、导出和 CI。测试均使用本地假 API，未操作生产 DNS。

## 修复的风险

| 路径 | 原有风险 | 改进与回归 |
|---|---|---|
| 导出 | 整份结果反复 Marshal/Unmarshal；表格保留整个导出缓冲 | 完整分页后逐条编码、32 KiB 写缓冲、简表每 256 行刷新；验证多行 Unicode、未知字段、大整数和写失败 |
| CSV | 非 DNS 对象被套入记录列，丢失 Zone/profile/变更信息；record_json 被混入 Zone 元数据 | 按数据种类选择列，原样保存记录 JSON，区分 null/空字符串 |
| HTTP | 包装 Transport 未转发连接池清理；重试可能携带上次错误的状态和请求 ID | 转发 CloseIdleConnections，每次尝试重置元数据；真实 SDK 假服务验证 |
| API 完整性 | 提前空页结束导出；合法 JSON 的错误对象/ID 可能被当成功 | 校验分页完整性、结果数组/对象形状和目标 ID；写请求依旧只发送一次 |
| 查询 | 类型/代理/内容仅在本地筛选；明确 Zone ID 仍重复读取且受默认域名干扰 | 下推 API 支持的 AND 条件并保留本地精确检查；显式 ID 覆盖默认 Zone、只读一次详情 |
| 写入状态 | 写请求后取消被通用退出码 130 覆盖 | 结果未确认优先返回 5；写入前取消不发请求，返回 130 |
| 本地输出 | DNS 已写入才发现输出目录不存在 | 写入前预建临时文件；后续磁盘/管道失败明确提示 DNS 已成功；并发创建和符号链接替换回归 |
| 初始化 | 同一 profile 的并发修改被旧快照覆盖；相对 Token 路径基准不一致 | 锁内核对已读取的 profile、冲突时保留新配置；明确命令参数与配置内路径的不同基准 |
| 凭据生命周期 | 配置已发布但目录同步失败时，清理逻辑可能删除配置引用的新 Token | 区分发布前错误和 PublishedError；后者保留已引用凭据 |
| 数据所有权 | 准备变更直接改写调用方 map；结构化 null 可被解码为零值 | 复制补丁及 RawMessage；校验显式 null；JSON 比较保留数字精度且拒绝尾随数据 |
| 配置解析 | 通用 JSON 重复键检测不当折叠大小写，配置字段又被 Go 隐式忽略大小写 | JSON 键按原值检测重复，配置字段严格检查，命名 profile 区分大小写；限定嵌套深度 |

## Go 模块组织

- `internal/cloudflare` 将 SDK 构造、HTTP 传输、Zone 和记录操作拆开；生成类型继续封装在适配层。
- `internal/dns` 分离模型、选择、变更和 JSON 比较。context 仍逐次传递，不存入长期存活对象，不添加后台 goroutine。
- `internal/output` 对 DNS 数据使用明确类型，其他少量对象使用通用列；避免整份数据转成通用 map 后再解释。
- `internal/fileio.Output` 显式拥有临时文件，调用方负责 Close，Commit 只发布完整文件；原有 Publish 使用同一实现。
- 不引入新的运行依赖、自动写重试、并行分页或全局可变缓存。

传输生命周期依据 [Go HTTP Client 文档](https://pkg.go.dev/net/http#Client.CloseIdleConnections)，筛选字段依据 [Cloudflare DNS List API](https://developers.cloudflare.com/api/resources/dns/subresources/records/methods/list/)。内容筛选在 API 中不区分大小写，因此仍需本地精确匹配。

## 基准结果

Go 1.27.3、Linux amd64、同一台 AMD EPYC 7K62 主机。固定 `GOMAXPROCS=2`、`GOMEMLIMIT=160MiB`，各格式 5 次、每次 500ms，取中位数。基线与改进版顺序执行，没有并发运行编译或 race 检查；原始记录已在计时前构造。数据为 1,000 条记录、每条含 1 KiB TXT 内容和未知字段。

| 格式 | 原耗时 | 新耗时 | 耗时变化 | 原分配量/次 | 新分配量/次 | 分配量变化 |
|---|---:|---:|---:|---:|---:|---:|
| JSON | 4.32 ms | 1.86 ms | -57.1% | 2,392,706 B | 52,430 B | -97.8% |
| CSV | 36.15 ms | 18.42 ms | -49.0% | 16,149,375 B | 4,771,656 B | -70.5% |
| txt | 50.54 ms | 34.09 ms | -32.5% | 17,165,698 B | 8,135,829 B | -52.6% |
| table | 31.71 ms | 3.96 ms | -87.5% | 18,987,378 B | 890,971 B | -95.3% |

原始结果：[基线](benchmarks/before.txt)、[改进版](benchmarks/after.txt)。复现：

```sh
GOMAXPROCS=2 GOMEMLIMIT=160MiB go test ./internal/output \
  -run '^$' -bench BenchmarkDump -benchmem -benchtime=500ms -count=5
```

这些是本地编码基准，不含网络、Cloudflare 限流和磁盘同步，也不是进程峰值内存。JSON 的小分配次数由约 5 次升至约 1,012 次，但总分配字节显著下降；逐条编码避免了与整个导出大小相同的临时缓冲。

## 保留的边界

API 分页不是事务快照；修改前核对也不是服务端原子 CAS。记录仍需完整收集后排序，`--max-bytes` 控制的是原始 JSON 累计量，不能视作 RSS 上限。创建响应丢失后无法取得可靠记录 ID 时继续返回“不确定”，由后续查询判断。预创建输出只能提前排除路径/权限错误，不能保证后续不会发生磁盘满或管道断开。

四个平台继续由 CI 原生执行测试和发行包验证；真实 API 写入测试仍需显式指定测试 Zone。正式版本继续通过版本标签触发 Release。
