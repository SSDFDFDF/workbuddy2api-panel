/* Dialog 弹窗：veil 遮罩 + 居中卡片，ESC 关闭、点遮罩关闭（可禁用）。
   宽度用 style 传（可选），内容自由。 */
import { useEffect } from 'react';
import type { ReactNode } from 'react';

export function Dialog({
  title,
  hint,
  onClose,
  width,
  footer,
  children,
}: {
  title: ReactNode;
  hint?: ReactNode;
  onClose: () => void;
  width?: number;
  footer?: ReactNode;
  children: ReactNode;
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    addEventListener('keydown', onKey);
    return () => removeEventListener('keydown', onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        className="flex max-h-[88vh] w-full max-w-[560px] flex-col overflow-hidden rounded-xl border border-[var(--line)] bg-[var(--surface)] shadow-2xl"
        style={width ? { maxWidth: width + 'px' } : undefined}
      >
        <header className="flex items-start gap-3 border-b border-[var(--line-soft)] px-5 py-3.5">
          <div className="min-w-0 flex-1">
            <h3 className="text-[14.5px] font-semibold">{title}</h3>
            {hint && <div className="mt-1 text-[12px] leading-relaxed text-[var(--ink-3)]">{hint}</div>}
          </div>
          <button className="shrink-0 rounded-md p-1 text-[var(--ink-3)] hover:bg-[var(--surface-2)] hover:text-[var(--ink)]" onClick={onClose} aria-label="关闭">
            <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.6">
              <path d="M3.5 3.5l9 9M12.5 3.5l-9 9" />
            </svg>
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
        {footer && <footer className="flex items-center gap-2 border-t border-[var(--line-soft)] px-5 py-3">{footer}</footer>}
      </div>
    </div>
  );
}
