# 配置与接口约定

默认路径来自 `os.UserConfigDir()/cloudflare-go/config.json`：Linux 通常为 `~/.config/cloudflare-go/config.json`，macOS 为 `~/Library/Application Support/cloudflare-go/config.json`。`--config` 可覆盖。

```json
{
  "default_profile": "personal",
  "profiles": {
    "personal": {"token_file": "credentials/personal.token"},
    "work": {
      "token_env": "CLOUDFLARE_WORK_TOKEN",
      "account_id": "33333333333333333333333333333333",
      "default_zone": "example.org",
      "format": "json"
    }
  }
}
```

profile 选择：`--profile` → `CLOUDFLARE_PROFILE` → `default_profile`。没有 profile 时可直接使用 `CLOUDFLARE_API_TOKEN`。凭据选择：`--token-file` → 所选 profile 的 token_file/token_env → 没有显式来源时的 `CLOUDFLARE_API_TOKEN`。所选 profile 的文件失效时不静默回退到其他账号的环境 Token。文件与环境变量来源互斥；相对文件路径相对配置文件目录。

显式 `--config` 必须指向已有文件，只有 `init` 可创建；`config show/check` 也要求文件存在。隐式默认路径不存在时可直接使用环境 Token，但失效的配置符号链接会报错。显式空的 config/profile/token-file/token-env/zone/zone-id/id/type/format/output/file 参数不触发默认值回退；`--timeout 0` 是错误。没有 HOME/XDG 配置目录的环境仍支持 help/version 和显式 `--config`。

字段名保持领域含义，不接受 API Token 明文属性、重复属性或未知属性。profile 名称为 1–64 个字母数字、下划线或连字符，以字母数字开头。`init` 修改所选 profile 时保留其他 profile 和未指定的可选设置。`profile remove` 只删除配置引用，不删除外部凭据文件。

配置字段必须使用文档中的准确拼写；profile 名称区分大小写。JSON 输入最多嵌套 128 层。`init --token-file` 的相对路径以当前工作目录为基准；配置内已有相对路径仍以配置目录为基准。初始化期间同一 profile 被另一个进程修改时返回冲突，保留较新的配置。

显式 `--zone-id` 覆盖 profile 的默认 Zone，并只查询一次 Zone 详情；同时显式指定 `--zone` 时二者必须一致。

公共参数：

| 参数 | 默认与行为 |
|---|---|
| `--zone`、`--zone-id` | 不指定时由输入域名向父域查询最长匹配 Zone；两个同时指定时必须一致 |
| `--timeout` | 2m，dump 为 10m；包括远端查询与重试，删除人工确认后重建执行预算 |
| `--max-bytes` | 268435456（256 MiB），收集记录原始 JSON 的累计上限；超过即失败 |
| `--format` | table/txt/csv/json；整 Zone dump 另支持 bind |
| `--output/-o` | 原子输出文件，0600，不覆盖旧文件 |
| `--overwrite` | 明确允许替换普通输出文件，不接受符号链接或特殊文件 |
| `--spreadsheet-safe` | 仅 CSV，给可能的表格公式前缀增加单引号；默认关闭 |

记录名称支持 Unicode→Punycode、大小写归一化和末尾点规范化；记录所有者名称允许下划线及最左通配符，Zone 与普通主机目标不允许。`@` 和单标签简写需要指定/配置 Zone；多标签名称按 FQDN 处理，不猜测其是否为相对名称。ID 需要 32 位十六进制。

ASCII 记录标签内的下划线不限于前缀；`。`、`．`、`｡` 按 IDNA 规则转换为点号。IPv6 写入内容使用规范化表示，降低等价写法造成的重复记录误判。`content-file` 和文本参数要求有效 UTF-8，不用替换字符修复输入。

查询的多个条件按 AND 组合，多个 `--type` 按 OR 组合。update 的 `--content/--proxied` 是新值；筛选原记录内容使用 `--match-content`。单条写入只允许一个类型选择。

JSON 结果：list 为数组，get/create/update 为单条 API 记录，delete 为 `{id,deleted}`，dry-run 为 `{action,zone,record_id?,before?,changes?}`。dump 为：

```json
{"schema_version":1,"exported_at":"2026-10-31T00:00:00Z","zone":{"id":"…","name":"example.com"},"query":{"domain":"example.com","exact":false},"records":[]}
```

CSV 固定列：zone_id、zone_name、id、type、name、content、ttl、proxied、proxiable、priority、comment、tags、data、settings、meta、private_routing、created_on、modified_on、comment_modified_on、tags_modified_on、record_json。嵌套值为 JSON 单元格，record_json 保留未知字段。默认 CSV 不添加 BOM。

每次响应限制 16 MiB、每页请求 100 条；显式分页持续传递原 context，验证分页前进，不固定截断页数。GET 最多重试两次；POST/PATCH/DELETE 不自动重试。单次 HTTP 请求 30 秒，不接受 HTTP 重定向。

提前出现的空页、页码异常、同 ID 数据变化、响应对象类型错误以及详情/修改/删除返回错误 ID 都会报错。单类型、代理状态与内容条件会同时交给 API 筛选，并继续在本地检查大小写精确内容和 DNS 标签边界；多个类型仍按本地 OR 筛选。

HTTP 错误只有具备有效 Cloudflare 错误结构时才能被识别为明确拒绝。删除核对只接受 HTTP 404 加 Cloudflare 错误码 81044（记录不存在）；代理 HTML、未知路由和其他 404 不能证明记录已删除，继续返回结果不确定。
