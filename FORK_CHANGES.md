# Fork 差异能力记录

本 fork 相对上游主线的差异清单：哪些行为是 fork 特有的、改在哪些文件、回主线时不要恢复什么。
配置项与用法见 README「配置说明 → 字段速查」，本文只记差异与合入注意事项。

- **上游仓库**：`https://github.com/linguo2625469/workbuddy2api-panel`（已加为 remote `upstream`）
- **已同步基线**：`d66384d`（v1.13.0-panel）
- **共同原则**：新增能力自成一体（独立包/文件），既有代码只在少量挂载点插入调用

---

## 1. 同步对齐与合入逻辑

**为什么不用 rebase / cherry-pick**：fork 是 squash 初始提交（根提交 `18c6443`，全库 4 个提交），
与上游（202 个提交）**无共同祖先**——`git merge-base` 为空，cherry-pick 一个提交也要连带
baseline 之前的所有变更，冲突量远超收益。故采用
**「按文件取上游版本 + 手工 port」**：能在 fork 里逐字节对齐的整文件直接取，已大改的文件只手工挑逻辑。

### 同步步骤

```bash
git fetch upstream main
git log --oneline <已同步基线>..upstream/main     # 列出待判定提交（合并提交无独立内容）
```

1. **基线校验**：确认 fork 的 `appVersion`（`cmd/server/main.go`）等于上次同步的上游版本
   （实测：`1.12.0-panel` ↔ `e05edb7`，同步后 `1.13.0-panel` ↔ `d66384d`）。
   版本号对不上 = 中间有整段历史未处理，先查清再往下走。
2. **逐提交判定是否已合入**（两类证据，取其一即可）：
   - 文件未改：`git diff <基线> HEAD -- <file>` 为空 → 该提交在此文件上可整取；
   - 已改：按新增的符号/字符串在 fork 中检索（`grep -rF 'IsEnterprise' internal/`）。
   - 批量初筛：提取提交的**新增行**在 fork 工作树里 grep，算命中率（`命中/新增`）；
     命中率接近 100% 即已并入，接近 0 即未并入，中间值即部分并入需逐文件看。
3. **分类移植**：
   - 与上游逐字节一致的文件 → `git checkout upstream/main -- <file>`（可用 `git hash-object` 复核）；
   - fork 已大改的文件（如 `internal/panel/*`）→ 只手工挑逻辑，保留 fork 的本地改动。
4. **顺带移植测试**：上游同批测试若与 fork 构造兼容，整文件取；跑一次「还原修复 → 用例应 FAIL」验牙齿。
5. **收尾**：`go build` / `go vet` / `gofmt` / `go test ./...` 全绿后，在文末「同步记录」登记范围与内容。

### 选择合入逻辑

| 优先级 | 判据 | 处置 |
| --- | --- | --- |
| 1 | 影响**真实账号行为**的上游能力（门控、端点口径、额度体系） | 必合——不做则每日空发注定失败的请求、面板显示错值 |
| 2 | 修**fork 中同样存在**的缺陷（计数口径、排序、前端渲染） | 必合——同一 bug 在本仓库未修 |
| 3 | 与 fork 契约**冲突**的降级改写（如上游 `fd3e142` GPT `max_tokens` 抬升） | 不合——属 §2 第 1 条「转发契约」删除范围 |
| 4 | 恢复**已删除模块**（`payload`/`sanitize`/`thinking`/`tool_pairing`/`degrade`） | 不合——见 §3 回主线禁忌 |
| 5 | 纯测试可替换点（生产行为逐字节不变，仅提升可覆盖性） | 便宜则合 |
| 6 | 版本号 `appVersion` | fork 自管，向上取上游最新（不直接抄上游提交） |
| 7 | 文档 | 用户可见的能力说明合入 README；差异记录只入本文 |

---

## 2. 差异能力一览

| # | 能力 | 行为差异 | 默认 |
| --- | --- | --- | --- |
| 1 | **转发契约（严格、不改写）** | 出站只做校验与编码：未知字段与大整数完整保留，无法表达的输入直接 400；删除系统提示词清洗、内容拦截降级重试、思考自动补档、effort 降抬档、GPT `max_tokens` 抬升、工具历史重排 | 生效（无开关） |
| 2 | **响应统一管线** | 流式与非流式共用同一 SSE 解析与完成判定；HTTP 错误与 `error` 帧同一分类；按 choice index 独立聚合；错误帧/截断不再伪装成功；新增首模型事件/首生成/尾部阶段超时 | 生效 |
| 3 | **重试与账号策略** | 仅「明确未受理」才换号；已生成/已提交一律不重放；限流与配额不跨账号绕过 | 生效 |
| 4 | **系统提示词体系** | 组合位置 `none`/`replace`/`after`/`append` + 内置预设库 + 按账号域覆盖 + 面板预览 | `none` |
| 5 | **出站指纹改写层** | 改写 user/assistant/tool/推理/工具入参里的已知指纹串（内置 7 类 + 自定义规则） | `false` |
| 6 | **分域身份与 UA** | 按 `realm × 用途` 生成版本、产品名、Origin、语言与 UA；不随代理域名变化 | CN `5.7.6`/Global `5.6.2` |
| 7 | **缓存键** | `prompt_cache_key` 改 HMAC 派生（持久化 secret）；无显式会话则不生成 | 生效 |
| 8 | **配置容错** | 未知/旧键忽略并告警（启动日志 + 面板），保存时丢弃；`config_version: 2` | 生效 |
| 9 | **Web 管理面板** | 内嵌单页（明暗主题，七个视图）：账号运维 / 用量与积分 / 模型档位 / 在线改配置（热生效）/ 运行日志 / 任务中心 | 生效 |
| 10 | **积分任务体系** | 任务列表/接受/领取 + 「一键完成」覆盖 17 个成长任务（纯 API）；任务中心全账号扫描 + 执行队列 | 生效 |
| 11 | **出站代理** | 普通正向代理 + Resin 粘性代理池 + 账号级代理开关 | 未配置 = 不接入 |
| 12 | **裸模型名默认域** | `model_default_realm = cn / global / auto` | `cn` |
| 13 | **客户端特征对齐** | 稳定设备/会话指纹、硬件特征离散化、`/v2/report` 桌面指纹、版本自检 | 启用 |
| 14 | **面板版本配置** | 占位符来自后端内置基线（不硬编码）；「一键填入已拉取版本」；CLI 版本需人工核对 | 生效 |

---

## 3. 回主线禁忌

**不要恢复已删除的模块**：`upstream/payload.go`、`upstream/thinking.go`、`upstream/sanitize.go`、
`upstream/tool_pairing.go`、`server/degrade.go` 及其测试——职责已由 `internal/forwarding/`、
`internal/upstream/sse.go`、`internal/prompt/`、`internal/scrub/` 取代。

同理不要带回写死下限的降级改写（如上游 `fd3e142` 的 GPT `max_tokens` 抬升），它属 §2 第 1 条
「转发契约」的删除范围。

## 4. 配置不兼容（旧键不识别、不迁移：启动告警、面板保存时丢弃）

| 旧键 | 替代 |
| --- | --- |
| `features.sanitize_blacklist_fingerprints` | `fingerprint_rewrite` + `fingerprint_rules` |
| `upstream.user_agent` / `client_version` / `cli_version` | `upstream.profiles.<realm>.*` |
| `prompt.mode: custom` | `prompt.mode: replace`（旧值现为非法，报错） |
| `prompt.mode: passthrough` | `prompt.mode: none`（同上） |

其余默认值变更：`upstream.chat_base_cn` → `https://www.workbuddy.cn`。

## 5. 挂载点（冲突最可能）

| 能力 | 主要文件 |
| --- | --- |
| 转发契约 | **新增** `internal/forwarding/`、`internal/jsondoc/` |
| 响应管线 | `internal/upstream/sse.go`、`internal/upstream/idle.go` |
| 重试与账号策略 | `internal/server/handler.go`、`internal/upstream/client.go` |
| 系统提示词 | **新增** `internal/prompt/`、`cmd/server/prompt_config.go`、`cmd/server/prompt_preview.go` |
| 指纹改写 | **新增** `internal/scrub/` |
| 分域身份与 UA | **新增** `internal/upstream/identity.go`、`internal/auth/snapshot.go`；`internal/upstream/{headers,client,desktop,hint,usage}.go` |
| 缓存键 / 配置容错 | `internal/upstream/cache_key.go`、`cmd/server/{config,config_warnings,main}.go` |
| 出站代理 | `internal/proxy/`、`internal/auth/auth.go`、`internal/upstream/proxy.go` |
| 裸模型名默认域 | `internal/server/resolve_model.go` |
| 面板 | `internal/panel/{index.html,app.css,js/*.js,config.go,panel.go}` |

## 6. 未验证项（静态检查不能替代真实上游验收）

默认 chat 端点与 profile 头、`max_completion_tokens` 映射、`tool_choice` 的 none/required/具名、
思考关闭编码与 effort 档位、`n>1`、logprobs、JSON Schema、图片、工具名增量方言。

## 7. 验证

```bash
go build ./... && go vet ./...
go test ./... -count=1
go test -race ./internal/server ./internal/upstream ./internal/forwarding ./internal/scrub
```

---

## 附：同步记录

| 日期 | 并入范围 | 内容 |
| --- | --- | --- |
| 2026-10-08 | `e05edb7..d66384d`（5 个实质提交） | 企业版账号能力门控 + 企业额度分流（`get-enterprise-user-usage`）；暂停选号单列统计；到期提醒按到期时间排序；图表 tooltip 归位 `<g>`；夜猫子门控测试时钟钉住；版本号 `1.13.0-panel`。已同时移植上游 5 个测试文件与 2 个前端用例 |

> 更早的基线 `e05edb7` 为 fork 首次记录差异时的比对点；其后上游提交均已按上表并入。
