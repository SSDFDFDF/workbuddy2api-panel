# Fork 低侵入改动与主线同步说明

本文档记录本 fork 相对上游主线所做的**低侵入改动**、同步回主线时的注意事项，
以及配置与验证方式。

## 上游主线基准与同步规范

- **上游主线仓库**：`https://github.com/linguo2625469/workbuddy2api-panel`
- **上游最后拉取基准（Base Commit）**：`e05edb7`（后续上游更新以此版本为基准进行比对与合并）。
- **同步原则**：后续上游若发布新更新，以此基准 Commit `e05edb7` 为比对起点进行 `rebase` 或 `cherry-pick`。同步时需重点保护本 fork 针对客户端特征伪装、硬件指纹离散化以及上游风控闭环的修改，避免被上游旧版硬编码覆盖。

当前收录三处改动，彼此**无耦合**，可分别摘除/合并：

| # | 改动 | 默认行为 | 详见 |
| --- | --- | --- | --- |
| 1 | **出站代理接入**（普通正向代理 + Resin 粘性代理池）+ **账号级代理开关** | 未配置 `proxy_url` / `resin_url` = 不接入，零回归 | [第一部分](#第一部分出站代理接入) |
| 2 | **裸模型名默认域** `model_default_realm` | 缺省 `cn`，零回归 | [第二部分](#第二部分裸模型名默认域-model_default_realm) |
| 3 | **客户端特征对齐、持久化指纹统一与版本自检** | 默认启用官方最新 5.7.6 / 2.156.0 规约，消除设备哈希冲突，无感零破坏 | [第三部分](#第三部分客户端特征对齐持久化指纹统一与版本自检) |

> 共同原则：新增代码自成一体（不反向依赖业务包），既有代码只在少数「挂载点」
> 插入一行调用。**未启用配置时行为与主线逐字一致**，因此可以安全地合回主线或
> 从主线 rebase。

---

# 第一部分：出站代理接入

本部分记录**普通正向代理**与外部粘性代理池 **Resin** 的接入，以及**账号级代理开关**。

> 结论先行：改动遵循「一处新增、少量挂载」原则。新增代码集中在
> `internal/proxy/`，上游既有代码只在「构造账号请求头」的位置插入一行
> `c.tagProxy(req, a)`。**未配置代理时行为与主线逐字一致**，因此可以
> 安全地把本 fork 合回主线或从主线 rebase。

---

## 1. 接入模型

统一由 `internal/proxy` 承担，支持三种形态（同一时刻只启用其一）：

| 形态 | 配置 | 说明 |
| --- | --- | --- |
| **普通正向代理** | `proxy_url` | 任意 `http`/`https`/`socks5`/`socks5h` 代理；凭证可写在 URL userinfo |
| **Resin 反向代理** | `resin_url` + `resin_platform_name`，`resin_mode: "reverse"`（默认） | 目标编码进路径，账号用 `X-Resin-Account` 传递 |
| **Resin 正向代理** | `resin_url` + `resin_platform_name`，`resin_mode: "forward"` | 标准 HTTP 代理，`Proxy-Authorization: Basic ...` |

`proxy_url` 与 `resin_url` **互斥**（同时非空 → 配置校验 fail fast）。

本项目出站都是标准 Go `net/http` 的纯 Web API 请求，普通代理与 Resin 默认走
**反向代理**（Resin）或**普通正向代理**。

### 1.1 普通正向代理

- `proxy_url` 直接是代理地址，如 `http://127.0.0.1:8080`、
  `http://user:pass@proxy.example:8080`、`socks5://127.0.0.1:1080`。
- `http.Transport` 原生支持 `http`/`https`/`socks5`/`socks5h`（Go 1.9+）；
  URL 中的 userinfo 由标准库自动生成 `Proxy-Authorization`。
- 只有带账号标记的请求走代理；未打标记的请求（公开第三方 API）直连。
- 账号级开关关闭时该账号请求不打标记 → 直连。

### 1.2 Resin 反向代理

把目标编码进路径，凭证（Token）在 `resin_url` 的路径段里：

```
<resin_url>/<Platform>/<protocol>/<host>/<path>?<query>
```

- `protocol` 为 `http` / `https`（代表目标底层协议；`ws`→`http`、`wss`→`https`）。
- 账号通过请求头 `X-Resin-Account` 传递。
- 例：`resin_url = http://127.0.0.1:2260/my-token`，请求
  `https://copilot.tencent.com/v2/chat/completions` 且账号 `u1`，实际出站为
  `http://127.0.0.1:2260/my-token/Default/https/copilot.tencent.com/v2/chat/completions`
  + `X-Resin-Account: u1`。

### 1.3 Resin 正向代理

标准 HTTP 代理，靠 `Proxy-Authorization: Basic ...` 认证：

- V1（默认，推荐）：Base64 前的完整凭证为 `Platform.Account:RESIN_PROXY_TOKEN`。
- `LEGACY_V0`：完整凭证为 `RESIN_PROXY_TOKEN:Platform:Account`（顺序不同，不可混用）。
- 两种版本都只对**完整凭证做一次** Base64。`Account` 可含 `.` 和 `:`，实现里用
  `url.UserPassword` 交给标准库编码，**不会按 `.`/`:` 二次拆分**。

> 认证版本按部署实际值由 `resin_auth_version` 显式配置（默认 `V1`），本集成
> **绝不自动切换**服务器认证版本。

### 1.4 账号标识（Account）

- 登录后请求：统一使用 `auth.Auth.UID`。`UID` 是账号稳定标识，满足 Resin
  「同一账号标识必须稳定」的要求；**绝不**在邮箱 / Token 之间来回切换。
- 登录前请求（面板 OAuth）：使用**每次登录随机生成**的临时身份
  `login-<32 hex>`；登录成功后（仅 Resin）调用 `inherit-lease` 平滑继承给 `UID`。
  > 临时身份必须随机，固定 TempIdentity 会让所有账号继承同一个租约。

### 1.5 账号级代理开关

- 每个账号在 auth 文件里有一个顶层布尔键 `use_proxy`，缺省 `true`（= 走代理，
  与历史行为一致；旧 auth 文件不含该键，行为不变）。
- 关闭后该账号**所有出站请求直连**（chat / 签到 / 活跃上报 / 保活 / 余额刷新 /
  领奖 / 桌面事件等全站路径），其他账号不受影响。
- 开关闸口集中在 `upstream.tagProxy`：账号关闭时不打内部标记头，
  `proxy.Transport` 见无标记即透传（直连）。
- 面板「账号池」每行提供「代理 开/关」按钮（仅全局配置了代理时显示），
  对应 `POST /panel/api/accounts/{uid}/proxy`；切换结果落盘到该账号 auth 文件，
  重启不丢。

---

## 2. 新增文件（同步时可直接保留）

| 文件 | 作用 |
| --- | --- |
| `internal/proxy/proxy.go` | 配置解析校验（普通代理 + Resin）、反代 URL 拼接、正代凭证构造、`InheritLease` |
| `internal/proxy/transport.go` | 反向改写 / 正向 Proxy（含普通代理）的 `http.RoundTripper` 包装 |
| `internal/proxy/proxy_test.go` | 单元测试（普通代理 + Resin 两形态） |
| `internal/upstream/proxy.go` | upstream 侧接线：`SetProxy` / `tagProxy` / 账号标识口径 / 账号级开关闸口 |
| `internal/upstream/proxy_test.go` | upstream 接线回归测试 |
| `FORK_CHANGES.md` | 本文档 |

新增文件之间依赖关系：

```
internal/upstream ──> internal/proxy
internal/panel    ──> internal/proxy
cmd/server        ──> internal/proxy（仅构造 Config.ProxyClient）
```

`internal/proxy` **不反向依赖**任何项目内包，便于整体摘除或同步。

---

## 3. 对既有文件的全部改动点（同步冲突排查清单）

> 这些是唯一会与主线产生冲突的位置。按此清单逐条对照即可完成手工合并。

### 3.1 `internal/upstream/client.go`

1. import 增加 `internal/proxy`。
2. `Client` 结构体末尾新增字段 `proxy *proxy.Client`（私有，nil = 未接入）。

### 3.2 `internal/upstream/headers.go`

在各请求头构造函数的**末尾**各插入一行 `c.tagProxy(req, a)`：

- `CommonHeaders`（覆盖 refresh / 企业模型目录 / global 模型探测等）
- `ChatHeaders`（chat 出站；与 CommonHeaders 重复标记，幂等）
- `BillingHeaders`（billing / growth / report / travel / checkin / school）

### 3.3 手工构造头的出站函数（各插入一行 `c.tagProxy(req, a)`）

| 文件 | 函数 |
| --- | --- |
| `internal/upstream/client.go` | `fetchV3ConfigModelMap` |
| `internal/upstream/global_register.go` | `globalRegisterJSON`（新增 `a *auth.Auth` 入参，由 3 处调用传入） |
| `internal/upstream/tasks.go` | `ClaimReward` |
| `internal/upstream/desktop.go` | `ReportDesktopEvent` / `SetAppearanceTheme` / `ReportWebEvent` / `MarketExpertList` / `DesktopChatWithExpert` |
| `internal/upstream/school.go` | `schoolJSON` / `ReportMPEvent` |
| `internal/upstream/profile.go` | `FetchAccountProfile` |

### 3.4 `internal/auth/auth.go`（账号级开关）

- `Auth` 结构体新增私有字段 `useProxy *bool`（nil = 未设置，默认 true）。
- 新增 `UseProxy()` / `SetUseProxy(bool)` / `ProxyStored()`（均持 `a.mu`）。
- `Parse`（嵌套形与扁平形）读取顶层 `use_proxy`。
- `SaveAtomic` 仅在显式设置过时写回顶层 `use_proxy`（不给旧文件强加键）。

### 3.5 `internal/pool`

- `Status` 新增 `ProxyEnabled bool json:"proxy_enabled"`。
- `statusOf` 填 `e.a.UseProxy()`。

### 3.6 `cmd/server/config.go`

- `Config` 新增字段：`proxy_url`，以及原有 `resin_url`、`resin_platform_name`、
  `resin_mode`、`resin_auth_version`，解析产物 `ProxyClient *proxy.Client`（`json:"-"`）。
- `Default()` 给 `resin_mode="reverse"`、`resin_auth_version="V1"`。
- `applyEnv` 新增 `WB2A_PROXY_URL` 与 `WB2A_RESIN_*` 覆盖。
- `normalize()` 末尾调用 `proxy.New` 校验并填充 `ProxyClient`（非法/互斥配置 fail fast）。

### 3.7 `cmd/server/main.go`

- `auth.SetGlobalEnabled(...)` 之后新增 `up.SetProxy(cfg.ProxyClient)` 与启动日志。
  **必须在读取底层 `*http.Transport` 调整 `ResponseHeaderTimeout` 之后**——
  `SetProxy` 会包一层 RoundTripper，之后类型断言不再命中。
- `panel.New` 的 `Config` 传入 `Proxy: cfg.ProxyClient`。
- `restartRequiredFields` 增加 `proxy_url` 与 `resin_*` 四项（装配期对象，需重启）。

### 3.8 `internal/panel/panel.go`

- import 增加 `internal/proxy`。
- `Config` 新增字段 `Proxy *proxy.Client`。
- `loginSession` 新增字段 `tempIdentity string`。
- `New` 中在代理启用时给 `loginHTTP.Transport` 包一层
  `cfg.Proxy.Wrap(http.DefaultTransport.Clone())`（用 `Clone` 避免污染进程级默认
  Transport；反代模式下 `Wrap` 不改 base，正代/普通代理模式下才安装 `Proxy`）。
- `overview` 输出 `proxy_configured` / `proxy_mode`（前端据此显示账号级开关）。
- 新增路由 `POST /panel/api/accounts/{uid}/proxy` 与 handler `accountProxy`。

### 3.9 `internal/panel/login.go`

- `doJSON` 新增 `account string, rc *proxy.Client` 入参，内部调用
  `rc.SetAccountHeader(req, account)`（nil/未启用时空操作）。
- `newTempIdentity()` 生成随机临时身份。
- `loginStart` / `loginPoll` 传入临时身份；登录成功后（仅 Resin）调用
  `p.cfg.Proxy.InheritLease(ctx, tempIdentity, uid)`（best-effort，普通代理为空操作）。

### 3.10 配置样例 / gitignore

- `config.example.json` 增加 `proxy_url` 与 `resin_*` 四项。
- `.gitignore` 增加 `!FORK_CHANGES.md`（仓库默认忽略 `*.md`，本文档需显式放行）。

### 3.11 管理面板配置表单

- `internal/panel/app.js`：`CFG_MAP` 增加 `proxy_url` 与 `resin_*` 四项映射；
  `CLEARABLE_CFG` 增加 `proxy_url` / `resin_url` / `resin_platform_name`
  （清空即关闭代理）；账号行增加「代理 开/关」按钮与点击处理。
- `internal/panel/index.html`：在「上游与高级」中新增「普通代理 URL」与 Resin 四项表单。
  不想要 UI 时可整段删除，不影响配置文件和后端接入。

---

## 4. 配置参考

```jsonc
{
  // 普通正向代理：http/https/socks5/socks5h，凭证可写在 URL。
  // 空 = 不接入；与 resin_url 互斥。
  "proxy_url": "http://user:pass@127.0.0.1:8080",

  // —— 或 Resin（二者只能选其一）——
  // 空 = 未接入 Resin，出站行为与主线完全一致。
  // 非空示例：http://127.0.0.1:2260/my-token（含基址与 Token）
  "resin_url": "http://127.0.0.1:2260/my-token",

  // Platform 字段，必须与部署的 RESIN 平台名一致；不能含 '.'、':'、'/'。
  "resin_platform_name": "Default",

  // reverse（默认，推荐）| forward
  "resin_mode": "reverse",

  // V1（默认）| LEGACY_V0，仅正向代理使用；按部署实际值显式配置。
  "resin_auth_version": "V1"
}
```

账号级开关（写在对应 auth 文件顶层，或面板切换）：

```jsonc
{ "use_proxy": false }   // 该账号直连；缺省/true = 走全局代理
```

环境变量覆盖（优先级高于文件）：

```
WB2A_PROXY_URL
WB2A_RESIN_URL
WB2A_RESIN_PLATFORM_NAME
WB2A_RESIN_MODE
WB2A_RESIN_AUTH_VERSION
```

管理面板「配置 → 上游与高级」已提供这些表单；也可直接编辑 `config.json`。
修改后需重启进程（面板保存时会在 `restart_required` 里提示）。

---

## 5. 运行时行为

1. 账号相关请求在构造头时被打上内部标记
   `X-Wb2a-Proxy-Account: <UID>`（仅代理启用且该账号 `use_proxy != false` 时）。
2. `proxy.Transport.RoundTrip` 读取该标记并删除它（防泄漏），然后：
   - **reverse**：把 URL 改写为 Resin 反代路径，设置 `X-Resin-Account`；
   - **forward / plain**：把账号写入 request context，由 `*http.Transport.Proxy`
     生成 `Proxy-Authorization`（forward 用 Resin 凭证，plain 用代理 URL userinfo）。
3. 未打标记的请求（如公开第三方 API `models.dev`）**直连**，不走代理。
4. 面板 OAuth 登录三个阶段请求打临时身份；登录成功后（仅 Resin）`inherit-lease` 给 `UID`。
5. 连接层加固保持：`roundTripCloseIdle` 失败清池经 `CloseIdleConnections` 透传到
   底层 Transport。

---

## 6. 验证

```bash
# 单元测试（无需真实代理）
go test ./internal/proxy/ ./internal/upstream/ ./internal/panel/ ./internal/auth/ ./cmd/server/

# 端到端（示例，需本地代理与真实 config.json）
go build ./cmd/server
# 配置 proxy_url 或 resin_url 后启动，观察日志：
#   [proxy] 出站代理已接入：mode=reverse platform=Default auth=V1
#   [proxy] 出站代理已接入：mode=plain platform= auth=
```

建议手工验证点：

- 配置 `proxy_url` 后，`curl` 经网络观察出口 IP，确认同一账号出口稳定。
- 面板对某账号点「代理 关」，确认该账号出站直连、其他账号仍走代理；
  重启后开关保持。
- 关闭 `proxy_url` / `resin_url` 后启动，确认出站无 `X-Resin-Account` /
  `X-Wb2a-Proxy-Account` 头，且请求行为与主线一致。

---

## 7. 已知边界（未接入的账号相关请求）

以下路径**当前未经过代理**，如需可在后续迭代补齐：

- 独立 CLI：`cmd/credit`、`cmd/login`、`cmd/signin`、`cmd/trial`
  （各自构造独立 `http.Client` 或直接 `upstream.New()`，不读 `config.json` 的
  代理配置）。
- `scripts/*.py` 外部脚本。
- WebSocket：本项目当前无 WS 出站；若后续新增，按规范反向代理路径的
  `protocol` 段填 `http`/`https`（不是 `ws`/`wss`），客户端到 Resin 这一段用 `ws`。

> 若需正向 / 反向在同一部署内**按请求混用**：当前实现是全局形态。
> 扩展点已预留——`proxy.Transport` 只认内部标记头，可在需要时把
> 「形态」也放进内部标记（如再加一个内部头），在 `RoundTrip` 里按请求分流即可，
> 不影响既有调用点。

---

## 8. 与主线同步的操作建议

1. `git fetch upstream && git rebase upstream/main`。
2. 冲突大概率集中在第 3 节列出的文件；按清单保留：
   - import 的 `internal/proxy`；
   - `Client.proxy` 字段；
   - 各 header 构造函数 / 手工出站函数末尾的 `c.tagProxy(req, a)`；
   - `auth.Auth` 的 `useProxy` 字段与 `UseProxy/SetUseProxy`；
   - `Config` 的 `proxy_url` / `resin_*` 字段与 normalize 分支；
   - `main.go` 的 `SetProxy` / panel 注入 / restart 列表；
   - `panel` 的代理开关路由与 `tempIdentity` + inherit 逻辑。
3. `internal/proxy/`、`internal/upstream/proxy.go`、`internal/panel` 内的
   `tempIdentity` + inherit 逻辑通常无冲突，可直接保留。
4. 合并后运行第 6 节测试；确认未配置代理时行为不变。

---

# 第二部分：裸模型名默认域 `model_default_realm`

本部分是另一处**独立**的低侵入改动（与代理无耦合，可分别摘除/合并）。

**背景**：模型名协议 `[realm:]model` 中，裸名历史上恒归 `cn` 域。纯 global 部署里
裸名请求会因「池内无 CN 号」报 503，必须写 `global:` 前缀。

**新增配置**（顶层键，默认 `cn` 零回归）：

```jsonc
"model_default_realm": "cn"   // cn（默认）| global | auto
```

- `cn`：裸名归国内版（历史行为）。
- `global`：裸名归国际版（纯 global 池免写前缀）。
- `auto`：按池内**可用账号域**自动判定（只有一个域有可用号时归该域，两域都有/
  都没有回落 `cn`）。
- 显式 `cn:` / `global:` 前缀**恒优先**，不受本项影响；非法值回落 `cn`。

## 2.1 改动点（同步冲突排查）

| 文件 | 改动 |
| --- | --- |
| `internal/server/resolve_model.go` | 重写：新增 `RealmResolver`（`Resolve` + `auto` 判定）；保留 `ResolveModel` 默认 cn 兼容面 |
| `internal/server/handler.go` | `Config` 增 `RealmResolver`；新增 `h.realmResolver()` 兜底；chat 路径改用 `h.realmResolver().Resolve(...)` |
| `internal/server/resolve_model_test.go` | 新增（含端到端选号域验证） |
| `cmd/server/wiring.go` | `realmAwareAvailableForModel` 多收一个 `*server.RealmResolver` 入参（**必须与 handler 同一实例**） |
| `cmd/server/main.go` | 构造 `realmResolver`（含池可用性回调），传给粘性闭包与 handler；`restartRequiredFields` 增 `model_default_realm` |
| `cmd/server/config.go` | 新增 `model_default_realm` 字段 + `WB2A_MODEL_DEFAULT_REALM` + `normalizeModelDefaultRealm`（非法回落 cn） |
| `internal/panel/app.js` / `index.html` | 配置页新增「裸模型名默认域」下拉 |
| `config.example.json` / `README.md` | 配置项与说明 |

## 2.2 一致性要点

handler 与 session 粘性闭包必须共用同一个 `RealmResolver`，否则粘性分配的账号域与
请求实际路由域可能错位。

## 2.3 `auto` 的口径

只按「池内该域有没有可用账号」判定（不查模型目录），因此 chat 路由与粘性路由天然
一致；双域都有号时裸名回落 `cn`，想指定国际版仍用 `global:` 前缀。

---

# 第三部分：客户端特征对齐、持久化指纹统一与版本自检

本部分是针对官方最新客户端（国内版 5.7.6 / 国际版 5.6.2）进行全量逆向分析后，
实施的**客户端特征精准对齐、持久化指纹一致性闭环与版本自检机制**。

**背景与解决的问题**：
1. **设备 ID 哈希盐值冲突消除**：
   - 旧代码中 `headers.go` 的 `deriveAccountStableID` 使用盐值 `"wb2a:machine:" + uid`，
     而 `desktop.go` 的 `deriveID` 使用 `"machine:" + uid`，导致同一账号在 API 交互与
     做活动遥测打点时，报出了两台完全不同的虚拟设备 ID。
   - **修复**：统一调用 `deriveAccountStableID`，API 请求头的 `X-Machine-ID`/`X-Session-ID`
     与遥测 Body 的 `machineId`/`sessionId` 达到 **100% 逐字相同**，跨重启稳定一致。
2. **多账号底层硬件特征离散化（防关联风控）**：
   - 旧代码中所有账号在遥测时硬编码固定为“20核、24G内存、Win 10.0.26220”，多账号池在
     同一节点打点时硬件特征完全雷同，极易触发聚集风控。
   - **修复**：实现基于账号 UID 确定性哈希散列的 `deriveHardwareProfile(uid string)`，
     多账号池打点时硬件特征（CPU 核心 8~24、内存 16~64GB、Windows 构建号）自然离散，
     且同一账号跨重启恒定不变，杜绝同质化聚集特征。
3. **出站版本号与官方最新发版对齐**：
   - 默认客户端版本常量从 `5.5.4` 提升至 `5.7.6`；内置 CLI 版本从 `2.137.1` 提升至 `2.156.0`。
   - 遥测上报中的 `commit` 升级为 `306add2a5dfafacf97769081cd17c3d172f6e60d`，
     `releaseDate` 升级为 `1791104628975`。
4. **国内主端点 Base URL 迁移**：
   - 官方已正式将国内主服务迁移至 `https://www.workbuddy.cn`（原 `codebuddy.cn` 根路径已 404）。
   - 将 `originRefererCN` 更新为 `https://www.workbuddy.cn`。
5. **补齐官方标准产品身份与分布式追踪头**：
   - `ChatHeaders` 补齐 `X-Private-Data: true` 与 `X-Agent-Intent: craft`（对齐官方 daemon 规范）。
   - `injectConversationHeaders` 补充标准 W3C `traceparent` 头（`00-${32hex}-${16hex}-01`）。
   - `ReportDesktopEvent` 遥测请求补齐图灵盾设备 Token 与稳定设备头。
6. **启动检查与面板配置页版本提示（不强改配置）**：
   - 新增 `version_check.go`，启动时在独立 goroutine 内非阻塞请求官方国内与海外更新 Feed，
     获取最新构建号并缓存至 `atomic.Pointer`。
   - 在面板配置接口 `getConfig` 输出 `version_info`，并在 Web 配置页的出站 User-Agent
     设置项旁展示只读提示，管理员可一目了然看到官方最新动态，完全保留用户对配置的控制权。

## 3.1 改动点清单（同步冲突排查）

| 文件 | 改动说明 |
| :--- | :--- |
| `internal/upstream/headers.go` | 默认版本号提升至 5.7.6 / 2.156.0；国内基础域迁至 workbuddy.cn；追加 X-Private-Data、X-Agent-Intent 及 W3C traceparent |
| `internal/upstream/desktop.go` | 废除私有 deriveID，统一调用 deriveAccountStableID；新增 deriveHardwareProfile 离散化硬件；补齐 X-Device-Token 与设备头 |
| `internal/upstream/version_check.go` | **新增模块**：官方客户端最新发版非阻塞启动检测与内存原子缓存 |
| `internal/upstream/version_check_test.go` | **新增测试**：版本解析、Semver 提取与默认值单测 |
| `internal/upstream/desktop_test.go` | 断言遥测 machineId/sessionId 与 API 请求头 100% 相同；断言硬件特征离散性与幂等性 |
| `internal/upstream/useragent_test.go` | 同步更新测试期望 UA 为 5.7.6 与 2.156.0 |
| `cmd/server/main.go` | 启动时异步触发 `upstream.StartVersionCheckAsync()` |
| `internal/panel/config.go` | `getConfig` 响应中附带 `version_info` 结构体 |
| `internal/panel/index.html` / `app.js` | 配置表单增设只读版本提示 Badge（不设 name 属性，不破坏既有单测） |
| `docs/WORKBUDDY_CLIENT_FEATURES_ANALYSIS.md` | **新增文档**：包含全量逆向分析、官方端点清单与风控时序的技术报告 |

## 3.2 验证方式

```bash
# 全量单元测试（含竞态检测）
go test -race ./internal/... ./cmd/...

# 静态代码检查
go vet ./...

# 编译验证
go build ./cmd/server
```

