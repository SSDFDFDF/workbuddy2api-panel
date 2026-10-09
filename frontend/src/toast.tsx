/* 全局 toast：右下角浮层，ok/err 两种语义色。
   用法：toast('已保存', 'ok')。 */
import { useState, useEffect } from 'react';

export type ToastKind = '' | 'ok' | 'err';

interface ToastItem {
  id: number;
  msg: string;
  kind: ToastKind;
}

let items: ToastItem[] = [];
let seq = 0;
const listeners = new Set<(v: ToastItem[]) => void>();

function emit() {
  for (const fn of listeners) fn(items);
}

export function toast(msg: string, kind: ToastKind = '') {
  const it = { id: ++seq, msg, kind };
  items = [...items, it];
  emit();
  setTimeout(() => {
    items = items.filter((x) => x.id !== it.id);
    emit();
  }, 3600);
}

function Toasts() {
  const [list, setList] = useState<ToastItem[]>(items);
  useEffect(() => {
    listeners.add(setList);
    return () => {
      listeners.delete(setList);
    };
  }, []);
  return (
    <div className="fixed right-5 bottom-5 z-[90] flex flex-col gap-2 items-end">
      {list.map((t) => (
        <div
          key={t.id}
          className={
            'max-w-[380px] rounded-lg border border-[var(--line)] bg-[var(--raise)] px-3.5 py-2.5 text-[13px] shadow-lg ' +
            (t.kind === 'ok'
              ? 'border-l-[3px] border-l-[var(--ok)]'
              : t.kind === 'err'
                ? 'border-l-[3px] border-l-[var(--bad)]'
                : 'border-l-[3px] border-l-[var(--ink-3)]')
          }
          style={{ animation: 'wb-toast-in .18s ease' }}
        >
          {t.msg}
        </div>
      ))}
    </div>
  );
}

export function ToastHost() {
  return <Toasts />;
}

/** confirm 包装（保留原生实现，调用点更简短）。 */
export function ask(msg: string): boolean {
  return window.confirm(msg);
}
