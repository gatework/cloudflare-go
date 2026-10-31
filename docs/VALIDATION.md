# 测试与发布

本地：`go test ./...`、`go test -race -timeout 5m ./...`、`go vet ./...`、`python3 -m unittest discover -s scripts -p 'test_*.py'`。

测试使用本地 HTTP 假服务，验证实际官方 SDK 请求的 Bearer 认证、POST/PATCH/DELETE、原始 JSON 保留、精确 false、分页、重试和错误脱敏。CLI 测试通过整个命令/API/输出链路验证 dry-run、CRUD、冲突、删除确认、dump 和 profile 更新。名称、配置与 JSON 解析器提供 fuzz 入口。

CI 在 Linux amd64/arm64、macOS amd64/arm64 原生运行全部 Go 测试，构建并执行同一份发行包。独立检查 race、vet、Staticcheck、govulncheck、actionlint、模块一致性、解析器 fuzz 和 Python 发行脚本测试。Linux amd64 重建归档进行逐字节比较。

发布：填写 CHANGELOG，将代码推送 main 并等待 CI，通过后创建 `v0.1.0` 等版本标签。标签必须属于 main 历史，且对应非空变更记录。Release 工作流重新执行完整验证，生成四个归档和 checksums.txt；生成 GitHub 来源证明，再草稿上传、下载回读核对、最后公开。

```sh
gh attestation verify cloudflare-go_v0.1.0_linux_amd64.tar.gz --repo gatework/cloudflare-go
```

每个包包含二进制、配置示例、文档、CHANGELOG、go.mod/go.sum、构建元数据和依赖许可证。源码标签不可移动，公开附件不可覆盖。上传中断可以重跑，只补齐缺失附件；未知附件、内容不一致或标签移动会停止发布。

macOS 为未公证的 Go 单文件程序；Linux CGO_ENABLED=0，归档验证拒绝带动态解释器的 ELF。测试不声称已执行真实生产 DNS 写入，也不把 API 查询分页当作事务快照。真实写入验收仅可通过显式启用的测试 Zone 集成测试执行。
