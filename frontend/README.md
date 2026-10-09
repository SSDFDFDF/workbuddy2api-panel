# 前端源码目录（React + Tailwind v4 + Vite + TypeScript）

面板前端的独立工程。源码在这里，**构建产物**输出到 `internal/panel/web/`，
由 Go 的 `go:embed` 嵌进二进制——`go:embed` 只能嵌包目录内的文件，
所以产物目录必须落在 panel 包下。

## 目录结构

```
frontend/
├── index.html          # 唯一 HTML（构建后产物同名）
├── package.json
├── tsconfig.json
├── vite.config.ts      # base:'./' + 固定产物名三件套
└── src/
    ├── main.tsx        # React 入口
    ├── tokens.css      # 设计令牌（明/暗主题 CSS 变量）+ Tailwind 注册
    ├── types.ts        # 后端 /panel/api/* 的 TS 类型
    ├── api.ts          # fetch 封装（Bearer / 401 密钥门）
    ├── fmt.ts          # 数值/时间格式化（fmtTok/dur/ago…）
    ├── qr.ts           # 精简 QR 编码器（券码二维码，CSP 禁外链）
    ├── toast.tsx       # 全局 toast
    ├── theme.tsx       # 明暗主题切换（localStorage）
    ├── components/     # SegBar / StackedBars / Dialog / TimeRange …
    └── views/          # 7 个视图组件
```

## 常用命令

```bash
cd frontend
npm install        # 首次
npm run build      # 产物 → ../internal/panel/web
npm run dev        # 本地开发（5173 端口，API 代理到 127.0.0.1:7863）
npm run watch      # 持续构建（配合 go run 调试）
```

构建集成：仓库根 `make build` 会先跑 `npm run build`（有 node 时）再编 Go；
没有 node 的环境用 `internal/panel/web` 里已提交的产物照常编译。
