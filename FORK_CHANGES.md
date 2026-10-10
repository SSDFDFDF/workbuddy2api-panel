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
| 1 | **转发契约（严格、不改写）** | 出站只做校验与编码：未知字段与大整数完整保留，无法表达的输入直接 400（跨协议入口的提示/状态字段另有「接受即丢」，见第 15 条）；删除系统提示词清洗、内容拦截降级重试、思考自动补档、effort 降抬档、GPT `max_tokens` 抬升、工具历史重排 | 生效（无开关） |
| 2 | **响应统一管线** | 流式与非流式共用同一 SSE 解析与完成判定；HTTP 错误与 `error` 帧同一分类；按 choice index 独立聚合；错误帧/截断不再伪装成功；新增首模型事件/首生成/尾部阶段超时 | 生效 |
| 3 | **重试与账号策略** | 仅「明确未受理」才换号；已生成/已提交一律不重放；限流与配额不跨账号绕过 | 生效 |
| 4 | **系统提示词体系** | 组合位置 `none`/`replace`/`after`/`append`/`inject`（inject 对全部预设可用：客户端 system 套官方 `<user_custom_instructions>` 包装追加到网关正文末尾）+ 六种内置预设（`official-craft` / `official-ask` / `official-plan` / `official-quick` / `official-expert` 为**官方渲染产物逐字**——条件已按抓包解掉、变量已字面化或删除，`official-craft` 两份与实物抓包逐行核对；`default` 为自设计位，空 preset 回落它）+ 按账号域覆盖 + 面板预览；预设是**静态 MD 加载即用**，无模板标记、无运行期变量改写；导出工具见 [scripts/render-official-presets.py](scripts/render-official-presets.py)，官方明文模板原件归档见 [docs/official-templates/README.md](docs/official-templates/README.md) | `none` |
| 5 | **出站指纹改写层** | 改写 user/assistant/tool/推理/工具入参里的已知指纹串（内置 7 类 + 自定义规则） | `false` |
| 6 | **分域身份与 UA** | 按 `realm × 用途` 生成版本、产品名、Origin、语言与 UA；不随代理域名变化 | CN `5.7.6`/Global `5.6.2` |
| 7 | **缓存键** | `prompt_cache_key` 改 HMAC 派生（持久化 secret）；无显式会话则不生成 | 生效 |
| 8 | **配置读写从简** | 单份 `config.json` 即唯一真相：读取只认已知键（不认识的键忽略并告警，不做版本迁移、不做旧取值兼容、读盘不改盘），保存 = 把提交叠加到磁盘现值后**按当前 schema 整份覆盖**（历史遗留键随之消失） | 生效 |
| 9 | **Web 管理面板** | 内嵌单页（明暗主题，七个视图）：账号运维 / 用量与积分 / 模型档位 / 在线改配置（热生效，唯一真相为 `internal/config/catalog.go`；仍需重启的只有 5 个字段：监听地址、状态文件路径、服务读超时、Upstash 地址与 Token）/ 运行日志 / 任务中心 | 生效 |
| 10 | **积分任务体系** | 任务列表/接受/领取 + 「一键完成」覆盖 25 个内置成长任务动作（纯 API）；任务中心全账号扫描 + 执行队列；**哪些任务参与自动化可配置**（`growth.autotasks`：黑/白名单、执行顺序、mp 口径码、逐任务参数 gap/target/window/activity_id、未内置判据任务的 accept+领奖兜底），保存即热生效 | 生效（缺省全启用） |
| 11 | **出站代理** | 普通正向代理 + Resin 粘性代理池 + 账号级代理开关 | 未配置 = 不接入 |
| 12 | **裸模型名默认域** | `model_default_realm = cn / global / auto(:cn,global) / auto:global,cn` | `cn` |
| 13 | **客户端特征对齐** | 稳定设备/会话指纹、硬件特征离散化、`/v2/report` 桌面指纹、版本自检 | 启用 |
| 14 | **面板版本配置** | 占位符来自后端内置基线（不硬编码）；「一键填入已拉取版本」；CLI 版本需人工核对 | 生效 |
| 15 | **Responses / Anthropic 桥接** | `/v1/responses` 无状态文本 / function tools；`/v1/messages` 文本 / client tools。复用原有执行、重试、用量与日志；原生 Chat 保留扩展。纯提示/遥测字段（`cache_control` / `metadata` / `include` / `client_metadata` / `service_tier` / `prompt_cache_key` / `stream_options` / `top_k` / `text.verbosity` 等）与客户端侧推理状态（Responses `reasoning` 历史、Anthropic `thinking`）接受即丢，丢弃项写入请求归档并在面板展示；近似变换显式可见（`is_error` → `[tool error]` 文本前缀、`stop_sequences` → Chat `stop`）；会改变语义或伪造能力的字段（`previous_response_id` / `conversation` / `background:true` / `text.format` 结构化输出 / `strict:true` / 白名单外未知字段）仍明确 400。见 [兼容说明](docs/PROTOCOL_COMPATIBILITY.md) | 生效（限定子集） |

---

### 成长任务自动化策略（`growth.autotasks`）

**动机**：哪些活动任务能被自动化，此前是 `internal/panel/autotask.go` 里的硬编码注册表
（`autoActions` / `builtinMPTaskCodes`）——上游每上一个新活动、调一次反作弊口径，都只能改代码重编译。
现在改成「判据实现仍是代码 + 启用集合/参数可配置」：

| 配置键 | 语义 | 缺省 |
| --- | --- | --- |
| `disabled` | 屏蔽的任务码黑名单（与 `only` 同时命中时黑名单优先） | 空 = 不屏蔽 |
| `only` | 白名单；非空时只有列出的任务参与自动化 | 空 = 不启用白名单 |
| `order` | 执行顺序覆盖（队列与一键完成）；列出的按本序在前，未列出的按内置依赖序排后 | 空 = 完全按内置序 |
| `mp_codes` | 小程序口径专属任务码：**裸码 = 整体覆盖**内置表，前缀 `+` = 追加 | 空 = 内置表 |
| `allow_unknown_claim` | 对「上游已下发但无判据实现」的任务只做 accept + 达标领奖（绝不伪造事件）；未达标不排队 | `false` |
| `tasks.<code>.gap` | 判据事件间隔（Go 时长，如 `45s`；mp 对话事件有上游反作弊校验） | 空 = 内置 |
| `tasks.<code>.target` | 进度目标覆盖（`0` = 以上游下发为准） | `0` |
| `tasks.<code>.attempt` | 「尝试型」展示标记覆盖（不改变执行逻辑） | 内置 |
| `tasks.<code>.window` | 计数窗口 `HH:MM-HH:MM`（支持跨零点；`black_cat` 用，**同时作用于面板动作与 blackcat 排程**） | 内置 `23:00-08:00` |
| `tasks.<code>.activity_id` | 上报事件的 activityId（`school_season` 用，活动改 id 免改代码） | 内置 `school_open_day_2026` |

**能配什么、不能配什么**（设计边界）：

- **能配**：启用集合、顺序、mp 口径、事件间隔、进度目标、窗口、activityId。上游改活动节奏/加严风控
  时不需要改代码。
- **不能配**：判据事件的**字段形状与埋点指纹**（`chat_request_send` 带哪些字段、
  `expert_actual_use` 带不带 conversationId、桌面/小程序两套指纹）——这些是逆向产物，
  必须先作为 Go 代码存在（`internal/upstream/school.go` 等），配置只能引用已实现的动作。
  新增一类判据仍需改代码；新增「与已实现动作同形状」的活动则可只靠配置接入。

**只影响自动化动作集合**：`disabled` / `only` 不改账号页的任务列表展示，也不拦手动
「接受 / 领取」接口（上游下发的任务在那里照常可见可操作）；被屏蔽的任务在「一键完成」返回 501
并说明是策略屏蔽，不静默失败。

**运行时行为**：

- 全部字段热生效（`internal/config/catalog.go` 的 `growth.*` → Hot；保存时
  `Panel.SetAutotaskPolicy` 原子替换快照）。
- mp 专属码优先**运行时探测**：任务中心每次拉双口径列表（`ListTasks` vs `ListTasksMP`，两者都
  成功时才更新）把差集记入缓存；静态表与配置只作兜底。因此上游新增 mp 任务无需登记。
- 配置里出现非内置任务码只打日志（镜像进面板运行日志），不报错——活动下线/码改名是常态，
  报错会让旧配置起不来。
- 窗口判定与排程共用同一实现（`upstream.InWindow`）：配置收窄 `black_cat` 窗口后，`blackcat`
  排程在窗口外触发会直接跳过，不会出现「面板说在窗口内、排程说不在」的分裂
  （`cmd/server.blackcatWindowFrom` 装配与热改共用口径；排程器口径见 `scheduler.SetBlackcatWindow`）。
- **面板保存是深合并**：`growth.autotasks.tasks` 这类 map 无法通过面板删除键，只能手工编辑
  `config.json`（面板配置页给出只读预览与说明）；`disabled`/`only`/`order`/`mp_codes` 列表
  清空表单即下发 `[]`（整体替换，可清除）。

**面板入口**：配置页「定时任务」分区底部「成长任务自动化」；内置动作码清单由后端
`GET /panel/api/tasks/actions` 下发（前端不硬编码，灰显 = 当前被策略屏蔽）。

---

## 3. 回主线禁忌

**不要恢复已删除的模块**：`upstream/payload.go`、`upstream/thinking.go`、`upstream/sanitize.go`、
`upstream/tool_pairing.go`、`server/degrade.go` 及其测试——职责已由 `internal/forwarding/`、
`internal/upstream/sse.go`、`internal/prompt/`、`internal/scrub/` 取代。

同理不要带回写死下限的降级改写（如上游 `fd3e142` 的 GPT `max_tokens` 抬升），它属 §2 第 1 条
「转发契约」的删除范围。

跨协议兼容不得引入模型名猜测式补丁：不按模型名增删参数、不搬移 system、
不改写 Schema、不伪造 reasoning / 工具结果、不将截断转为成功。
工具空 identity 续传与名称方言解析仅由新协议消费者显式启用；非空 ID 冲突仍报错，原生 Chat 不变。
名称的重复 / 累计 / 分片解释仅在唯一匹配本次工具声明时采用；未声明、缺失和歧义均失败。
长流正文与工具参数改用增量缓冲，保留快照冲突规则与原生流式已发送正文的释放行为。
缓存计数桥接只映射已观测字段，不把 cache miss 当 cache write。

跨协议入口的接受即丢分两类，且都必须记账：

- **纯提示/遥测类**：`cache_control`、`metadata`、`service_tier`、`top_k`、`include`、
  `truncation:auto`、`client_metadata`、`prompt_cache_key`、`stream_options`、
  `safety_identifier`、`top_logprobs`、`text.verbosity`、Responses message `phase`。
  丢弃不改变任何生成语义（缓存/路由/遥测提示或额外输出字段）。
- **客户端侧推理状态**：Responses `reasoning` 历史项、Anthropic `thinking` /
  `redacted_thinking` 块与 `thinking:{type:"enabled"}`。Chat 上游没有签名/加密状态可回放，
  丢弃意味着模型按可见消息**重新推理**、推理链不延续（这是有损取舍，不是无损转换）。

近似变换必须显式可见，不得伪装：`tool_result.is_error:true` 转成内容前缀 `[tool error]`
（Chat 无工具失败标志）；`stop_sequences` 透传 Chat `stop`，但响应只能给
`stop_reason:end_turn` 与 `stop_sequence:null`（上游不回报命中了哪个序列）；
`reasoning.effort` 透传 Chat `reasoning_effort`，但响应不回推理文本。
被丢弃字段统一进 `protocol.Request.Dropped` → `reqlog.Event.dropped`（去重、数组下标折叠为
`input[]`、上限 8 项，只写归档不打 stdout）→ 面板「丢弃字段」列与搜索。
仍拒绝：`previous_response_id`/`conversation`、`background:true`、`text.format` 非 `text`、
`strict:true`、非空 namespace `description`、白名单外的未知字段——丢失它们会改变请求、
对话或能力语义。
入口头不做协议合规断言：`anthropic-version` 缺失/任意值均放行，`anthropic-beta` 不再拒绝
（两者都不上行，真正的 beta 语义字段仍按上述规则逐字段处理）。

协议 P0 优化：`protocol.Completion` 将原始记账数据与有序块引用分开，流式/非流式共用
`collect` 与块格式化；不新增跨块正文/参数复制。工具后文本并入同一文本块、快照与增量可
混用（快照覆盖先前增量且不重复投递）：目标协议无法复放原始交错顺序，但不丢内容。
暂不宣称支持有序块历史回放。Responses 在明确 length/content_filter 时允许身份完整的
未完成工具作为诊断数据；只在最终 incomplete envelope 携带，不发工具生命周期事件。
参数前缀验证不修复 JSON，错误语法/重复键/过深嵌套仍拒绝；无 finish 的 EOF 仍失败。
Anthropic 不能表达半截 input 对象，继续拒绝截断工具。原生 Chat 完成语义不变。

提示词默认不变（none + fingerprint_rewrite:false）。三协议端到端夹具锁定四种组合模式
不改变工具声明、选择、历史参数、大整数和工具结果；replace 仍明确删除全部 system/developer。
独立验证指纹开关开启后会改写历史参数及结果，不能把该行为误称为“提示词替换无损”。

后续工具兼容：Responses 支持空分组说明的 namespace 函数工具子集。短名称沿用长度前缀编码；
长组合名使用带版本域的 SHA-256 / base64url 稳定别名（47 字节），通过请求级索引恢复输出
namespace/name，保留子项描述、schema 与 call_id。重复、别名冲突和局部选择歧义仍拒绝。
历史显式携带 namespace 即可重建身份，不再要求重新声明；历史身份碰撞索引与本轮工具声明
分离，不使用会话缓存、不补造 schema、不给历史工具增加调用权限。缺省/null namespace 代表
平面历史，不因本轮 namespace 局部同名而改写或拒绝。声明/历史或历史之间实际别名碰撞仍拒绝。
非空分组说明/custom/deferred 等不能等价表达的语义仍拒绝，不注入提示词；
缺省/null 的 namespace `description` 按空串处理（不再要求显式 `"description":""`），
`strict` 同样允许省略（官方默认即 `false`，`strict:true` 仍拒绝）。
function_call_output 支持 input_text 数组（包括 []），保留块顺序；Anthropic tool_result 也支持
content:[]。空数组保持原形，不作为 null、缺失结果或虚构文本；普通消息内容校验不随之放宽。
新协议在最终交付工具前校验 none/required/指定函数/禁止并行；违规上游结果不交付工具，
不中途改请求也不重放生成。明确 length/content_filter 允许缺失尚未产生的必需调用，
但不能绕过禁止调用或选择函数限制；原生 Chat 不加这些跨协议检查。

响应侧容错（不改变请求侧拒绝语义）：上游 message 的旧版单调用 `function_call` 折入
`tool_calls`（合成 call id；`finish_reason:function_call`、以及旧版调用旁的 `stop` 归为
`tool_calls`），不再 502；未知 message 字段、非空 `annotations`/`refusal`、logprobs、
文本分段数组与混合快照/增量均不再拒绝——能映射的映射（`refusal` 无正文时作正文、
`annotations` 进 Responses `output_text`、文本分段拼接），不能映射的忽略。
最终聚合才出现的文本（快照补正文、refusal 兜底）会补建文本块，流式收尾时从未以 delta
发出的正文在关闭块内补发，不再因块计划不一致 502 或静默丢文本。
工具名白名单只在请求声明了工具集或带工具历史时强制（“历史不授权工具”边界不变，
`TestHistoricalIdentitiesDoNotGrantTools` 继续拒绝退役身份）；未声明任何工具的请求
放行上游工具调用，并在未声明场景下仍做名称增量/累积/重复去重。请求体未知字段、
`strict:true`、`text.format` 非 text 等仍 400——丢失它们会改变请求、对话或能力语义。

本阶段参考 CLIProxyAPI、llm-rosetta、cc-switch，独立实现而非直接移植源码：

| 参考处理 | 取舍 |
|---|---|
| cc-switch：空工具 identity、晚到名称、并行分片，DeepSeek 缓存别名 | 借鉴无损数据形态处理与回归场景；不按模型名启用，原生 Chat 不变 |
| llm-rosetta：MiniMax `reasoning_split` / `<think>` 拆解，DeepSeek / Moonshot 删除参数及 system 前移 | 不采用：WorkBuddy 未验证，自动删参数或重排消息会改变请求意图 |
| cc-switch：Moonshot `$ref` 兄弟字段改为 `allOf` | 不采用：原补丁限定直连 Moonshot 的 Responses→Chat，不能套用到 WorkBuddy |
| CLIProxyAPI / cc-switch：namespace 展平与身份恢复 | 借鉴请求级双向身份索引；采用长度前缀 / 稳定摘要别名并检查声明与历史碰撞，历史身份不增加本轮权限；不采用 first-wins 去重、截短名称、猜测 namespace 或丢弃分组说明 |
| CLIProxyAPI / cc-switch：块状态、工具项 incomplete | 借鉴状态与终止分类；不补工具名、不修复 JSON、不在 EOF 上猜测 length；截断工具只在 Responses 终帧提供诊断信息 |
| CLIProxyAPI / cc-switch：budget→effort、档位钳位；Kimi / DeepSeek 工具历史 reasoning 补全 | 不采用：不自动升降档、不补造推理；新协议接受 `thinking:{type:"enabled"}` 与历史 reasoning 但丢弃状态（不补造思维链），原 Chat 保留已有字段 |

厂商直连补丁不等于 WorkBuddy 能力；后续采用补丁须提供真实上游脱敏请求 / 原始帧依据与回归测试。

## 4. 配置读写：只认当前 schema，不做迁移

配置文件带 `config_version`（当前 `3`），但它只是**标记 + 一道单向守卫**，不再是迁移链的终点：

- **读取**（`internal/config/load.go`）：把文件反序列化到 `Default()` 上，只认 `Config` 结构里存在的键；
  不认识的键（含 v1/v2 时代的旧键）忽略并进 `_warnings` 提示键名，**不参与任何语义**；
  旧取值（如 `prompt.mode: custom`）不做别名兼容，直接报错并列出合法取值。
  读盘**不改盘**——没有"读时迁移回写"，也没有 `config.json.v<旧版本>` 快照。
- **唯一守卫**：文件声明的 `config_version` 比程序新 → 拒绝启动（旧程序读新配置会静默丢掉
  不认识的键，那种数据损失必须 fail fast）。更旧的版本号不做任何转换，照当前结构读。
- **保存**（`cmd/server` 的 `saveConfigTx`）：提交的 JSON 叠加到**磁盘现值**上（提交里没出现的键
  保持原值，因此部分提交不会把其它键重置），校验后把**整份 `Config`** 序列化落盘。
  于是磁盘上的未知键/历史遗留随这次覆盖自然消失，写出去的文件永远是完整当前结构。

由此带来的行为变化（相对"一次性迁移"口径）：

| 旧口径 | 现口径 |
| --- | --- |
| 旧键由迁移机械改写（`features.*` → `fingerprint_rewrite`、`prompt.mode: custom` → `replace`、`upstream.client_version` → `profiles.*`…） | 一律不做：旧键忽略并告警，旧取值报错。要沿用旧配置就得手工对照下表改名 |
| 读取时自动迁移并回写 + 留快照 | 读盘只读；旧键留在文件里直到下一次保存被覆盖 |
| 保存时深合并原始 map + 剪掉未知键 | 叠加到磁盘现值后整份覆盖（未知键不再需要单独剪枝） |
| 空的分域提示词覆盖会写成空壳对象 | 保存时剪掉（`PruneEmptyPromptProfiles`），文件里不出现空壳 |

历史键对照（**仅供手工迁移参考，程序不再自动处理**）：

| 旧键 | 现在怎么办 |
| --- | --- |
| `features.sanitize_blacklist_fingerprints: true` | 手工改成顶层 `fingerprint_rewrite: true`（缺省 false，需要就自己打开） |
| `upstream.client_version` / `cli_version` | 手工搬到 `upstream.profiles.{cn,global}.{client_version,cli_version}` |
| `upstream.user_agent` | 无对应键（现按域×用途生成 UA），删掉即可 |
| `prompt.mode: custom` / `passthrough` | 手工改成 `replace` / `none`；不改会启动报错并列出合法取值 |
| `cooldown.hard_credit` / `err_threshold` / `err_cooldown` | 删掉（硬冷却固定次日 04:00，连续错误语义并入熔断器） |
| `schedule.travel_interval_minutes` | 删掉（已被 `schedule.travel_hours` 取代） |

> 为什么去掉迁移：这套机制（迁移链 + 版本闸门 + 读时回写 + 快照 + 合并 + 写侧剪枝）
> 是为一堆历史配置文件服务的，而实际只有一份自用配置。去掉之后配置路径只剩
> "读当前结构 / 写当前结构"两个动作，少了一整类"迁移写错了把用户配置改坏"的失败面。

## 5. 挂载点（冲突最可能）

| 能力 | 主要文件 |
| --- | --- |
| 转发契约 | **新增** `internal/forwarding/`、`internal/jsondoc/` |
| 响应管线 | `internal/upstream/{sse,tool_names}.go`、`internal/upstream/idle.go` |
| Responses / Messages | **新增** `internal/protocol/`、`internal/server/protocol.go`；`internal/server/{handler,logging}.go`；`upstream.ConsumeCompletion` 共享消费接口 |
| 图片形状与校验 | **新增** `internal/media/`、`internal/protocol/content.go`；`internal/forwarding/request.go`（Parse 归一）、`internal/server/handler.go`（hasImage 判定） |
| 工具结果图片策略 | **新增** `internal/media/{policy,hoist}.go`；`internal/protocol/request.go`（Decode 按入口策略）、`internal/server/protocol.go`（Mutated 重序列化）、`cmd/server/{config,main}.go` |
| 图片转码/压缩 | **新增** `internal/media/image.go`（解码/缩放/JPEG 阶梯，依赖 `golang.org/x/image`）；`cmd/server/{config,main}.go`；面板表单项（`frontend/src/{configSchema.ts,views/ConfigView.tsx}`） |
| 重试与账号策略 | `internal/server/handler.go`、`internal/upstream/client.go` |
| 系统提示词 | `internal/prompt/`（预设库与组合逻辑）、`internal/config/prompt.go` + `internal/config/prompt_preview.go`（配置解析与面板预览）（官方明文模板原件归档见 [docs/official-templates/README.md](docs/official-templates/README.md)，重新导出：`make templates-export`） |
| 指纹改写 | **新增** `internal/scrub/` |
| 分域身份与 UA | **新增** `internal/upstream/identity.go`、`internal/auth/snapshot.go`；`internal/upstream/{headers,client,desktop,hint,usage}.go` |
| 缓存键 / 配置容错 | `internal/upstream/cache_key.go`、`cmd/server/{config,config_warnings,main}.go` |
| 出站代理 | `internal/proxy/`、`internal/auth/auth.go`、`internal/upstream/proxy.go` |
| 裸模型名默认域 | `internal/server/resolve_model.go` |
| 请求归档 / 被丢弃字段 | `internal/reqlog/reqlog.go`（`Event.dropped`）、`internal/server/logging.go`（归一化：去重 / 下标折叠 / 上限 8）；面板 `frontend/src/views/LogsView.tsx`（列 + 搜索 + tooltip） |
| 面板 | 源码 `frontend/src/`（React + Vite 工程）；构建产物 `internal/panel/web/`（固定三件套，`go:embed`，见 `web_embed.go`）；后端 `internal/panel/{config.go,panel.go,web_embed.go}` |
| 成长任务自动化策略 | `internal/config/{schema,normalize,catalog}.go`（`growth.autotasks` 定义/校验/热生效登记）、`internal/panel/autotask_policy.go`（**新增**：策略快照 + 启用集合/顺序/参数/mp 解析）、`internal/panel/{autotask,taskcenter,tasks}.go`（动作实现与调用点）、`internal/upstream/blackcat.go`（`InWindow` 窗口判定，面板与排程共用）、`internal/scheduler/{scheduler.go,blackcat.go}`（`SetBlackcatWindow` 热改 + 窗口守卫）、`cmd/server/main.go`（装配 + 热应用 + `blackcatWindowFrom`）、面板表单项（`frontend/src/{configSchema.ts,views/ConfigView.tsx}`） |

## 6. 未验证项（静态检查不能替代真实上游验收）

默认 chat 端点与 profile 头、`max_completion_tokens` 映射、`tool_choice` 的 none/required/具名、
思考关闭编码与 effort 档位、`n>1`、logprobs、JSON Schema、图片、工具名增量方言。
图片的 wire 形状、外链/超限拒绝、三入口转换与工具结果图片策略（auto/passthrough/hoist/reject）已实现并有单元/端到端（假上游）回归；`media.image_transcode` / `media.image_max_dimension` 的转码与压缩有单元/端到端回归（默认全关，关闭时不解码像素）。但**没有真实上游验收**（官方 COS 链接白名单、各模型 `supports_images`、真实图片理解、`hoist` 与转码后图片在上游的接受度、JPEG 重编码对识别准确率的影响均未实测）；文件/音视频仍未实现（明确 400）。
**外链图片代抓有意不实现**（网关不是图片托管服务；代抓引入 SSRF/隐私/尾延迟成本，且本项目客户端不产生该形态）：外链请求明确 400 + 请客户端内联。若日后真遇官方自家 CDN 外链，正确修法是**白名单透传**（不下载）而非代抓。
Responses / Messages 已覆盖本地假上游及官方 Python SDK 的文本 / 工具 / 图片（含工具结果图片默认抬升）/ 两轮回传 / SSE，
仍未做真实 WorkBuddy 和完整 Codex / Claude Code 端到端验收；不能将协议测试视为模型能力证明。

本轮宽松化的依据是本机 Codex CLI `0.153.4` 与 Claude Code `2.1.198` 二进制 + openai/codex 源码的请求形态取证
（`include:["reasoning.encrypted_content"]` 恒发、`store:false`、`reasoning:{effort,summary}`、`client_metadata` 等请求字段；
`stop_sequences:["</block>"]`、`is_error:true`、`cache_control`/`metadata` 等），仍非真实上游验收：
Chat `stop` 字段的接受度、各 `reasoning_effort` 档位在真实模型上的行为、`thinking:enabled` 接受后的实际输出、
`[tool error]` 前缀对模型判断的影响，均未实测。

## 7. 验证

```bash
go build ./... && go vet ./...
go test ./... -count=1
go test -race ./internal/protocol ./internal/server ./internal/upstream ./internal/forwarding ./internal/scrub
go test ./internal/protocol -run '^$' -bench '^BenchmarkLongCompletion$' -benchmem
go test ./internal/upstream -run '^$' -fuzz FuzzToolNameResolution -fuzztime 5s -parallel 2
```

---

## 附：同步记录

| 日期 | 并入范围 | 内容 |
| --- | --- | --- |
| 2026-10-08 | `e05edb7..d66384d`（5 个实质提交） | 企业版账号能力门控 + 企业额度分流（`get-enterprise-user-usage`）；暂停选号单列统计；到期提醒按到期时间排序；图表 tooltip 归位 `<g>`；夜猫子门控测试时钟钉住；版本号 `1.13.0-panel`。已同时移植上游 5 个测试文件与 2 个前端用例 |

> 更早的基线 `e05edb7` 为 fork 首次记录差异时的比对点；其后上游提交均已按上表并入。
