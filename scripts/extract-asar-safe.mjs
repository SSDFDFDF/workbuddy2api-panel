#!/usr/bin/env node
// extract-asar-safe.mjs —— 逐文件安全提取 Electron app.asar（跳过 unpacked 条目）。
//
// 背景：WorkBuddy 官方安装包把 resources/templates/*.tpl 与
// resources/plugins/workbuddy-builtin/** 标成 `unpacked: true`（真文件在
// app.asar.unpacked/ 里，app.asar 只留索引）。此时 `asar extract` 会因为
// unpacked 树缺 cli/bin/* 而中途抛错，拿不到 main/ 与 cli/dist/ 里的代码。
// 本脚本用 @electron/asar 的 listPackage/extractFile 挨个提取，遇 unpacked
// 或缺失条目只记失败清单，不中断整体。
//
// 用法：
//   node scripts/extract-asar-safe.mjs <app.asar> <outdir> [前缀...]
//   node scripts/extract-asar-safe.mjs /tmp/exe562/resources/app.asar /tmp/p562_safe main resources /cli/dist
//
// 依赖 @electron/asar：优先从当前目录的 node_modules 解析，其次从
// $ASAR_MODULE_DIR（指向任意含 @electron/asar 的 node_modules 目录）。
// 提取模板/插件不必用本脚本 —— `7z x <installer.exe>` 直接拿 app.asar.unpacked
// 真文件更稳（见 docs/official-templates/README.md §2）。
//
// 输出：<outdir>/_extract_failures.txt（失败清单，目录条目属预期噪声）。

import fs from 'node:fs';
import path from 'node:path';
import { createRequire } from 'node:module';
import { pathToFileURL } from 'node:url';

const [archive, outDir, ...prefixes] = process.argv.slice(2);
if (!archive || !outDir) {
  console.error('usage: node extract-asar-safe.mjs <app.asar> <outdir> [prefix...]');
  process.exit(2);
}

async function loadAsar() {
  const candidates = [];
  if (process.env.ASAR_MODULE_DIR) {
    candidates.push(path.join(process.env.ASAR_MODULE_DIR, '@electron/asar/lib/asar.js'));
    candidates.push(path.join(process.env.ASAR_MODULE_DIR, '@electron/asar/lib/asar.mjs'));
  }
  try {
    const req = createRequire(pathToFileURL(path.join(process.cwd(), 'noop.cjs')));
    candidates.push(path.join(path.dirname(req.resolve('@electron/asar/package.json')), 'lib/asar.js'));
  } catch { /* 忽略，继续找 */ }
  candidates.push('/home/xjc/.local/share/pi-node/node-v22.23.1-linux-x64/lib/node_modules/@electron/asar/lib/asar.js');
  for (const c of candidates) {
    if (c && fs.existsSync(c)) return await import(pathToFileURL(c).href);
  }
  throw new Error('cannot locate @electron/asar; set ASAR_MODULE_DIR to a node_modules containing it');
}

const asar = await loadAsar();
const { listPackage, extractFile } = asar;

const normPrefixes = prefixes.map((p) => p.replace(/^\/+/, ''));
const files = listPackage(archive, { isPack: false })
  .filter((f) => {
    const rel = f.replace(/^\/+/, '');
    return !normPrefixes.length || normPrefixes.some((p) => rel === p || rel.startsWith(p));
  });

let ok = 0;
let fail = 0;
const failed = [];
for (const f of files) {
  const rel = f.replace(/^\//, '');
  try {
    const buf = extractFile(archive, rel);
    const dest = path.join(outDir, rel);
    fs.mkdirSync(path.dirname(dest), { recursive: true });
    fs.writeFileSync(dest, buf);
    ok++;
  } catch (e) {
    fail++;
    failed.push(`${rel} :: ${String(e.message).split('\n')[0]}`);
  }
}

console.log(`ok=${ok} fail=${fail} (${files.length} matched, prefixes=${prefixes.join(',') || '*'})`);if (failed.length) {
  fs.mkdirSync(outDir, { recursive: true });
  fs.writeFileSync(path.join(outDir, '_extract_failures.txt'), failed.join('\n') + '\n');
  console.log('first failures:\n' + failed.slice(0, 5).join('\n'));
}
