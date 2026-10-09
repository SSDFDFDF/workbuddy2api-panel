/* 精简 QR 编码器（券码二维码用）：byte 模式、ECC L、版本 1-5、固定掩码 0。
   自包含零依赖；面板 CSP 只允许 self，外链 QR 服务不可用。
   已用 python qrcode 库逐像素交叉验证（5/5 diff=0）。 */

// GF(256) 对数/指数表（本原多项式 0x11d）
const QR_EXP = new Array<number>(512);
const QR_LOG = new Array<number>(256);
(() => {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    QR_EXP[i] = x;
    QR_LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d;
  }
  for (let i = 255; i < 512; i++) QR_EXP[i] = QR_EXP[i - 255];
})();

const gmul = (a: number, b: number) => (a && b ? QR_EXP[QR_LOG[a] + QR_LOG[b]] : 0);

// 各版本参数（下标 = 版本-1）：[数据码字数, 纠错码字数]，ECC L 单块
const QR_V: [number, number][] = [
  [19, 7],
  [34, 10],
  [55, 15],
  [80, 20],
  [108, 26],
];
// 对齐图案中心坐标（v2+）
const QR_ALIGN: number[][] = [[], [6, 18], [6, 22], [6, 26], [6, 30]];
const QR_MASK = (r: number, c: number) => (r + c) % 2 === 0; // 掩码模式 0

function qrGenPoly(deg: number): number[] {
  let g = [1];
  for (let i = 0; i < deg; i++) {
    const a = QR_EXP[i];
    const ng = new Array<number>(g.length + 1).fill(0);
    ng[0] = g[0];
    for (let j = 1; j < g.length; j++) ng[j] = g[j] ^ gmul(a, g[j - 1]);
    ng[g.length] = gmul(a, g[g.length - 1]);
    g = ng;
  }
  return g;
}

function rsRem(data: number[], deg: number): number[] {
  const g = qrGenPoly(deg);
  const res = data.concat(new Array<number>(deg).fill(0));
  for (let i = 0; i < data.length; i++) {
    const f = res[i];
    if (f) for (let j = 0; j < g.length; j++) res[i + j] ^= gmul(g[j], f);
  }
  return res.slice(data.length);
}

function qrDataCodewords(text: string, dataCap: number): number[] {
  const bytes = Array.from(new TextEncoder().encode(text));
  const bits: number[] = [];
  const push = (val: number, n: number) => {
    for (let i = n - 1; i >= 0; i--) bits.push((val >> i) & 1);
  };
  push(4, 4); // byte 模式
  push(bytes.length, 8);
  for (const b of bytes) push(b, 8);
  const cap = dataCap * 8;
  push(0, Math.min(4, cap - bits.length)); // 终止符
  while (bits.length % 8) bits.push(0);
  const out: number[] = [];
  for (let i = 0; i < bits.length; i += 8) {
    let v = 0;
    for (const b of bits.slice(i, i + 8)) v = (v << 1) | b;
    out.push(v);
  }
  for (let p = 0; out.length < dataCap; p ^= 1) out.push(p ? 0x11 : 0xec);
  return out;
}

/** text → 布尔矩阵（true = 深色模块）。 */
export function qrMatrix(text: string): boolean[][] {
  const bytes = Array.from(new TextEncoder().encode(text));
  let ver = 0;
  for (let v = 0; v < QR_V.length; v++) {
    if (bytes.length + 2 <= QR_V[v][0]) {
      ver = v + 1;
      break;
    }
  }
  if (!ver) throw new Error('QR: text too long');
  const [dataCap, ecCap] = QR_V[ver - 1];
  const n = 17 + 4 * ver;

  const M = Array.from({ length: n }, () => new Array<boolean>(n).fill(false));
  const F = Array.from({ length: n }, () => new Array<boolean>(n).fill(false));

  const setF = (r: number, c: number, v: boolean) => {
    M[r][c] = v;
    F[r][c] = true;
  };
  const finder = (r0: number, c0: number) => {
    for (let r = -1; r <= 7; r++)
      for (let c = -1; c <= 7; c++) {
        const rr = r0 + r;
        const cc = c0 + c;
        if (rr < 0 || cc < 0 || rr >= n || cc >= n) continue;
        const dark =
          r >= 0 && r <= 6 && c >= 0 && c <= 6 && (r === 0 || r === 6 || c === 0 || c === 6 || (r >= 2 && r <= 4 && c >= 2 && c <= 4));
        setF(rr, cc, dark);
      }
  };
  finder(0, 0);
  finder(0, n - 7);
  finder(n - 7, 0);
  for (let r = 8; r <= n - 9; r++) setF(r, 6, r % 2 === 0);
  for (let c = 8; c <= n - 9; c++) setF(6, c, c % 2 === 0);
  const align = QR_ALIGN[ver - 1] || [];
  for (const ar of align)
    for (const ac of align) {
      if (F[ar][ac]) continue;
      for (let r = -2; r <= 2; r++)
        for (let c = -2; c <= 2; c++) setF(ar + r, ac + c, Math.max(Math.abs(r), Math.abs(c)) !== 1);
    }

  // 格式信息（ECC L=01，掩码 0）：BCH(15,5) + 0x5412 异或
  let fmt = (1 << 3) | 0;
  let rem = fmt << 10;
  for (let i = 14; i >= 10; i--) if ((rem >> i) & 1) rem ^= 0x537 << (i - 10);
  fmt = ((fmt << 10) | rem) ^ 0x5412;
  const fb = (i: number) => (fmt >> i) & 1;
  for (let i = 0; i <= 5; i++) setF(i, 8, !!fb(i));
  setF(7, 8, !!fb(6));
  setF(8, 8, !!fb(7));
  for (let i = 8; i <= 14; i++) setF(n - 15 + i, 8, !!fb(i));
  for (let i = 0; i <= 7; i++) setF(8, n - 1 - i, !!fb(i));
  setF(8, 7, !!fb(8));
  for (let i = 9; i <= 14; i++) setF(8, 14 - i, !!fb(i));
  setF(n - 8, 8, true); // 暗模块

  const dcw = qrDataCodewords(text, dataCap);
  const cw = dcw.concat(rsRem(dcw, ecCap));
  const bits: number[] = [];
  for (const b of cw) for (let i = 7; i >= 0; i--) bits.push((b >> i) & 1);

  // 蛇形放置（成对列，从右向左，跳过第 6 列），写数据时直接异或掩码
  let bi = 0;
  let up = true;
  for (let x = n - 1; x > 0; x -= 2) {
    if (x === 6) x--;
    for (let i = 0; i < n; i++) {
      const r = up ? n - 1 - i : i;
      for (const c of [x, x - 1]) {
        if (F[r][c]) continue;
        const bit = bi < bits.length ? bits[bi++] : 0;
        M[r][c] = bit ? !QR_MASK(r, c) : QR_MASK(r, c);
      }
    }
    up = !up;
  }
  return M;
}

/** 矩阵 → SVG 字符串（quiet zone 4 模块）。 */
export function qrSVG(M: boolean[][], px: number): string {
  const n = M.length;
  const q = 4;
  const total = n + q * 2;
  let s =
    '<svg viewBox="0 0 ' + total + ' ' + total + '" width="' + px + '" height="' + px +
    '" shape-rendering="crispEdges" role="img" style="background:#fff">';
  for (let r = 0; r < n; r++)
    for (let c = 0; c < n; c++) if (M[r][c]) s += '<rect x="' + (c + q) + '" y="' + (r + q) + '" width="1" height="1" fill="#000"/>';
  return s + '</svg>';
}
