<p align="center">
  <img src="https://raw.githubusercontent.com/DGZSbot/ai-icon/refs/heads/main/WorkBuddy.png" alt="WorkBuddy2API" width="120">
</p>

<h1 align="center">WorkBuddy2API Panel</h1>

<p align="center">
  <b>把腾讯 CodeBuddy / WorkBuddy 账号变成 OpenAI 兼容 API 的多账号网关 · 附 Web 管理面板</b><br>
  多账号池 · 熔断冷却 · 会话粘性 · 定时任务 · <b>成长任务一键完成（17/18）</b> · 流式 / 非流式
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.22.5-00ADD8?logo=go&logoColor=white&style=flat-square">
  <img alt="API" src="https://img.shields.io/badge/API-OpenAI_Compatible-412991?style=flat-square">
  <img alt="Deploy" src="https://img.shields.io/badge/Deploy-Single_Binary%20%7C%20Docker-2496ED?style=flat-square">
  <img alt="Transport" src="https://img.shields.io/badge/Transport-SSE%20%2F%20Streaming-0DBD8B?style=flat-square">
</p>

---

> **本项目是 [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api) 的增强分支**（fork）。
> 上游仓库现已删除；本项目**已同步至上游删库前的最后一次更新**（`ea8b1e5`），此后由本分支独立维护演进。
> 转发契约、提示词体系、指纹改写层等全部差异记录在 **[FORK_CHANGES.md](FORK_CHANGES.md)**。

> ⚠️ **本项目仅限自用账号（签到 / 保活 / 个人工具接入），不支持同时也是禁止批量小号分发额度、二次打包或收费售卖。** 详见 [使用声明（必读）](#使用声明必读)。

## 使用声明（必读）

本项目（含上游 [Sliverkiss/workbuddy2api](https://github.com/Sliverkiss/workbuddy2api)，下同）的开发初衷只有一个：**方便个人管理自己的 CodeBuddy 账号**——自动签到、保活、给自己的本地工具提供一个 OpenAI 兼容入口。它是免费、开源、按「原样」提供的个人自用工具。

近期我们发现有人将本项目用于以下行为：

- **批量注册小号 / 收购账号，对外提供付费 API、共享池、代充等业务**；
- **二次加壳、捆绑卡密（授权码）售卖**，或以「公益服」「低价中转」等名义变相收费分发。

我们对上述行为**表达最强烈的反对**，并声明如下：

1. **一切商用 / 售卖行为与本项目及作者无关。** 本项目不授权、不支持、不参与任何面向公众的 API 售卖、账号池出租、卡密收费分发；行为人由此产生的一切后果（包括但不限于账号封禁、条款违约与法律风险）由其自行承担，与作者和贡献者无任何关系。
2. **批量注册与转售接口配额违反目标平台服务条款。** CodeBuddy / 腾讯系服务条款禁止批量注册账号及商业转售接口。上游仓库已删除、停止公开维护——我们无法断定具体原因，但此类滥用行为正在毁掉所有正常使用者的环境，请勿再消耗社区的善意。
3. **请勿购买任何「收费版」「卡密版」「公益中转版」。** 本项目永远免费开源。任何加壳、加密、捆绑收费的「版本」都是他人篡改的产物，与本项目无关；且此类分发无法审计，存在被植入后门、回传并窃取你 CodeBuddy 凭证的风险（`auths/` 中保存的是明文 accessToken / refreshToken）。**你付钱买到的不是本项目，而是把自己账号交给陌生人的机会。**
4. **关于开源协议的诚实说明。** 本项目基于 MIT 协议开源，协议允许自由使用与修改源码——这是开源的本意，我们不会收回；但 MIT 赋予的是代码层面的自由，**不赋予**以本项目名义宣传、售卖、捆绑分发，或要求作者提供支持与背书的权利。作者不为任何第三方分发版本提供支持、更新承诺或安全保证。
5. **作者保留止损的权利。** 若滥用行为持续，作者可能随时停止维护、关闭或删除仓库，且不另行通知。上游的今天可能就是本项目的明天，望自重。

如果你的用途是管理自己的账号，欢迎正常使用、反馈问题与提交 PR。

## 项目简介

WorkBuddy2API 是一个自托管的 **OpenAI / Anthropic 协议桥接网关**，将腾讯 CodeBuddy / WorkBuddy（`www.workbuddy.cn` / `www.workbuddy.ai`）账号包装为统一的 API：原生 `/v1/chat/completions`，以及无状态文本 / 工具子集的 `/v1/responses`、`/v1/messages`。

- 官方不提供 OpenAI 形态的开放 API，本项目通过 **OAuth 设备授权**（面板「添加账号」或 `login.sh`）获取账号凭证，在网关侧做 token 自动刷新、账号池调度与流量治理；
- 面向 **个人多账号** 场景：多账号共享、单号故障自动换号、冷却 / 熔断防止雪崩、会话粘性保证多轮上下文不跳号；
- 客户端可使用 OpenAI / Anthropic SDK 接入；Responses / Messages 的支持范围与必要参数见 [协议兼容说明](#protocol-compatibility)，不承诺 Codex / Claude Code 全功能兼容。

> ⚠️ 合规须知：本项目是**非官方**网关，使用 CodeBuddy / WorkBuddy 账号作为上游，**仅限本人授权账号、本机 / 私有环境测试**。

## 核心能力

| 能力 | 说明 |
|---|---|
| 🔑 **OAuth 一键登录** | 面板内完成设备授权，凭证落盘并**热加载进池（免重启）**；也可用 `login.sh` |
| 🔄 **多账号池** | 快过期积分加权 + 成本分层 + 加权随机选号，Top-5 候选 + 防惊群 |
| 🛡️ **熔断与冷却** | 429 软冷却 600s 起指数退避（封顶可配）、404 短冷却、402 硬冷却至次日 04:00、连续失败熔断、在途租约限流 |
| 🧲 **会话粘性** | 同一会话尽量绑定同一账号，TTL 滚动续期，失败自动解绑，可镜像 Redis 防重启丢失 |
| ⏰ **定时任务** | 签到（09/21）+ 活跃上报（10）+ 小猫旅行（09/21）+ token 保活（22）+ 余额后台刷新，各自独立开关 |
| ⚡ **流式 + 非流式** | 出站强制 `stream:true`；SSE 帧按规范重建；非流式由本地聚合为单响应 |
| 🔌 **Responses / Messages** | 复用同一上游执行与记账路径，支持无状态文本及普通 function/client tools；Responses 要求 `store:false`、工具 `strict:false`，详见 [兼容边界](#protocol-compatibility) |
| 🖼️ **图片输入** | 三入口用户图片统一支持（Chat `image_url` 字符串/对象、Responses `input_image`、Messages `image` base64）；出站归一为上游唯一接受的 data URL 对象形态，外链/超限/音视频 part 明确 400 |
| 🧰 **工具结果图片** | `media.tool_images` 三档策略（auto/passthrough/hoist/reject）；桥接入口默认把工具图片抬升为工具批次后的 user 图片消息（官方 custom-model 插件同构），原生 Chat 默认不改写 |
| 🪄 **图片转码/压缩** | 可选（默认全关）：`gif/bmp/tiff` → PNG 无损转码；1080 / 2000 两档等比缩放 + JPEG 质量阶梯（对齐官方客户端）。关闭时请求路径不解码图片 |
| 💬 **系统提示词体系** | `none`/`replace`/`after`/`append` 四种组合位置；五种内置预设；按账号域（CN/Global）分别配置；面板可预览生效正文 |
| 🗑️ **指纹改写层** | 可选：改写 user/assistant/tool 消息里的上游黑名单指纹串；支持自定义词/句规则（热生效） |
| 🧬 **分域客户端特征** | 按「账号域 × 请求用途」生成版本、Origin、语言与 UA，不随代理域名漂移 |
| 🌐 **出站代理** | 普通正向代理 + Resin 粘性代理池；支持按账号开关 |
| ✅ **严格转发契约** | 原生 Chat 未知字段与大整数完整保留；跨协议仅转换已声明的字段，无法表达的输入明确返回 400 |
| 📊 **可观测** | 每请求一行表格日志（TTFB / token 速率 / uid）；`/healthz` 带 `service` 身份标识可接负载均衡探活 |
| 💾 **状态持久化** | 池状态本地原子落盘 + Upstash Redis 异步镜像（可选；写路径固定 worker + 有界队列，队满丢弃并计数，绝不阻塞请求） |
| 🖥️ **Web 管理面板** | 内嵌单页应用（明暗主题）：账号运维 / 用量与积分分析 / 模型档位查询 / 在线改配置（热生效）/ 运行日志 / 任务中心 |
| 🎯 **成长任务一键完成** | 17/18 个官方成长任务纯 API 完成，自动推进进度并领奖 |

## 快速开始

### 环境要求

- **Docker + Docker Compose**（服务端部署方式，镜像内已含低权限用户与全部工具脚本）——或
- **Windows / macOS / Linux 直接跑单文件二进制**（无需 Docker，见下方「Windows 单文件运行」）
- 一个或多个已注册的 CodeBuddy 账号，用于 OAuth 登录
- 宿主机 Go ≥ 1.22（仅从源码构建时需要）

### 方式〇：GHCR 镜像（免克隆免构建）

CI 会自动构建多架构镜像（`amd64` / `arm64`）并发布到 GHCR，`git clone` 之外的部署路径：

```bash
# 1. 准备配置与数据目录
mkdir -p auths data && cp config.example.json config.json
#    建议编辑 config.json 设置 api_key（或留空由程序自动生成随机密钥）

# 2. 拉取并运行
docker run -d --name workbuddy2api \
  -p 7863:7863 -e TZ=Asia/Shanghai \
  -v ./auths:/app/auths -v ./data:/app/data -v ./config.json:/app/config.json \
  ghcr.io/linguo2625469/workbuddy2api-panel:latest

# 3. 健康检查（无可用账号时返回 503）
curl -s http://localhost:7863/healthz
```

> **首次发布后须将包设为公开**：GitHub 仓库页 → Packages → `workbuddy2api-panel` →
> Package settings → Change visibility → Public，否则拉取需要 `docker login ghcr.io`。
>
> 镜像 tag 规则：`main` 分支推送 `latest` / `main` / `sha-xxxxxx`；打 `v*` tag 额外发布
> `1.2.3` / `1.2` / `1` 语义化版本；PR 仅构建验证、不推送。

### 方式一：Docker Compose（推荐服务器部署）

```bash
# 1. 克隆
git clone https://github.com/linguo2625469/workbuddy2api-panel.git
cd workbuddy2api-panel

# 2. 准备配置（compose 挂载此文件，缺失会导致容器启动失败）
cp config.example.json config.json
#    建议编辑 config.json 设置 api_key（或留空由程序自动生成随机密钥）

# 3. 启动（首次会构建镜像，约 1-2 分钟）
docker compose up -d --build

# 4. 健康检查（无可用账号时返回 503）
curl -s http://localhost:7863/healthz
# {"healthy":0,"total":0,"service":"workbuddy2api"}
```

启动后打开 **`http://localhost:7863/panel/`**，用面板「添加账号」完成登录（见下节）。

常用运维命令：

```bash
docker compose logs -f          # 跟踪日志
docker compose restart          # 重启
docker compose down             # 停止并移除容器（数据在 ./auths 与 ./data，不受影响）
```

### 方式二：Windows 单文件运行（无需 Docker）

```powershell
# 1) 下载 Release 中的 wb2api.exe，或从源码构建
go build -trimpath -ldflags="-s -w" -o wb2api.exe ./cmd/server

# 2) 直接运行：首次启动自动生成 config.json（含随机 api_key，日志打印一次）
.\wb2api.exe -config config.json

# 3) 浏览器打开面板添加账号
#    http://127.0.0.1:7863/panel/
```

exe 为**单文件自包含**（前端资源已 embed 进二进制），拷到任意 Windows 机器即可运行，只需保证 `auths/`（凭证）与 `data/`（状态）目录可写。

### 方式三：源码运行（开发调试）

```bash
go build ./...
go vet ./...
go test ./...                      # 完整测试套件
go run ./cmd/server -config config.json
```

构建全部二进制：

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o wb2api ./cmd/server
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o signin_bin ./cmd/signin
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o login ./cmd/login
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o credit ./cmd/credit
```

### 添加账号（登录）

**方式 A：Web 面板（推荐，各平台通用，免命令行）**

打开 `http://127.0.0.1:7863/panel/`，点右上角「**添加账号**」：面板展示授权链接 → 浏览器完成登录 → 自动检测并落盘凭证 → **热加载进池（无需重启）**，顺带完成首次签到。

**方式 B：命令行脚本（仅 Linux / macOS，依赖 bash + python3）**

```bash
./login.sh
# 按提示在浏览器打开授权链接 → 回到终端确认 → 凭证落盘 auths/workbuddy-<uid>.json
```

`login.sh` 内置授权 URL 获取 + 浏览器登录 + token 轮询 + 首次签到 + 凭证落盘 + 容器重启，全程无 PKCE（state 由服务端签发）。账号池在容器启动时用 `auths/` 目录自动对齐，新增凭证文件即自动发现。

> Windows 用户请用方式 A（或 WSL）；`login.sh` 需要 python3。

### 验证

```bash
# 模型列表
curl -s http://localhost:7863/v1/models -H "Authorization: Bearer your-api-key"

# 账号状态（汇总 + 每账号详情，disabled 账号透出 disabled_reason）
curl -s http://localhost:7863/status -H "Authorization: Bearer your-api-key"

# 流式聊天
curl -sN http://localhost:7863/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}],"stream":true}'

# 非流式聊天（本地聚合）
curl -s http://localhost:7863/v1/chat/completions \
  -H "Authorization: Bearer your-api-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hi"}],"stream":false}'
```

<a id="protocol-compatibility"></a>

### Responses / Anthropic 接入

| 入口 | 必要参数 / 鉴权 |
|---|---|
| `POST /v1/responses` | Bearer 鉴权；显式 `store:false`，每次发送完整 `input` 历史；function 工具必须显式 `strict:false` |
| `POST /v1/messages` | `x-api-key` 或 Bearer 鉴权；两者同时提供时都必须匹配，重复凭证头拒绝；要求 `anthropic-version: 2023-06-01` 与正整数 `max_tokens` |

```bash
curl -sN http://localhost:7863/v1/responses \
  -H 'Authorization: Bearer your-api-key' -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-v4-flash","store":false,"stream":true,"input":"你好"}'

curl -sN http://localhost:7863/v1/messages \
  -H 'x-api-key: your-api-key' -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"deepseek-v4-flash","max_tokens":1024,"stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

OpenAI SDK 的 `base_url` 为 `http://localhost:7863/v1`，使用 `client.responses.create(store=False, ...)`；
Anthropic SDK 的 `base_url` 为 `http://localhost:7863`，使用 `client.messages.create(...)` 或 `client.messages.stream(...)`。
模型需按当前 `/v1/models` 选择；`cn:` / `global:` 前缀与原 Chat 一致。

**支持范围与工具回传**

- 支持文本、普通 function/client tools、流式与非流式、基础采样和工具选择；工具 schema 原样保留，不自动改写 `$ref`。
- Responses 支持 `instructions`、message / function_call / function_call_output；assistant 文本历史可使用 `input_text` 或 `output_text`（非空 annotations 等信息仍拒绝）。上一轮 `response.output` 加入完整历史，结果用 `{"type":"function_call_output","call_id":"...","output":"结果"}` 回传；`output` 也支持 `input_text` 数组，保留分块、空文本和先后顺序，不拼接或改写；显式 `output:[]` 表示空结果，保持为 Chat `content:[]`，不等同于缺失 output 或 null（后二者仍拒绝）。关联键为 **call_id，不是 item id**。`metadata` 只在本次响应回显，不作为服务端会话。
- Anthropic 支持顶层 system、text / tool_use / tool_result；回传上一轮 assistant content，再以 user 的 tool_result blocks 提供结果。`tool_use.input` 必须为对象；`tool_result.content:[]` 保留为空数组，省略 content 仍按该协议的空结果处理，显式 null 拒绝。assistant 文本须在工具前，user 的工具结果须在普通文本前；不重排历史、不补缺失或重复的工具结果。
- 图片：三入口都支持 user 消息里的图片（原生 Chat `image_url` 字符串或对象形态、Responses `input_image`、Messages `image` base64 source），顺序与文本 part 一起保留。出站统一为上游唯一接受的 `{"type":"image_url","image_url":{"url":"data:…"}}`（`detail` 原样保留）。只接受内联 `data:image/*;base64` URL：单图解码 ≤5 MiB、单请求 ≤20 张且累计 ≤24 MiB；外链、`file_id`、音视频/文件 part、assistant 历史图片一律明确 400，不删图、不静默降级。**外链代抓有意不实现**（网关不是图片托管服务，代抓要引入 SSRF/隐私/尾延迟成本，且本项目客户端不产生该形态；详见 [FORK_CHANGES.md](FORK_CHANGES.md)）。
- 工具结果图片由 `media.tool_images` 控制（默认 `auto`）：Responses `function_call_output.output` 里的 `input_image`、Messages `tool_result.content` 里的 `image`（仅 base64）以及原生 Chat tool 消息里的图片 part（含 `content` 为 JSON 字符串数组的官方客户端形态）都按同一策略处理。`auto` 下**桥接口默认抬升**（图片抽出、tool 内容保留其余 part 或占位 `(see attached image)`，在整批工具结果之后的 user 消息里附上图片并标注来源 `tool_call_id`），**原生 Chat 默认透传**（既有转发行为不变）；`hoist` / `passthrough` / `reject` 可全局覆盖。抬升是官方 `custom-model-tool-media-hoist` 插件同构的显式兼容变换，改变图片角色与上下文位置，不是无损编码。
- 图片转码与压缩**默认全关**（关闭时只做形状与 5 MiB 硬限制校验，不解码像素）：`media.image_transcode` 把上游不支持的 `gif/bmp/tiff` 转成 PNG（GIF 只保留首帧）；`media.image_max_dimension` 取 `1080` / `2000` 时，超过档位边长或体积的图片会等比缩放 + JPEG 质量阶梯重编码（有损）。两者都是显式声明的兼容变换，与原生 Chat 的逐字透传契约不同；要求像素保真时保持关闭。
- 不支持服务端存储 / `previous_response_id` / `conversation` / 后台生成、音视频 / 文件（含工具结果内文件）、原生 thinking、严格 Schema、托管工具、beta / cache_control、`count_tokens`。非空 `stop_sequences`、`tool_result.is_error:true` 等无法等价表达的输入明确拒绝，不静默降级。原生 Chat 的扩展字段保留策略不变。

**Responses namespace 函数工具子集**

支持仅用于分组身份的 namespace（必须显式 `description:""`），子项仍须为 `function`、对象 schema、显式 `strict:false`，子项描述与 schema 原样保留。例如：

```json
{"type":"namespace","name":"crm","description":"","tools":[
  {"type":"function","name":"lookup","description":"查询客户","parameters":{"type":"object"},"strict":false}
]}
```

- 网关将 `(namespace,name)` 编码成稳定的 Chat 函数名（例如 `ns_3_crm_lookup`），仅改变工具身份；返回的调用恢复 `namespace:"crm", name:"lookup"`，`call_id` 不变。流式 added/done、最终响应和 incomplete 诊断保持同一身份。
- namespace/子函数名分别为 1–64 个 ASCII 字母、数字、`_`、`-`。短组合名沿用原编码；组合后超过 64 字节时改用 `nsh_` + 完整 SHA-256 的 base64url 编码（47 字节），与声明顺序无关。通过请求内身份索引恢复输出，不截短原名称、不从哈希反猜身份；同一请求的声明及历史之间发现任何别名碰撞仍返回 400。重复声明仍拒绝。
- `tool_choice:{"type":"function","name":"lookup"}` 仅在局部名对应**唯一声明**时接受；跨 namespace 或与平面函数同名时，使用 `auto` / `required` 或缩小工具声明集合。`tool_choice.namespace` 不是本子集支持的字段，不能传内部别名规避歧义。
- 完整历史须保留返回的 namespace，但**不再要求重新声明历史工具**；根据显式 `(namespace,name)` 稳定重建别名，不依赖跨请求缓存，也不补造 schema。历史身份与本轮可调用声明分开：历史不会增加可调用工具或影响 `tool_choice` 的局部名选择，上游调用已移除的工具仍失败。
- 历史省略 namespace（或为 null）时按平面工具处理，即使本次声明了局部同名的 namespace 工具也不猜测归属；平面历史不必额外声明。内部别名不是客户端身份，不应代替返回的 namespace/name；若平面工具与声明或其他历史的 namespace 编码发生碰撞则明确拒绝。
- 非空 namespace description、嵌套 namespace、custom/托管工具、defer_loading、async、allowed_callers、output_schema 等继续拒绝；不把分组说明偷偷塞入提示词，也不宣称完整 Codex 工具协议兼容。

**流式与用量边界**

- 文本实时输出；工具按独立 index 聚合，支持名称晚到、重复完整名、累计前缀、名称分片和空 identity 续传。名称必须唯一匹配本次声明的工具，缺名、未声明或多种解释同时成立时失败，不按“只有一个工具”猜测补名。完整完成并验证 JSON 后才发送工具事件，避免客户端执行半截调用；非空 ID 冲突、畸形工具或无法无损表达的响应明确失败，不丢坏工具后报成功。此名称兼容只用于新协议，原生 Chat 仍保留字面增量行为。
- 翻译协议在工具交付前校验 `tool_choice` 与 `parallel_tool_calls`（Anthropic 对应 `any` / `tool` / `disable_parallel_tool_use`）：禁止工具却返回调用、指定函数却返回别的函数、禁止并行却返回多个调用、正常完成却缺必需调用，均明确失败且不交付工具。明确截断/过滤可解释“尚未生成必需调用”，但不能授权本来禁止的调用。该检查不等于 strict schema 验证；原生 Chat 的转发行为不变。
- 流式与非流式共享协议层块计划，原始上游响应单独用于记账；正文与参数不因块计划再复制。当前仍只开放“文本在前、工具在后”，工具后的文本及混合 snapshot/delta 明确失败，不能把输出保序误当成历史可无损回放。
- 空流、错误帧、缺失 finish 的 EOF 不伪装成功，也不猜成 `length`，不触发生成重放；开流前返回 HTTP 错误，开流后为 `response.failed` / Anthropic `error`。
- 文本 `length` 对应 `response.incomplete` / `stop_reason:max_tokens`。OpenAI SDK `3.26.0` 的 `get_final_response()` 只处理 completed；截断须读取 incomplete 事件的 response，不能依赖该 helper。
- Responses 对**明确 `length` / `content_filter`** 且身份、声明名称完整的工具，保留原始参数字符串并标记 `status:incomplete`；参数须为完整对象或合法未完成对象前缀，缺参、错误语法、重复键、歧义/缺失名称仍失败。SSE 中这些调用**只出现在最终 `response.incomplete.response.output`**，不发工具 added/delta/done 事件；所有调用均不可执行，也不可作为 completed 历史回传。即使某个参数对象已完整，也不能把截断回合的工具升级成 completed。Anthropic 截断工具仍失败，不补 `{}`。
- 内部按原始上游 usage 记账。缓存别名不相加，cache miss 不作 cache write；Anthropic 输入扣除已观测缓存读写，SSE 初始 0 为临时计数，最终 `message_delta.usage` 覆盖。旧客户端是否支持最终输入计数更新需单独验证。
- Responses 缺失 usage 时返回 null；Anthropic 缺必要计数则失败，不估算 token。Responses 的 `usage.input_tokens_details.cache_write_tokens` 是保留已观测缓存写入的**网关扩展**，不是官方标准字段。

已通过本地假上游及官方 Python SDK（OpenAI `3.26.0` / Anthropic `1.12.1`）的文本、工具、namespace / 长别名 / 移除声明后的历史回传 / 空数组及文本数组结果、**图片（Responses `input_image` / Messages `image` base64 与工具结果图片的默认抬升）**、工具选择约束、两轮回传、流式、用量和截断测试；**尚未完成真实 WorkBuddy / Codex / Claude Code 端到端验收**。
可选 SDK 测试只访问测试创建的 loopback 网关，不消耗账号额度；普通 `go test` 默认跳过，不安装 Python 依赖：

```bash
uv run --no-project --with openai==3.26.0 --with anthropic==1.12.1 python -c \
  'import os,sys,subprocess; os.environ["WB2A_SDK_PYTHON"]=sys.executable; sys.exit(subprocess.call(["go","test","./internal/server","-run","^TestProtocolSDKSmoke$","-v","-count=1"]))'
```

**网关提示词与 Codex / Claude Code 工具约定**

- 三个入口共用提示词组合；Responses `instructions`、Anthropic `system` 转成 Chat system 后同样受 `prompt.mode` 控制。推荐默认 `none`，需通用提示时选 `after` / `append` 保留客户端规则。
- `replace` 删除所有 system/developer（包括中途消息），不区分身份模板、项目规范、权限或工具使用约定。它不修改 `tools` 的 schema/描述、工具选择、已转换的历史参数和工具结果，但**可能影响模型新生成的参数与调用行为**；通用预设不等价于 Codex 内置规范。user 中的项目规则保留。
- `fingerprint_rewrite` 是独立的有损改写层，默认 false；开启后可能直接改变出站历史 `tool_calls[].function.arguments`、工具结果和消息正文，不只是 system 模板。例如历史参数中的 `11128` 会变成 `11-128`。要求代码、命令、补丁逐字保真时不要开启。
- `after` / `append` 保留模板，不保证绕过上游内容拦截，也不保证冲突提示的模型执行优先级。提示词模式没有在本轮自动调整。

实现挂载点与开源模型补丁取舍见 [FORK_CHANGES.md](FORK_CHANGES.md)。

## 配置说明

**`config.example.json` 是配置项最完整的参考**：每个字段、默认值与结构都能在其中找到，示例值一律是 `test_key` 之类占位符，**不含任何真实密钥**。下表为字段含义速查。

### 字段速查

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `:7863` | HTTP 监听地址 |
| `api_key` | 空 | 网关鉴权密钥；**空 = 不鉴权直接放行**（公网必须设置） |
| `auth_dir` | `./auths` | 账号凭证目录 |
| `state_file` | `./data/state.json` | 账号池状态持久化文件 |
| `server.read_timeout` | `300s` | 入站请求读取（含 body 上传）总时长上限；大上下文/文件块经反代转发超时会 400 `read body: i/o timeout`；`0` = 不限制；改动需重启（#100） |
| `server.max_inflight_requests` | `64` | 服务级入站准入：并发「读取 + 整包解析 + 图片校验」的请求数上限；`0` = 不限制；改动需重启 |
| `server.max_inflight_bytes_mb` | `256` | 同上，按 `Content-Length` 计的并发字节预算（MiB）；chunked 按 2 MiB 计入；`0` = 不限制；改动需重启 |
| `server.ingress_wait` | `5s` | 准入满载时的等待上限，超时回 503 `server_busy`；`0` = 立即拒绝；改动需重启 |
| `panel.package_detail_limit` | `5` | 积分构成页单账号默认展示的最早到期包数；其余未用完包与已用完包聚合折叠 |
| `logging.request_archive_enabled` | `true` | 请求元数据 JSONL 归档开关；不记录提示词、响应正文或 Authorization |
| `logging.request_retention_days` | `7` | 请求归档保留天数；超期文件在启动和周期清理时删除 |
| `logging.request_archive_max_mb` | `100` | 请求归档总容量上限（MiB）；超限优先删除最旧文件 |
| `cooldown.soft_rate` | `600s` | 软限流（429 / 限流文案）冷却基数；同一账号连续触发按 2 倍指数退避 |
| `cooldown.soft_rate_max` | `2h` | 软冷却指数退避封顶 |
| `schedule.checkin_hours` | `[9, 21]` | 每日本地时区整点签到 + 余额查询解冻。空数组 / `null` = 未配置回落默认（不是禁用） |
| `schedule.travel_hours` | `[9, 21]` | 每日本地时区整点推进猫猫旅行状态机（领养 / 派出 / 领奖） |
| `schedule.activity_hours` | `[10]` | 每日本地时区整点对话活跃上报（点亮连登 + 解锁 `first_buddy`） |
| `schedule.keepalive_hours` | `[22]` | 每日本地时区整点刷新 token 保活 |
| `schedule.blackcat_hours` | `[23]` | 每日本地时区整点夜猫子补足（23:00–08:00 计数窗口） |
| `schedule.checkin_enabled` | `true` | 签到总开关；`false` 真正关闭 |
| `schedule.travel_enabled` | `true` | 猫猫旅行总开关（独立于签到） |
| `schedule.activity_enabled` | `true` | 活跃上报总开关 |
| `schedule.keepalive_enabled` | `true` | token 保活总开关 |
| `schedule.blackcat_enabled` | `true` | 夜猫子总开关 |
| `schedule.include_disabled_in_tasks` | `false` | 让**保号类**四任务（签到 / 活跃上报 / token 保活 / 余额刷新）对**已禁用**账号也执行——「禁用」只关选号，不停保号。`false`（默认）保持「禁用的跳过」 |
| `upstream.timeout_seconds` | `120` | 短 RPC（刷新 / 签到 / 余额 / 模型列表）总时长上限 |
| `upstream.header_timeout_seconds` | 回落 `timeout_seconds` | 聊天首字节前（响应头）上限 |
| `upstream.idle_timeout_seconds` | `300` | 聊天流中空闲上限（活跃续命，静默断流） |
| `upstream.profiles.<realm>` | 内置 | 按账号域覆盖客户端版本 / CLI 版本 / 产品名 / Origin / 语言 / 各用途 UA（`client_version`、`cli_version`、`product_name`、`application_name`、`origin`、`language`、`user_agents.<用途>`） |
| `prompt.mode` | `none` | 系统提示词组合位置：`none` = 不改写；`replace` = 只留网关提示词（删客户端 system/developer）；`after` = 网关在前 + 客户端块紧随其后；`append` = 客户端块在前 + 网关在后。支持 `prompt.profiles.<realm>.mode` 按域覆盖 |
| `prompt.preset` | `default` | 内置预设名（见下表）；`minimal`/`coding`/`tool-agent`/`assistant` 按域取中/英文正文 |
| `prompt.file` | 空 | 提示词文件路径（优先级低于内联正文、高于预设）；不可读 → 启动报错 |
| `prompt.text` | 空 | 内联提示词正文，优先生效（无需额外落盘文件） |
| `prompt.profiles.<realm>` | `{}` | 按账号域（`cn` / `global`）覆盖 `mode`/`preset`/`file`/`text` |
| `fingerprint_rewrite` | `false` | 出站指纹改写层：改写 user/assistant/tool 消息里的已知指纹串（内置 7 类 + 自定义规则）。**会改用户可见内容**，默认关闭；面板改即时生效 |
| `fingerprint_rules` | `[]` | 自定义改写规则，在内置指纹之上叠加；每项 `{match, replace, mode, action}`（写法见 FORK_CHANGES.md §4.2） |
| `media.tool_images` | `auto` | 工具结果图片策略：`auto` = 原生 Chat 透传 / 桥接口抬升；`passthrough` = 全部原样转发；`hoist` = 全部抽出为工具批次后的 user 图片消息（官方 custom-model 插件同构，改变图片角色与位置）；`reject` = 明确 400。热生效 |
| `media.image_transcode` | `false` | 上游不支持的图片格式（gif/bmp/tiff）转 PNG；GIF 只保留首帧，转码后仍超 5 MiB 则明确报错。热生效 |
| `media.image_max_dimension` | `0` | 图片压缩档位：`0` = 关闭；`1080`（base64 ≤500KB）/ `2000`（≤5MB）对齐官方客户端，超档位等比缩放 + JPEG 质量阶梯（**有损**）。热生效 |
| `upstash.url` / `upstash.token` | 空 | 空 = 纯内存模式（Noop 降级，功能照常） |
| `pool.max_in_flight` | `3` | 单账号最大在途请求数（`0` = 不限） |
| `pool.max_in_flight_global` | `2` | global 域单账号在途上限（国际版 WAF 风控更紧，压低并发） |
| `pool.degrade_threshold` | `5` | 连败降权阈值：未知错误（ErrClient/传输层）连败 N 次临时出池 |
| `pool.degrade_cooldown` / `pool.degrade_cooldown_max` | `10m` / `2h` | 连败降权时长与上限钳制 |
| `pool.cost_explore_interval` | `30m` | costTier 条件探索窗口：免费层垄断且存在未知号时，每窗口把一个真实请求搭车改道给未知号（零新增上游请求；成功即毕业，失败走既有错误策略）。`0` = 关停 |
| `pool.credit_floor` | `100` | **积分保底**：账号余额低于该值时，对**实测收费**模型（tier 2，账本 6h 内有效观测）不再参与选号——防止收费模型把余额打穿、连免费模型都 402 冷却到次日签到（最坏约 11.5 小时不可用）。tier 0（实测免费）/ tier 1（无观测）**不受限**：保底保的是「留余额给免费模型用」，且 tier 1 若拦会让账本过期 / 重启清零的触底号死锁在「学不回来」。含会话粘性路径（粘性号触底则解绑换号）。全池触底且全 tier 2 时选号返回空（网关回 503），**不放行**。签到回血越过 floor 即刻自动恢复。`0` = 关闭 |
| `pool.breaker_threshold` | `3` | 连续失败触发熔断阈值 |
| `pool.breaker_cooldown` | `30m` | 熔断基础退避时长 |
| `pool.breaker_cooldown_max` | `6h` | 熔断指数退避封顶 |
| `pool.idle_weight_per_hour` | `0.5` | 闲置补偿：每小时未使用 +0.5 权重 |
| `pool.idle_weight_max` | `5.0` | 闲置补偿权重封顶 |
| `pool.prefer_expiring` | `true` | 快过期积分加权：窗口内仍有有效快过期批次的账号，选号权重 ×3（虚拟实例）；不按到期时间排序、与批次金额无关；是软偏好，弱于会话粘性与模型成本分层（#101） |
| `pool.expiring_soon` | `168h` | 快过期加权窗口：仅窗口内仍有有效批次的账号命中上述 ×3；窗口开大 → 命中账号变多、偏好被稀释；留空或 `0` 关闭 |
| `session_sticky.enabled` | `true` | 会话粘性路由开关 |
| `session_sticky.ttl` | `30m` | 会话绑定 TTL（滚动续期） |
| `session_sticky.gc_interval` | `5m` | 过期绑定 GC 周期 |
| `model_default_realm` | `cn` | 裸模型名（无 `cn:`/`global:` 前缀）的默认域：`cn`=归国内版（历史默认）；`global`=归国际版（纯 global 池免写前缀）；`auto`（或 `auto:cn,global`）=自动判定（CN 优先，无号/失败时轮退至 global）；`auto:global,cn`=自动判定（Global 优先，无号/失败时轮退至 cn）。显式前缀恒优先；非法值回落 `cn` |
| `proxy_url` | 空 | **普通正向代理**地址（`http`/`https`/`socks5`/`socks5h`，凭证可写在 URL，如 `http://user:pass@127.0.0.1:8080`）。**空 = 不接入**；与 `resin_url` 互斥；账号可用 `use_proxy` 单独关闭（见下） |
| `resin_url` | 空 | 外部粘性代理池 Resin 接入地址（含基址与 Token，如 `http://127.0.0.1:2260/my-token`）。**空 = 不接入**，出站行为与主线一致；详见 [FORK_CHANGES.md](FORK_CHANGES.md) |
| `resin_platform_name` | 空 | Resin `Platform` 字段，必须与部署一致（不能含 `.` / `:` / `/`） |
| `resin_mode` | `reverse` | `reverse` 反向代理（推荐）/ `forward` 正向代理 |
| `resin_auth_version` | `V1` | 正向代理凭证版本：`V1`（`Platform.Account:Token`）/ `LEGACY_V0`（`Token:Platform:Account`），按部署实际值选，不自动切换 |
| `use_proxy`（每账号） | `true` | 账号级代理开关（auth 文件顶层键，面板账号行内「代理 开/关」可切换）：`false` 时该账号所有出站请求直连，其余账号不受影响 |

### 上游超时语义（三段各归其位）

| 字段 | 作用对象 | 默认 | 行为 |
|---|---|---|---|
| `timeout_seconds` | 短 RPC（token 刷新 / 签到 / 余额 / 模型列表） | `120` | 总时长硬上限，到期报错走换号 / 熔断 |
| `header_timeout_seconds` | 聊天 SSE **首字节前** | `120` | 由 `Transport.ResponseHeaderTimeout` 约束；超时 = 换号重发 |
| `idle_timeout_seconds` | 聊天 SSE **流中空闲** | `300` | 活跃吐数据续命不掐；静默超时才断流释放租约 |

聊天流（`stream` true / false 均同）**没有总时长上限**：聊天使用 `Timeout=0` 的专用 client，长思考 / 长输出不会被掐断。

### 环境变量覆盖

加载顺序：JSON 文件 → `WB2A_*` 环境变量（变量非空才覆盖）：

`WB2A_LISTEN` · `WB2A_API_KEY` · `WB2A_AUTH_DIR` · `WB2A_STATE_FILE` · `WB2A_SOFT_RATE`(duration) · `WB2A_SOFT_RATE_MAX`(duration) · `WB2A_TIMEOUT_SECONDS` · `WB2A_HEADER_TIMEOUT_SECONDS` · `WB2A_IDLE_TIMEOUT_SECONDS` · `WB2A_FIRST_MODEL_EVENT_SECONDS` · `WB2A_FIRST_GENERATION_SECONDS` · `WB2A_TAIL_SECONDS` · `WB2A_FINGERPRINT_REWRITE`(bool) · `WB2A_PROMPT_MODE` · `WB2A_PROMPT_FILE` · `WB2A_MODEL_DEFAULT_REALM` · `WB2A_CLIENT_NAME` · `WB2A_DEVICE_TOKEN` · `WB2A_DEVICE_TOKEN_FILE` · `WB2A_PASSTHROUGH_IP`(bool) · `WB2A_PREFER_EXPIRING`(bool) · `WB2A_EXPIRING_SOON`(duration) · `WB2A_PROXY_URL` · `WB2A_RESIN_URL` · `WB2A_RESIN_PLATFORM_NAME` · `WB2A_RESIN_MODE` · `WB2A_RESIN_AUTH_VERSION`

## 差异化功能

本仓库是上游的增强分支，相对上游的**全部差异**（转发契约、提示词体系、指纹改写层、
分域身份、面板与任务体系、合并挂载点、配置不兼容项）记录在
**[FORK_CHANGES.md](FORK_CHANGES.md)**。

上游客户端特征与风控体系的逆向分析（两个官方安装包解包、提示词构成、指纹清单、
11128 归因）见 **[docs/WORKBUDDY_CLIENT_FEATURES_ANALYSIS.md](docs/WORKBUDDY_CLIENT_FEATURES_ANALYSIS.md)**。

## 免责声明

本项目仅供学习和研究使用。使用者需遵守 CodeBuddy 服务条款，自行承担使用风险（包括账号封禁、条款违约等）。作者不对任何因使用本项目产生的直接或间接损失负责。

## License

本项目采用 [MIT License](LICENSE) 开源协议。

- 允许任意使用、复制、修改、合并、发布、分发、再授权及销售
- 再分发（源码或二进制形式）时，请保留原仓库的 MIT 版权声明与许可声明（如在 NOTICE 或 README 中注明原始出处 `https://github.com/Sliverkiss/workbuddy2api`）
- 本项目不授予任何上游（CodeBuddy / 腾讯）接口或服务的权利；使用者仍需自行遵守上游服务条款
