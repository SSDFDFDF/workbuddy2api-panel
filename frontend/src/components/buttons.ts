/* 按钮与表单控件样式类（统一样式、文字高对比度、现代化微阴影与按压动效）。 */

export const btnBase =
  'inline-flex items-center justify-center gap-1.5 rounded-lg border text-[13px] font-medium cursor-pointer transition-all duration-150 select-none active:scale-[0.98] ' +
  'disabled:opacity-45 disabled:cursor-not-allowed disabled:active:scale-100';

export const btnPrimary =
  `${btnBase} border-transparent bg-[var(--accent)] text-white font-semibold shadow-xs hover:brightness-110 active:brightness-95 px-3.5 py-1.5`;

export const btnGhost =
  `${btnBase} border-[var(--line)] bg-[var(--surface-2)] text-[var(--ink)] hover:bg-[var(--raise)] hover:border-[var(--accent)]/40 hover:text-[var(--accent)] px-3.5 py-1.5 shadow-2xs`;

export const btnDanger =
  `${btnBase} border-[var(--bad)]/30 bg-[var(--bad-soft)] text-[var(--bad)] font-medium hover:bg-[var(--bad)] hover:text-white px-3.5 py-1.5`;

export const btnXs =
  `${btnBase} border-[var(--line)] bg-[var(--surface-2)] text-[var(--ink)] hover:bg-[var(--raise)] hover:border-[var(--accent)]/40 hover:text-[var(--accent)] px-2.5 py-1 text-[12px] shadow-2xs`;

export const btnXsPrimary =
  `${btnBase} border-transparent bg-[var(--accent)] text-white font-medium hover:brightness-110 px-2.5 py-1 text-[12px] shadow-xs`;

export const btnXsGhost = btnXs;
