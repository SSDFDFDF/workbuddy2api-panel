/* 按钮与表单控件的 Tailwind 组件类（tokens.css 中 @layer components 定义，
   这里只是类名常量 + 少量组件，保证各视图用法一致）。 */

export const btnBase =
  'inline-flex items-center justify-center gap-1.5 rounded-lg border text-[13px] font-medium cursor-pointer transition-colors ' +
  'disabled:opacity-50 disabled:cursor-not-allowed';

export const btnPrimary = `${btnBase} border-transparent bg-[var(--accent)] text-[var(--accent-ink)] hover:opacity-90 px-3.5 py-1.5`;
export const btnGhost = `${btnBase} border-[var(--line)] bg-[var(--surface-2)] text-[var(--ink-2)] hover:text-[var(--ink)] hover:border-[var(--ink-3)] px-3.5 py-1.5`;
export const btnDanger = `${btnBase} border-[var(--line)] bg-transparent text-[var(--bad)] hover:bg-[var(--bad-soft)] px-3.5 py-1.5`;
export const btnXs = `${btnBase} border-[var(--line)] bg-[var(--surface-2)] text-[var(--ink-2)] hover:text-[var(--ink)] px-2 py-0.5 text-[12px]`;
export const btnXsPrimary = `${btnBase} border-transparent bg-[var(--accent)] text-[var(--accent-ink)] hover:opacity-90 px-2 py-0.5 text-[12px]`;
export const btnXsGhost = btnXs;
