# 测试与发布

本地：`go test ./...`、`go test -race -timeout 5m ./...`、`go vet ./...`、`python3 -m unittest discover -s scripts -p 'test_*.py'`。

基准：`make bench`。导出测试固定 1,000 条、每条 1 KiB TXT 内容；传输测试固定一页 100 条，测量响应校验。CI 保存 `export-benchmark` 附件。`BenchmarkCollection` 测量 SDK 解码、分页、筛选、排序到 JSON 输出的完整链路；`TestCollectionRetainedHeap` 在最后一页前执行 GC 后采样保留堆增量，不代表峰值或 RSS。历史方法与结果见[首轮](REVIEW.md)、[第二轮](REVIEW-2.md)、[第三轮](REVIEW-3.md)和[第四轮审查报告](REVIEW-4.md)。

CI 的 `Paired collection acceptance` 使用固定基线 `f933a60966cd74a8c3404489767ab7b79bac81e6` 与当前提交，在同一 runner 上交替执行五对样本，每样本固定 5 次完整收集；`GOMAXPROCS=2`、`GOMEMLIMIT=160MiB`，fixture 不含真实网络或磁盘。两种场景分别是不筛选和 1% 匹配，各自取样本中位数：累计分配量 B/op 必须不超过基线的 70%，耗时 ns/op 不超过 105%；10,000 条、1% 匹配时的保留堆增量不超过 110%。计数缺失、测量失败和不满足门槛都会使 CI 失败。共享 runner 的测量有波动，门槛仅用于这组固定对照，不能外推真实 Cloudflare 延迟。

```sh
python3 scripts/compare_collection.py --baseline /path/to/f933-checkout \
  --candidate "$PWD" --output /tmp/collection-comparison
```

`collection-comparison` CI 附件保留原始输出、提交 ID、全部样本、中位数比值和门槛结果 14 天；本地比较应使用已提交的干净 checkout，报告不检测工作目录改动。

测试使用本地 HTTP 假服务，验证实际官方 SDK 请求的 Bearer 认证、POST/PATCH/DELETE、原始 JSON 保留、精确 false、分页、重试和错误脱敏。CLI 测试通过整个命令/API/输出链路验证 dry-run、CRUD、冲突、删除确认、dump 和 profile 更新。发送边界覆盖拨号失败、TLS 失败、发送后丢失响应、未知传输及取消；响应额度覆盖包裹字段、重试错误正文、Zone 与记录合计、确认/Zone 选择后的共享额度及写入后的退出码。连接池分别验证自建传输释放和外部传输保留。本地补丁错误验证在缺少凭据时仍先报告输入原因。名称、配置与 JSON 解析器提供 fuzz 入口。

CI 在 Linux amd64/arm64、macOS amd64/arm64 原生运行全部 Go 测试，构建并执行同一份发行包。独立检查 race、vet、Staticcheck、govulncheck、actionlint、模块一致性、解析器 fuzz 和 Python 发行脚本测试。Linux amd64 重建归档进行逐字节比较。

发布：填写 CHANGELOG，将代码推送 main 并等待 CI，通过后创建 `v0.1.0` 等版本标签。标签必须属于 main 历史，且对应非空变更记录。Release 工作流重新执行完整验证，生成四个归档和 checksums.txt；生成 GitHub 来源证明，再草稿上传、下载回读核对、最后公开。

```sh
gh attestation verify cloudflare-go_v0.1.0_linux_amd64.tar.gz --repo gatework/cloudflare-go
```

每个包包含二进制、配置示例、文档、CHANGELOG、go.mod/go.sum、构建元数据和依赖许可证。源码标签不可移动，公开附件不可覆盖。上传中断可以重跑，只补齐缺失附件；未知附件、内容不一致或标签移动会停止发布。

打包要求指定提交与 HEAD 一致、已跟踪文件没有未提交改动。构建与文件收集均使用独立 Git 快照，工作目录中的未跟踪/忽略文件不参与构建或进入归档；`GOWORK=off` 排除父目录 workspace 对依赖的影响。普通开发构建可继续使用 `make build`。

macOS 为未公证的 Go 单文件程序；Linux CGO_ENABLED=0，归档验证拒绝带动态解释器的 ELF。测试不声称已执行真实生产 DNS 写入，也不把 API 查询分页当作事务快照。真实写入验收仅可通过显式启用的测试 Zone 集成测试执行。


真实 API 测试默认跳过。必须指定专门测试 Zone：

```sh
CLOUDFLARE_INTEGRATION=read CLOUDFLARE_TEST_ZONE_ID=ZONE_ID \
  CLOUDFLARE_TEST_API_TOKEN=TOKEN_FROM_SECURE_ENV go test -v ./integration
# write 显式启用创建、读取、修改、删除本轮唯一 TXT 记录：
# CLOUDFLARE_INTEGRATION=write ... go test -v ./integration
```

实际运行时通过安全环境注入 Token，不要把真实值写入 shell 历史。write 模式只清理本轮 API 返回的 ID，并先核对唯一名称/内容，不按前缀批量删除。
