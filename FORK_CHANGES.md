# Fork 差异能力与合并说明

本 fork 相对上游主线的**能力差异清单**，用于后续 rebase / cherry-pick 时快速了解
「哪些行为是 fork 特有的、改在哪些文件」。

- **上游仓库**：`https://github.com/linguo2625469/workbuddy2api-panel`
- **比对基准 Commit**：`e05edb7`
- **同步方式**：以该基准做 rebase 或 cherry-pick；冲突集中在下方「挂载点」列出的少数文件。
- **共同原则**：新增能力自成一体（独立包/文件），既有代码只在少量挂载点插入调用；
  未启用配置时行为与主线一致。

## 1. 差异能力一览

| # | 能力 | 行为差异 | 默认 |
| --- | --- | --- | --- |
| 1 | **转发契约（严格、不改写）** | 不再改写请求内容：删除系统提示词指纹清洗、内容拦截降级重试、DeepSeek 自动开思考/补档、effort 降抬档、GPT `max_tokens` 抬升、工具历史合并重排删除、schema pattern 改写。未知字段与大整数完整保留；无法表达的输入明确返回 400 | 生效（无开关） |
| 2 | **响应统一管线** | 流式与非流式共用同一 SSE 解析与完成判定；HTTP 错误与 `error` 帧同一分类；按 choice index 独立聚合；工具名增量拼接；错误帧/截断不再伪装成功；新增首个模型事件/首个生成/尾部阶段超时 | 生效 |
| 3 | **重试与账号策略** | 仅「明确未受理」才换号；已生成/已提交一律不重放；限流与配额不跨账号绕过；仅真实完成才记成功、清模型负缓存、绑粘性 | 生效 |
| 4 | **分域身份与 UA** | 按 `realm × 用途`（chat/refresh/catalog/billing/banner/desktop）生成版本、产品名、Origin、语言与 UA；不随代理域名变化；修复 refresh 丢失 realm；带凭据请求不跟随重定向；refresh token 不出现在 chat 头 | CN `5.7.6`/CLI `2.156.0`、Global `5.6.2`/CLI `2.147.0` |
| 5 | **缓存键** | `prompt_cache_key` 改为 HMAC 派生（持久化 secret），无显式会话则不生成；客户端显式值保留 | 生效 |
| 6 | **配置容错** | 未知/退役键忽略并告警（启动日志 + 面板），保存时原样保留；仅「已识别键的值非法」报错；`config_version: 2` | 生效 |
| 7 | **出站代理** | 普通正向代理 + Resin 粘性代理池 + 账号级代理开关 | 未配置 = 不接入 |
| 8 | **裸模型名默认域** | `model_default_realm = cn / global / auto` | `cn` |
| 9 | **客户端特征对齐** | 稳定设备/会话指纹、硬件特征离散化、`/v2/report` 桌面指纹、版本自检 | 启用 |
| 10 | **面板版本配置** | 占位符来自后端内置基线（不硬编码）；「一键填入已拉取版本」；CLI 版本需人工核对并给出版本不成对提醒 | 生效 |

## 2. 相关改动位置

| 能力 | 主要文件 |
| --- | --- |
| 转发契约 | **新增** `internal/forwarding/`、`internal/jsondoc/`；**删除** `internal/upstream/{payload,thinking,sanitize,tool_pairing}.go`；`internal/prompt/prompt.go`（大整数安全解码） |
| 响应管线 | `internal/upstream/sse.go`（重写）、`internal/upstream/idle.go`；**删除** 第二套解析器逻辑（`internal/server/logging.go` 改为事件观察者） |
| 重试与账号策略 | `internal/server/handler.go`、`internal/upstream/client.go`（**删除** `internal/server/degrade.go`） |
| 分域身份与 UA | **新增** `internal/upstream/identity.go`、`internal/auth/snapshot.go`；`internal/upstream/{headers,client,desktop,global_models,hint,usage}.go` |
| 缓存键 | `internal/upstream/cache_key.go`、`cmd/server/main.go`（加载 `cache-secret`） |
| 配置容错 | `cmd/server/config.go`、**新增** `cmd/server/config_warnings.go`、`config.example.json` |
| 出站代理 | `internal/proxy/`、`internal/auth/auth.go`（账号级开关）、`cmd/server/config.go` |
| 裸模型名默认域 | `internal/server/resolve_model.go`、`cmd/server/config.go` |
| 客户端特征对齐 | `internal/upstream/{headers,desktop,version_check}.go`、`cmd/server/main.go` |
| 面板 | `internal/panel/{index.html,app.js}` |

## 3. 合并注意

**挂载点（冲突最可能）**：`internal/upstream/client.go`、`internal/upstream/headers.go`、
`internal/server/handler.go`、`cmd/server/{config,main}.go`、`internal/panel/{index.html,app.js}`。

**回主线时不要恢复已删除的模块**：`upstream/payload.go`、`upstream/thinking.go`、
`upstream/sanitize.go`、`upstream/tool_pairing.go`、`server/degrade.go` 及其测试。
这些文件的职责已被 `internal/forwarding/` 与 `internal/upstream/sse.go` 取代。

**配置不兼容点**（旧配置仍可启动，仅告警）：

- `features.sanitize_blacklist_fingerprints` 已移除（不再改写请求内容）
- `upstream.user_agent` / `client_version` / `cli_version` → 改用 `upstream.profiles.<realm>.*`
- `prompt.mode`：`passthrough → none`、`custom → replace`（自动迁移并告警）
- `upstream.chat_base_cn` 默认值改为 `https://www.workbuddy.cn`

**需要留档的未验证项**：默认 chat 端点与 profile 头、`max_completion_tokens` 映射、
`tool_choice` 的 none/required/具名、思考关闭编码与 effort 档位、`n>1`、logprobs、
JSON Schema、图片、工具名增量方言。静态检查与离线测试不能替代真实上游验收。

## 4. 验证

```bash
go test ./... -count=1
go vet ./...
go test -race ./internal/server ./internal/upstream ./internal/forwarding
```
