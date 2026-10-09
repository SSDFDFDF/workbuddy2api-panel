import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

/* stripCrossorigin：清掉 vite 注入的 crossorigin 属性。
   面板是同源资源（go:embed 下发，无 CDN/CORS），crossorigin 只会让浏览器
   多走一遍 CORS 检查；同一文件的 link 与 script 也各带一个，纯属冗余。
   Vite 6 硬编码 crossorigin: true（不接受配置），transformIndexHtml 钩子
   在 build 模式下作用于注入前的源 HTML，故在 closeBundle（写盘后）直接改。 */
function stripCrossorigin(): Plugin {
  return {
    name: 'strip-crossorigin',
    closeBundle() {
      const f = resolve(__dirname, '../internal/panel/web/index.html');
      try {
        const html = readFileSync(f, 'utf8');
        const out = html.split(' crossorigin').join('');
        if (out !== html) writeFileSync(f, out);
      } catch {
        /* 产物不存在时跳过（如纯 dev 模式） */
      }
    },
  };
}

// 面板前端构建：产物为「无哈希三件套」（index.html + app.js + app.css），
// 输出到 ../internal/panel/web，由 Go 的 go:embed 整目录嵌进二进制
//（go:embed 只能嵌包目录内的文件，因此产物目录必须在 panel 包下，源码在外）。
//
// - base: './' 让资源引用为相对路径（Go 端按 /panel/ 前缀分发）；
// - 固定文件名：嵌入清单稳定，CSP script-src 'self' 直接命中；
// - emptyOutDir：构建即唯一真相。
export default defineConfig({
  plugins: [react(), tailwindcss(), stripCrossorigin()],
  base: './',
  build: {
    outDir: '../internal/panel/web',
    emptyOutDir: true,
    assetsInlineLimit: 0,
    cssCodeSplit: false,
    rollupOptions: {
      output: {
        entryFileNames: 'app.js',
        chunkFileNames: 'app.js',
        assetFileNames: 'app.css',
        // 单入口单 chunk：面板逻辑不大，拆 chunk 只会让 CSP 名单与 embed 清单复杂化。
        inlineDynamicImports: true,
      },
    },
  },
  server: {
    // 本地开发代理：前端跑在 5173，API 转发到本机 Go 网关。
    proxy: {
      '/panel/api': 'http://127.0.0.1:7863',
    },
  },
});
