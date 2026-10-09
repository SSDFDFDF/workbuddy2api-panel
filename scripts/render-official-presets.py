#!/usr/bin/env python3
"""离线渲染官方插件组合模板 → 静态预设正文（literal MD）。

背景
----
网关预设**不做任何运行期模板渲染**：`internal/prompt/presets/*.md` 必须是
纯静态成品正文（无 `{{ }}`、无 `{% %}`）。本脚本是**构建期工具**，负责把官方
安装包里的明文 Nunjucks 模板跑一遍，产出静态文件。

装配对象是官方**路径 B**（抓包实证走的就是这条）：
    welcomemode-<code|work|design>/prompt.tpl
      └── {% include "interactionmode-<mode>/fragments/*.md" %}
      └── {% include "prompt-common/fragments/*.md" %}

判定规则（对齐口径：文字 1:1 还原，删除条件内容、可变内容）
--------------------------------------------------------------
1. **可判定的条件** → 求值后内联，保留官方原文（1:1）：
   workMode 轴、realm 轴（ResponseLanguage / IsOversea / productFeatures.*）。
2. **网关不可知的条件**（IsWindows / 客户端功能开关 / 客户端本地状态）→ 按实物
   抓包取值（抓包是行为事实，优先于模板声明）；无抓包依据又无意义的直接删。
   例：`IsWindows` 在模板里存在，但真实客户端没人写它（抓包无 windows_command_safety）
   → 取 False，即该块不出现。
3. **域常量变量**（productName / dataFolderName / modelName / ResponseLanguage）
   → 替换为字面值（该值来自官方，跨部署恒定）。modelId 故意**不赋值**：
   官方首行的三档 Auto 规则在两份实物抓包里都未生效（见 prompts 注释）。
4. **客户端本地变量**（BinaryContext / WorkbuddyMemoryDir / *MemoryContent /
   ToolResultPresentationPrompt / PluginAgentPrompt …）→ **删除**：这些值只存在于
   客户端那台机器上，写死任何取值都是伪造。

产物校验
--------
`--verify` 把渲染结果与抓包正文对比（两侧都先过 `finalize`，所以空行不算差异）。
预期差异 **只剩“已声明删除”**，即 `declared_deletion_lines` 标出的三类：

  a) `<binary_context>` 整块（机器运行时清单：装了哪些 node/python、装在哪）；
  b) `memory_system` 的 Layer 3 整块（含 {{ WorkbuddyMemoryDir }} 工作区绝对路径）；
  c) 用户级记忆目录的本地 UUID → 官方可移植写法 `~/.workbuddy[-ai]`。

除这三类以外出现任何差异，都说明渲染器与官方行为不一致，必须查清楚再改产物。

`--emit-all` 会先跑这套校验，**不通过就不写任何文件**（避免把渲染器的 bug
落成“看起来像官方原文”的产物）。

> 这套校验不是摆设：它头一次跑到通就报出了两处真 bug —— ① CN 5.7.6 的
> `LocalSkillsMemoryEnabled` 与 Global 不同（CN 只有 Layer 1，若按 Global 取值
> 会凭空给 CN 多送出两段记忆说明）；② 两份模板在“删除到哪一行”上的边界分歧
> （一个包含闭合标签、一个不包含）。两者都已修正，见 BUILD_FACTS 与 POST_DELETE。

用法
----
    # 生成全部 official-*.md（不碰手写的 default.*.md）
    python3 scripts/render-official-presets.py --template-root docs/official-templates/v5.6.2 \
        --emit-all internal/prompt/presets

    # 单片校验（与抓包对比）
    python3 scripts/render-official-presets.py --template-root docs/official-templates/v5.6.2 \
        --realm global --mode craft --response-language-from docs/CAPTURED_OFFICIAL_GLOBAL_SYSTEM_PROMPT.txt \
        --verify docs/CAPTURED_OFFICIAL_GLOBAL_SYSTEM_PROMPT.txt
"""

from __future__ import annotations

import argparse
import difflib
import os
import re
import sys

# --------------------------------------------------------------------------
# realm × mode 事实表
# --------------------------------------------------------------------------

# 域常量：来自官方产品配置 / 抓包，跨部署恒定。
REALM_VARS = {
    "global": {
        "productName": "WorkBuddy AI",
        "dataFolderName": ".workbuddy-ai",
        "WorkbuddyDataFolderName": ".workbuddy-ai",
        # 用户级记忆目录：官方 Global 片段直接写 `~/{{ WorkbuddyDataFolderName }}`，
        # CN 分支换成了这个变量（实填绝对路径 + user-<uuid>）。取官方的可移植写法，
        # 既去掉 UUID 指纹，又与 Global 分支同义。
        "WorkbuddyUserMemoryDir": "~/.workbuddy-ai",
        "modelName": "default-model",
        "ResponseLanguage": None,  # 由抓包文字决定，见 set_response_language
        "IsOversea": False,  # 抓包实证：Global 5.6.2 含 regional_conventions
    },
    "cn": {
        "productName": "WorkBuddy",
        "dataFolderName": ".workbuddy",
        "WorkbuddyDataFolderName": ".workbuddy",
        "WorkbuddyUserMemoryDir": "~/.workbuddy",
        "modelName": "快速",
        "ResponseLanguage": None,
        "IsOversea": False,
    },
}

# 客户端本地变量：值只存在于客户端那台机器 → 整块删除。
# 映射到「哪个 include / 哪个 if 块要被丢掉」由 DELETED_BLOCKS 决定。
CLIENT_LOCAL_VARS = {
    "BinaryContext",
    "WorkingMemoryContent",
    "UserLocalMemoryContent",
    "UserMemoryContent",
    "WorkbuddyMemoryDir",
    "WorkbuddyMemory_1",
    "ToolResultPresentationPrompt",
    "PluginAgentPrompt",
    "SoulPath",
    "SoulContent",
    "BootstrapPath",
    "BootstrapContent",
    "IdentityPath",
    "IdentityContent",
    "UserPath",
    "UserContent",
    "ToneStyleContent",
    "UserCustomPrompt",
    "industryModeSystemPromptAppend",
    "ExpertManagement",
    "ArtifactDirectoryPath",
    "subAgentPrompt",
    "ClawMemory_1",
    "ClawMemory_2",
    "ClawMemory_3",
}

# --------------------------------------------------------------------------
# 构建级开关：**两份抓包实测不同**，所以按 realm 分开。
# --------------------------------------------------------------------------
#
# 不要想当然地认为“同一份模板 ⇒ 同一份渲染结果”：实测证据就在抓包里。
# 这也是 verify 存在的意义——它第一轮跑就把这里抓了出来。
BUILD_FACTS = {
    # Global 5.6.2
    "global": {
        "LocalSkillsMemoryEnabled": True,  # 抓包含 Layer 2/3 与 "three independent memory layers"
        "IsPersonalEdition": False,        # 抓包无 <agent_mail>
    },
    # CN 5.7.6
    "cn": {
        "LocalSkillsMemoryEnabled": False,  # 抓包只有 Layer 1（无 Layer 2/3、无三层提示句）
        "IsPersonalEdition": True,         # 抓包含 <agent_mail>（IsPersonalEdition 门控）
    },
}

# 与构建无关的事实（两份抓包一致，或网关侧恒定）。
FEATURE_FACTS = {
    "ExpertManagementEnabled": True,   # 两份抓包都有 <expert_management>
    "IsWindows": False,                 # 悬空变量：真实客户端没人写它（抓包无 windows_command_safety）
    "WorkspaceIdentityMode": "",        # 仅 user-context 模板用
    "productFeatures.DisableMultimodalGeneration": False,  # 抓包含多模态能力行
    "productFeatures.BrowserUse": False,   # 网关不声明 browser-use 运行时
    "productFeatures.ComputerUse": False,  # 网关不声明 computer-use 运行时
    "BrowserUseEnabled": False,         # 网关不声明 browser-use 运行时
    "ComputerUseEnabled": False,        # 网关不声明 computer-use 运行时
}

# 变量表与「客户端本地变量漏网」名单：每次 build 重置。
VARS: dict[str, str] = {}
# UNKNOWN_VARS 只用于自查：正常路径下客户端本地变量都会被 {}if 拦住；
# 万一某个变量在无守卫处直接出现，会落进这里（而不是静默变成空串）。
UNKNOWN_VARS: set[str] = set()

TOKEN_RE = re.compile(r"(\{\{.*?\}\}|\{%-?.*?-?%\}|\{#.*?#\})", re.S)


class RenderError(RuntimeError):
    pass


# --------------------------------------------------------------------------
# 空行：统一折叠（有意偏离抓包的逐字空行）
# --------------------------------------------------------------------------
#
# 空行是**条件块留下的空档**，不是内容。官方模板里到处都是这类结构：
#
#     {% if BinaryContext %}
#     <binary_context>{{ BinaryContext }}</binary_context>
#     {% endif %}
#
#     IMPORTANT: ...
#
# 条件不成立时，块没了、它的换行还在。于是：
#   - 两份抓包在同一位置就不一样（Global 抓到 3 个换行、CN 抓到 2 个：
#     Global 5.6.2 全篇 11 处 2–4 个连续空行；CN 5.7.6 全篇 0 处 —— CN 构建
#     自己做了折叠）；
#   - 未经抓包对照的模式（ask/quick/expert）会残留更大的空档：Global
#     `official-quick` 出现过 **20 个连续空行**、`official-ask` 出现过 8 个。
#
# 既然空行不影响 markdown/XML 语义、也不承载信息，本脚本统一折叠为最多一个空行，
# 让所有预设干净且一致。1:1 的口径因此定义为 **非空行逐字一致**（verify 也按
# 同一规则归一后再比，见 verify_against_capture）。
MAX_BLANK_RUN = 2  # 连续换行上限：2 = 恰好一个空行
# 客户端本地用户目录（绝对路径 + user-<uuid>）→ 官方可移植写法。
# `[0-9a-fA-F-]*[0-9a-fA-F]` 而不是 `[0-9a-fA-F-]+`：后者会把 `-personal` 前面那个
# 连字符也吃掉（`personal` 里的 p/r/s/o/n/l 不在字符类里，正则到此成功、不再回溯），
# 结果是匹配到 `...abd9-` 而把 `personal` 留在原地 —— 改写后会多出一截。
LOCAL_UUID_RE = re.compile(
    r"[A-Za-z]:\\[^\s`]*?\\(?:user-[0-9a-fA-F-]*[0-9a-fA-F](?:-personal)?)"
)
# 官方自己的可移植写法（Global 分支本来就这么写）：`~/.workbuddy[-ai]`。
PORTABLE_DIR_RE = re.compile(r"~/\.workbuddy(?:-ai)?")


def rewrite_local_uuid(text: str, realm: str) -> str:
    """把 `C:\\Users\\<用户>\\.workbuddy\\user-<uuid>-personal` 换回 `~/.workbuddy[-ai]`。

    官方 Global 分支本来就写 `~/<dataFolderName>`（无绝对路径），CN 分支才用绝对
    路径；这里取官方自己的可移植写法，同时去掉本地用户 UUID。
    """
    portable = "~/" + REALM_VARS[realm]["WorkbuddyUserMemoryDir"].split("/", 1)[1]
    return LOCAL_UUID_RE.sub(portable, text)


def canon_local_uuid(line: str) -> str:
    """把行里的“本地用户目录”统一成占位符，用于“只差 UUID 改写”的比对。

    两种写法要归一到同一个占位符，否则“改前/改后”永远比不相等：
      改写前（CN 抓包）：C:\\Users\\<用户>\\.workbuddy\\user-<uuid>-personal
      改写后（我们的产物）：~/.workbuddy
    """
    return PORTABLE_DIR_RE.sub("<LOCALDIR>", LOCAL_UUID_RE.sub("<LOCALDIR>", line))


def finalize(text: str) -> str:
    """收尾：CRLF→LF、连续空行折叠为一个、首尾去空、结尾恰好一个换行。

    幂等：对已经收尾过的文本再跑一次结果不变（verify 两侧都过一遍）。
    """
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    text = re.sub(r"\n{%d,}" % (MAX_BLANK_RUN + 1), "\n" * MAX_BLANK_RUN, text)
    return text.strip("\n") + "\n"


def declared_deletion_lines(cap_lines: list[str]) -> set[int]:
    """抓包正文里「已声明删除/改写」的行号集合（0 基），供 verify 做独立判定。

    声明一共只有三处（与 README 「已删除的内容」表一一对应）：

      a) `<binary_context>` 整块（机器运行时清单：装了哪些 node/python、装在哪）；
      b) `# Layer 3 — Workspace Memory` 到 `</memory_system>` **之前**为止
         （闭合标签保留：块里还有 Layer 1/2）；
      c) 含本地 `user-<uuid>` 的行（只改写为可移植写法，不删）。

    另外**紧贴**声明块的连续空行也算允许：删掉一整块必然吃掉它与相邻文本之间的
    空行分隔符，这属于同一处删除的副作用，不是渲染器多删。只放宽“紧贴”，
    不会掩盖别处的空行问题。
    """
    n = len(cap_lines)
    regions: list[tuple[int, int]] = []

    def find_close(start: int, prefix: str) -> int:
        """从 start 起找第一条以 prefix 开头的行；找不到返回 -1。"""
        for j in range(start, n):
            if cap_lines[j].startswith(prefix):
                return j
        return -1

    for i, line in enumerate(cap_lines):
        if line.startswith("<binary_context>"):
            j = find_close(i, "</binary_context>")
            if j != -1:
                regions.append((i, j))  # 整块含标签
        elif line.startswith("# Layer 3 — Workspace Memory (read/write)"):
            j = find_close(i, "</memory_system>")
            if j != -1:
                regions.append((i, j - 1))  # 到闭合标签之前

    allowed: set[int] = set()
    for start, end in regions:
        allowed.update(range(start, end + 1))
        j = start - 1
        while j >= 0 and not cap_lines[j].strip():
            allowed.add(j)
            j -= 1
        j = end + 1
        while j < n and not cap_lines[j].strip():
            allowed.add(j)
            j += 1

    for i, line in enumerate(cap_lines):
        if LOCAL_UUID_RE.search(line):
            allowed.add(i)
    return allowed


def load(root: str, name: str) -> str:
    """解析 include 名 → plugins/workbuddy-builtin 下的真实路径。

    片段开头的 YAML frontmatter（name/description/tools）由 CLI 的
    `unionTools` 消费掉、**不进正文**，所以这里也要剥掉。
    """
    # interactionmode-ask/fragments/x.md → interactionmode/ask/fragments/x.md
    first, sep, rest = name.partition("/")
    m = re.match(r"^(interactionmode|welcomemode)-(.+)$", first)
    if m:
        first = f"{m.group(1)}/{m.group(2)}"
    path = os.path.join(root, "plugins", "workbuddy-builtin", first, rest)
    if not os.path.isfile(path):
        raise RenderError(f"include 不存在: {name} → {path}")
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    if text.startswith("---\n"):
        end = text.find("\n---", 3)
        if end != -1:
            nl = text.find("\n", end + 1)
            text = text[nl + 1 :] if nl != -1 else ""
    # nunjucks 的 include 默认 keepTrailingNewline=false：吃掉被包含文件末尾那一个 \n。
    # 只吃 \n、不吃 \r（官方片段多为 CRLF，这个细节直接决定空行数要不要对得上抓包）。
    if text.endswith("\n"):
        text = text[:-1]
    return text


def eval_cond(expr: str) -> bool:
    """求值 `{% if %}` 条件。只支持官方模板里出现的那几种形态。"""
    expr = expr.strip()

    # not X
    m = re.match(r"^not\s+([A-Za-z_][A-Za-z0-9_.]*)$", expr)
    if m:
        return not eval_cond(m.group(1))

    # A and B
    if " and " in expr:
        return all(eval_cond(p) for p in expr.split(" and "))
    # A or B
    if " or " in expr:
        return any(eval_cond(p) for p in expr.split(" or "))

    # '中文' in ResponseLanguage
    m = re.match(r"^'(.*)'\s+in\s+([A-Za-z_][A-Za-z0-9_.]*)$", expr)
    if m:
        needle, var = m.group(1), m.group(2)
        return needle in VARS.get(var, "")

    # X == / === true|false（5.7.6 模板混入了 JS 风格的三等号与布尔字面量）
    m = re.match(r"^([A-Za-z_][A-Za-z0-9_.]*)\s*={2,3}\s*(true|false)$", expr)
    if m:
        var, want = m.group(1), m.group(2) == "true"
        got = VARS.get(var, FEATURE_FACTS.get(var, False))
        return bool(got) is want

    # X == / === "value"
    m = re.match(r'^([A-Za-z_][A-Za-z0-9_.]*)\s*={2,3}\s*"([^"]*)"$', expr)
    if m:
        var, val = m.group(1), m.group(2)
        if var in VARS:
            return VARS[var] == val
        # 未定义变量按 nunjucks 语义当 undefined 处理（不等于任何字符串）。
        # 注意：首行的 modelId 三档规则就是这样失效的 —— 第一阶段渲染时 modelId
        # 还未注入，`{% elif modelId == "fast-model" %}` 已落空，第二阶段补变量也
        # 救不回来。所以这里**故意**不给 modelId 取值，与抓包实证一致。
        return FEATURE_FACTS.get(var, "") == val

    # X != "value"
    m = re.match(r'^([A-Za-z_][A-Za-z0-9_.]*)\s*!=\s*"([^"]*)"$', expr)
    if m:
        var, val = m.group(1), m.group(2)
        if var in VARS:
            return VARS[var] != val
        return FEATURE_FACTS.get(var, "") != val

    # 裸变量（真值判定）
    m = re.match(r"^([A-Za-z_][A-Za-z0-9_.]*)$", expr)
    if m:
        var = m.group(1)
        if var in CLIENT_LOCAL_VARS:
            return False  # 客户端本地内容 → 一律删除
        if var in VARS:
            return bool(VARS[var])
        if var in FEATURE_FACTS:
            return bool(FEATURE_FACTS[var])
        raise RenderError(f"未知条件变量: {expr}")

    raise RenderError(f"无法求值的条件: {expr!r}")


def render(text: str, root: str, depth: int = 0) -> str:
    """渲染一段模板。include 递归解析，if/elif/else 求值内联。"""
    if depth > 12:
        raise RenderError("include 递归过深")

    out: list[str] = []
    stack: list[dict] = []  # {active, taken, parent_active}

    def active() -> bool:
        return all(f["active"] for f in stack)

    def emit(s: str) -> None:
        if active() and s:
            out.append(s)

    parts = TOKEN_RE.split(text)
    i = 0
    while i < len(parts):
        tok = parts[i]
        i += 1

        if not tok:
            continue

        if tok.startswith("{{"):
            name = tok[2:-2].strip()
            if not active():
                continue
            if name in VARS:
                emit(str(VARS[name]))
            elif name in CLIENT_LOCAL_VARS:
                UNKNOWN_VARS.add(name)  # 由外层 if 负责删除；漏到这里记一笔
            else:
                raise RenderError(f"未知变量: {{{{{name}}}}}")
            continue

        if tok.startswith("{#"):
            # nunjucks 注释：整段丢弃（官方只用来占位，例如 expert 的
            # `{# {{ PluginAgentPrompt }} #}` —— 专家人格由客户端 collector 注入，
            # 不在模板里；网关拿不到，留空即官方语义）。
            if not active():
                continue
            continue

        if not tok.startswith("{%"):
            emit(tok)
            continue

        # 空白控制：{%- 去左侧空白，-%} 去右侧空白
        strip_left = tok.startswith("{%-")
        strip_right = tok.endswith("-%}")
        inner = tok[2:-2]
        if inner.startswith("-"):
            inner = inner[1:]
        if inner.endswith("-"):
            inner = inner[:-1]
        inner = inner.strip()
        if strip_left and out:
            out[-1] = out[-1].rstrip()
        # 右侧空白控制作用于随后的文本 token
        pending_rstrip = strip_right

        if inner.startswith("include"):
            m = re.match(r'^include\s+"([^"]+)"$', inner)
            if not m:
                raise RenderError(f"无法解析 include: {inner!r}")
            if active():
                body = render(load(root, m.group(1)), root, depth + 1)
                emit(body)
            if pending_rstrip and i < len(parts) and parts[i].startswith(("{{", "{%")) is False:
                parts[i] = parts[i].lstrip()
            continue

        if inner.startswith("if "):
            expr = inner[3:]
            parent = active()
            val = eval_cond(expr) if parent else False
            stack.append({"active": parent and val, "taken": val, "parent_active": parent})
            if pending_rstrip and i < len(parts):
                parts[i] = parts[i].lstrip()
            continue

        if inner.startswith("elif "):
            if not stack:
                raise RenderError("elif 无对应 if")
            expr = inner[5:]
            frame = stack[-1]
            parent = all(f["active"] for f in stack[:-1])
            if frame["taken"] or not parent:
                frame["active"] = False
            else:
                val = eval_cond(expr)
                frame["active"] = val
                frame["taken"] = frame["taken"] or val
            continue

        if inner == "else":
            if not stack:
                raise RenderError("else 无对应 if")
            frame = stack[-1]
            parent = all(f["active"] for f in stack[:-1])
            frame["active"] = parent and not frame["taken"]
            frame["taken"] = True
            continue

        if inner == "endif":
            if not stack:
                raise RenderError("endif 无对应 if")
            stack.pop()
            continue

        if inner.startswith("for "):
            raise RenderError(f"模板含未支持的 for: {inner!r}")

        raise RenderError(f"未知标签: {inner!r}")

    if stack:
        raise RenderError(f"if 未闭合，残留 {len(stack)} 层")
    return "".join(out)


# 渲染后的定点删除：（起止标记，理由）。
# 这些段落本身是官方的，但内容全部由客户端本地变量填成（绝对路径），
# 写死任何取值都是伪造 —— 按“删除可变内容”口径整段删。
POST_DELETE = [
    (
        "# Layer 3 — Workspace Memory (read/write)",
        "</memory_system>",
        "含 {{ WorkbuddyMemoryDir }} 工作区绝对路径（客户端本地）",
    ),
]
# 语义：删 [起标记, 止标记) —— **不含**止标记本身。Layer 3 是 memory_system 的
# 最后一段，闭合标签 </memory_system> 必须保留（块里还有 Layer 1/2）。


def post_delete(text: str) -> str:
    for start_marker, end_marker, reason in POST_DELETE:
        s = text.find(start_marker)
        if s == -1:
            continue
        e = text.find(end_marker, s)
        if e == -1:
            raise RenderError(f"POST_DELETE {start_marker!r} 找不到结束标记 {end_marker!r}")
        text = text[:s] + text[e:]
    return text


def postprocess(text: str, realm: str) -> str:
    """渲染产物 → 静态预设正文（唯一后处理管线）。

    三步，顺序固定：
      1) `post_delete`  —— 删掉“声明删除”的整块（客户端本地内容）；
      2) `rewrite_local_uuid` —— 本地用户 UUID 换回官方可移植写法；
      3) `finalize`     —— CRLF→LF、按该构建策略处理空行、收尾恰好一个换行。

    `<binary_context>` 不在这里处理：它在模板里由 `{% if BinaryContext %}`
    守卫，而 BinaryContext 是客户端本地变量（恒不成立），渲染时整块本就不出现。
    """
    return finalize(rewrite_local_uuid(post_delete(text), realm))


def assemble(root: str, realm: str, mode: str, welcome: str) -> str:
    """模板 → 静态预设正文。**所有预设都走这一条路径**（包括 craft）。

    为什么 craft 也重新渲染而不用抓包正文：两条路径会产生两份可能漂移的实现
    （历史上就出现过“一个删到包含闭合标签、一个不包含”的分歧）。抓包只用来
    **校验**（见 verify_against_capture），不用来生成。
    """
    return postprocess(build(realm, mode, welcome, root), realm)


# 预设名 → 官方 workMode（全部由模板渲染同一条管线生成）。
#
# 注意：`default.<realm>.md` **不在此表**——它是自设计位，由维护者手写，
# 本脚本永远不会碰它（--emit-all 只写 official-* 前缀的文件）。
def verify_against_capture(
    render_text: str, capture_path: str, realm: str
) -> tuple[bool, list[str], int]:
    """渲染结果 vs 抓包：**所有**差异必须落在「已声明删除」里。

    这里是独立判定，不复用生成逻辑（生成是“按声明去删”，判定是“查有没有多删/少删”）：
      - 抓包侧被删/被改的行 → 必须命中 declared_deletion_lines；
      - 渲染侧多出的行   → 一律算错（说明渲染器凭空造了内容）；
      - replace 且行数相同 → 允许“只差本地 UUID 改写”的情形。

    两侧都先过 finalize（含空行折叠），所以空行多少不算差异；这不掩盖错位——
    非空行必须逐行对上，多一行少一行都会报出来。

    返回 (是否通过, 问题列表, 被删行数)。
    """
    with open(capture_path, encoding="utf-8") as fh:
        cap_lines = finalize(fh.read()).split("\n")
    got_lines = render_text.split("\n")
    allowed = declared_deletion_lines(cap_lines)

    problems: list[str] = []
    removed = 0
    matcher = difflib.SequenceMatcher(None, cap_lines, got_lines, autojunk=False)
    for tag, i1, i2, j1, j2 in matcher.get_opcodes():
        if tag == "equal":
            continue
        if tag == "delete":
            for idx in range(i1, i2):
                removed += 1
                if idx not in allowed:
                    problems.append(f"抓包 L{idx+1} 被删但未声明: {cap_lines[idx][:110]}")
            continue
        if tag == "insert":
            for idx in range(j1, j2):
                problems.append(f"渲染多出 L{idx+1}: {got_lines[idx][:110]}")
            continue
        # replace
        if (i2 - i1) != (j2 - j1):
            problems.append(f"抓包 L{i1+1}..L{i2}: 行数不匹配（{i2-i1} -> {j2-j1}）")
            continue
        for k in range(i2 - i1):
            ci, ri = i1 + k, j1 + k
            removed += 1
            if ci in allowed and canon_local_uuid(cap_lines[ci]) == canon_local_uuid(got_lines[ri]):
                continue
            problems.append(f"抓包 L{ci+1} 被改但未声明: {cap_lines[ci][:90]} → {got_lines[ri][:90]}")
    return (not problems), problems, removed


PRESET_SPECS = [
    ("official-craft", "craft"),
    ("official-ask", "ask"),
    ("official-plan", "plan"),
    ("official-quick", "quick"),
    ("official-expert", "expert"),
]

REALM_SOURCES = {
    "global": {
        "root": "docs/official-templates/v5.6.2",
        "capture": "docs/CAPTURED_OFFICIAL_GLOBAL_SYSTEM_PROMPT.txt",
    },
    "cn": {
        "root": "docs/official-templates/v5.7.6",
        "capture": "docs/CAPTURED_OFFICIAL_SYSTEM_PROMPT.txt",
    },
}


def emit_all(out_dir: str) -> int:
    os.makedirs(out_dir, exist_ok=True)
    failed = False
    for realm, src in REALM_SOURCES.items():
        REALM_VARS[realm]["ResponseLanguage"] = read_capture_response_language(src["capture"])
        # 先自检：craft 产物必须与抓包只差“已声明删除”。不通过就**不写任何文件**，
        # 避免把渲染器的 bug 落成产物（产物一旦进仓库就会被当成“官方原文”）。
        rendered = assemble(src["root"], realm, "craft", "code")
        ok, problems, removed = verify_against_capture(rendered, src["capture"], realm)
        if not ok:
            print(f"❌ {realm}: craft 与抓包不一致（{len(problems)} 处，查渲染器）")
            for prob in problems[:20]:
                print("   " + prob)
            failed = True
            continue
        print(f"✓ {realm}: craft 与抓包逐行一致（{len(rendered.splitlines())} 行；差异 {removed} 行，全部已声明）")
        for name, mode in PRESET_SPECS:
            body = assemble(src["root"], realm, mode, "code")
            path = os.path.join(out_dir, f"{name}.{realm}.md")
            with open(path, "w", encoding="utf-8", newline="\n") as fh:
                fh.write(body)
            print(f"  {path:56s} {len(body):6d} 字符")
    return 1 if failed else 0


def read_capture_response_language(path: str) -> str:
    """从抓包正文里取 <response_language> 原文（域常量，跨部署恒定）。"""
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    m = re.search(r"^<response_language>\n(.*?)\n</response_language>", text, re.S | re.M)
    if not m:
        raise RenderError(f"抓包里找不到 <response_language>: {path}")
    return m.group(1)


def build(realm: str, mode: str, welcome: str, root: str) -> str:
    VARS.clear()
    UNKNOWN_VARS.clear()
    VARS.update({k: v for k, v in REALM_VARS[realm].items() if v is not None})
    VARS.update(FEATURE_FACTS)
    VARS.update(BUILD_FACTS[realm])
    VARS["workMode"] = mode
    VARS["welcomeMode"] = welcome
    VARS["interactionMode"] = mode
    body = render(load(root, f"welcomemode-{welcome}/prompt.tpl"), root)
    body = post_delete(body)
    return body


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--template-root", required=True)
    ap.add_argument("--out")
    ap.add_argument("--verify")
    ap.add_argument("--emit-all", metavar="OUTDIR", help="生成全部静态预设到目录")
    ap.add_argument("--mode", default="craft")
    ap.add_argument("--welcome", default="code")
    ap.add_argument("--realm", default="global")
    ap.add_argument("--response-language-from")
    args = ap.parse_args()

    if args.emit_all:
        return emit_all(args.emit_all)

    if args.response_language_from:
        REALM_VARS[args.realm]["ResponseLanguage"] = read_capture_response_language(
            args.response_language_from
        )

    rendered = assemble(args.template_root, args.realm, args.mode, args.welcome)

    if args.verify:
        # 只有 craft 有实物抓包可比；其余模式走模板渲染，无对照物。
        # 比对口径：空白行按该构建策略归一后，差异必须全是“已声明删除”。
        ok, problems, removed = verify_against_capture(rendered, args.verify, args.realm)
        if ok:
            print(
                f"✅ {args.realm}/{args.mode}: 与抓包逐行一致"
                f"（{len(rendered.splitlines())} 行；差异 {removed} 行，全部已声明）"
            )
            return 0
        print(f"❌ {args.realm}/{args.mode}: 与抓包不一致（{len(problems)} 处）")
        for p in problems[:60]:
            print("   " + p)
        return 1

    if args.out:
        os.makedirs(os.path.dirname(args.out) or ".", exist_ok=True)
        with open(args.out, "w", encoding="utf-8", newline="\n") as fh:
            fh.write(rendered)
        print(f"写出 {args.out}  ({len(rendered)} 字符)")
        return 0

    sys.stdout.write(rendered)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
