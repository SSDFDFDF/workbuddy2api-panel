#!/usr/bin/env python3
"""从 WorkBuddy 官方安装包导出提示词模板（.tpl）与配套提示词资产。

为什么需要这个脚本
------------------
官方 Electron 安装包里的提示词是**明文 Jinja 模板**，位于 `resources/app.asar`，
但被 asar 头部标记为 `unpacked: true` —— 也就是说它们在磁盘上的真实位置是
`resources/app.asar.unpacked/...`（asar 里只留头索引）。这意味着：

  1. 可以直接抽真文件（不需要 asar extract，也就绕开了 unpacked 缺文件的报错）；
  2. asar 头部带每个文件的 SHA256 完整性哈希，可以**逐字节校验**导出结果。

脚本只做一件事：抽包 → 校验 → 按目录镜像落盘 → 写清单与溯源信息。不解析、
不改写模板内容，导出结果与官方文件字节一致。

导出范围（会随版本增减，脚本按 asar 头动态发现）
------------------------------------------------
  /resources/templates/**                          各模式 system prompt 模板
  /resources/plugins/workbuddy-builtin/prompt-common/**     跨模式共享片段
  /resources/plugins/workbuddy-builtin/interactionmode/**   各交互模式的 section 片段
  /resources/plugins/workbuddy-builtin/welcomemode/**       欢迎页多模式（code/design/work）

用法
----
  python3 scripts/extract-official-templates.py WorkBuddy-win32-x64-user-5.7.6.*.exe
  python3 scripts/extract-official-templates.py <exe> --out docs/official-templates/v5.7.6
  python3 scripts/extract-official-templates.py <exe> --version 5.7.6 --keep-tmp

退出码：0 成功（含"全部哈希校验通过"）；非 0 为抽取/校验失败。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import struct
import subprocess
import sys
import tempfile

# 导出范围：asar 头部里的绝对路径前缀（与安装目录无关，始终从 /resources 起算）
# interactionmode 下是各交互模式（ask/craft/expert/plan/quick）的 section 片段，
# 与模板一起构成最终 system prompt，缺了它就只能看到骨架、看不到分模式差异。
EXPORT_PREFIXES = (
    "/resources/templates/",
    "/resources/plugins/workbuddy-builtin/prompt-common/",
    "/resources/plugins/workbuddy-builtin/interactionmode/",
    "/resources/plugins/workbuddy-builtin/welcomemode/",
)

# asar 归档内的相对路径
ASAR_IN_ARCHIVE = "resources/app.asar"
UNPACKED_IN_ARCHIVE = "resources/app.asar.unpacked/resources"


def find_7z(explicit: str | None) -> str:
    """定位 7z 可执行文件（7za / 7z / 7zr 任一）。"""
    if explicit:
        if shutil.which(explicit) or os.path.isfile(explicit):
            return explicit
        sys.exit(f"找不到 7z 可执行文件：{explicit}")
    for name in ("7za", "7z", "7zr"):
        p = shutil.which(name)
        if p:
            return p
    sys.exit("找不到 7z（7za/7z/7zr）。Debian/Ubuntu: apt install p7zip-full；macOS: brew install p7zip")


def sha256_file(path: str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def read_asar_header(asar_path: str) -> dict:
    """读 asar 头部 JSON。

    格式：4 个小端 u32（pickle 头）→ header_size → JSON。字段含义不依赖具体
    取值，第 4 个 u32 即头部长度（与 @electron/asar 的 filesystem.js 一致）。
    """
    with open(asar_path, "rb") as f:
        raw = f.read(16)
        if len(raw) != 16:
            sys.exit(f"asar 头部不完整：{asar_path}")
        _a, _b, _c, header_size = struct.unpack("<4I", raw)
        if not (0 < header_size < len(raw) + 1 << 30):
            sys.exit(f"asar 头部长度异常（{header_size}）：{asar_path}")
        try:
            return json.loads(f.read(header_size).decode("utf-8"))
        except (UnicodeDecodeError, json.JSONDecodeError) as e:
            sys.exit(f"asar 头部解析失败：{e}")


def collect_targets(node: dict, path: str = "", out: list[tuple[str, dict]] | None = None):
    """递归收集命中导出前缀的文件节点，返回 [(asar 绝对路径, 节点)]。"""
    out = [] if out is None else out
    for name, child in (node.get("files") or {}).items():
        cur = f"{path}/{name}"
        if "files" in child:
            collect_targets(child, cur, out)
        elif any(cur.startswith(p) for p in EXPORT_PREFIXES):
            out.append((cur, child))
    return out


def extract(seven_zip: str, installer: str, dest_tmp: str) -> None:
    """用 7z 抽出 app.asar 与 app.asar.unpacked 下的提示词资产。"""
    patterns = [ASAR_IN_ARCHIVE, f"{UNPACKED_IN_ARCHIVE}/*"]
    cmd = [seven_zip, "x", installer, *patterns, f"-o{dest_tmp}", "-y"]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    # 7z 对"数据尾部/部分文件告警"返回非 0，只要关键产物在就继续——把判断权交给下面的校验。
    if not os.path.isfile(os.path.join(dest_tmp, ASAR_IN_ARCHIVE)):
        sys.exit("7z 抽取失败（未产出 app.asar）：\n" + (proc.stdout or "")[-2000:] + (proc.stderr or "")[-2000:])


def main() -> int:
    ap = argparse.ArgumentParser(description="导出 WorkBuddy 官方提示词模板（.tpl）与配套资产")
    ap.add_argument("installer", help="官方安装包路径（*win32-x64-user-*.exe）")
    ap.add_argument("--version", help="版本标签（缺省从安装包文件名解析，如 5.7.6）")
    ap.add_argument("--out", help="输出目录（缺省 docs/official-templates/v<version>）")
    ap.add_argument("--7z", dest="seven_zip", help="7z 可执行文件路径")
    ap.add_argument("--keep-tmp", action="store_true", help="保留临时目录（排查用）")
    args = ap.parse_args()

    if not os.path.isfile(args.installer):
        sys.exit(f"安装包不存在：{args.installer}")

    version = args.version
    if not version:
        m = re.search(r"-user-(\d+\.\d+\.\d+)", os.path.basename(args.installer))
        if not m:
            sys.exit("无法从文件名解析版本号，请显式传 --version")
        version = m.group(1)

    repo_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    out_dir = args.out or os.path.join(repo_root, "docs", "official-templates", f"v{version}")
    out_dir = os.path.abspath(out_dir)

    seven_zip = find_7z(args.seven_zip)
    installer_sha = sha256_file(args.installer)

    tmp = tempfile.mkdtemp(prefix="wb-tpl-")
    try:
        extract(seven_zip, args.installer, tmp)
        asar_path = os.path.join(tmp, ASAR_IN_ARCHIVE)
        unpacked_root = os.path.join(tmp, UNPACKED_IN_ARCHIVE)
        asar_sha = sha256_file(asar_path)

        header = read_asar_header(asar_path)
        targets = collect_targets(header)
        if not targets:
            sys.exit("asar 头部未找到任何提示词资产（导出前缀可能已随版本变更，请更新 EXPORT_PREFIXES）")

        if os.path.isdir(os.path.join(out_dir, "templates")):
            shutil.rmtree(out_dir)  # 全量替换，避免旧版本残留文件混入
        os.makedirs(out_dir, exist_ok=True)

        manifest: list[tuple[str, str]] = []
        mismatched: list[str] = []
        missing: list[str] = []

        for archive_path, node in sorted(targets):
            rel = archive_path[len("/resources/") :]
            src = os.path.join(unpacked_root, rel)
            if not node.get("unpacked"):
                missing.append(f"{archive_path}（asar 头未标记 unpacked，本脚本只处理真文件）")
                continue
            if not os.path.isfile(src):
                missing.append(archive_path)
                continue
            digest = sha256_file(src)
            expect = (node.get("integrity") or {}).get("hash")
            if expect and digest != expect:
                mismatched.append(f"{archive_path}\n    实际 {digest}\n    头部 {expect}")
                continue
            dst = os.path.join(out_dir, rel)
            os.makedirs(os.path.dirname(dst), exist_ok=True)
            shutil.copy2(src, dst)
            manifest.append((rel, digest))

        if missing or mismatched:
            for p in missing:
                print(f"  ✗ 缺失：{p}", file=sys.stderr)
            for p in mismatched:
                print(f"  ✗ 哈希不符：{p}", file=sys.stderr)
            sys.exit(f"导出失败：缺失 {len(missing)} / 哈希不符 {len(mismatched)}")

        # MANIFEST.sha256 —— 与 sha256sum -c 兼容
        with open(os.path.join(out_dir, "MANIFEST.sha256"), "w", encoding="utf-8") as f:
            for rel, digest in manifest:
                f.write(f"{digest}  {rel}\n")

        provenance = [
            f"installer: {os.path.basename(args.installer)}",
            f"installer_sha256: {installer_sha}",
            f"app.asar_sha256: {asar_sha}",
            f"version: {version}",
            "source: resources/app.asar.unpacked (asar 头部 unpacked=true 的真文件)",
            f"files: {len(manifest)}",
            "verify: sha256 与 app.asar 头部 integrity 逐文件比对，全部通过",
        ]
        with open(os.path.join(out_dir, "PROVENANCE.txt"), "w", encoding="utf-8") as f:
            f.write("\n".join(provenance) + "\n")

        total = sum(os.path.getsize(os.path.join(out_dir, r)) for r, _ in manifest)
        print(f"→ {os.path.relpath(out_dir, repo_root)}/")
        print(f"  {len(manifest)} 个文件 / {total / 1024:.0f} KiB，SHA256 全部校验通过（对齐 asar integrity）")
        print(f"  app.asar sha256: {asar_sha[:16]}…")
        return 0
    finally:
        if args.keep_tmp:
            print(f"  临时目录保留：{tmp}")
        else:
            shutil.rmtree(tmp, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
