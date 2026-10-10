/* 任务中心视图：成长任务队列（扫描 / 排队执行 / 进度轮询）+ 券码查询弹窗。 */
import { useEffect, useRef, useState } from 'react';
import { api } from '../api';
import { toast, ask } from '../toast';
import type { QueueItem, QueueStatus, ScanAccount, VoucherAccount, Voucher } from '../types';
import { Dialog } from '../components/Dialog';
import { State, Dots, Empty, Tag } from '../components/ui';
import { btnPrimary, btnGhost, btnXs } from '../components/buttons';
import { qrSVG, qrMatrix } from '../qr';
import { copyText } from '../clipboard';

interface Group {
  uid: string;
  nick?: string;
  rows: { code: string; kind?: string; prog?: string; status?: string; message?: string }[];
}

const ST_WORDS: Record<string, string> = { done: '完成', running: '执行中', error: '失败', skipped: '跳过', pending: '排队', scan: '待执行' };

function groupsFromScan(d: { accounts?: ScanAccount[] }): Group[] {
  const groups: Group[] = [];
  for (const a of d.accounts || []) {
    const rows = (a.growth || []).map((t) => ({
      code: t.task_code,
      prog: t.target ? `${t.current ?? 0}/${t.target}` : '—',
      status: 'scan',
      title: t.title,
    }));
    if (rows.length) groups.push({ uid: a.uid, nick: a.nickname, rows });
  }
  return groups;
}

function groupsFromQueue(items: QueueItem[]): Group[] {
  const by = new Map<string, Group>();
  for (const it of items || []) {
    let g = by.get(it.uid);
    if (!g) {
      g = { uid: it.uid, nick: it.nickname, rows: [] };
      by.set(it.uid, g);
    }
    g.rows.push({ code: it.code || '', kind: it.kind, status: it.status, message: it.message });
  }
  return [...by.values()];
}

/* 券码卡。 */
function VoucherCard({ v }: { v: Voucher }) {
  const [showQr, setShowQr] = useState(false);
  const expired = v.valid_to && new Date(v.valid_to) < new Date();
  return (
    <div className="rounded-xl border border-[var(--line)] bg-[var(--surface-2)] p-3.5">
      <div className="flex items-center gap-2">
        <span className="font-medium">{v.prize_name || v.sku_code || '券'}</span>
        {expired ? <Tag tone="bad">已过期</Tag> : <Tag tone="ok">可使用</Tag>}
      </div>
      <div className="mt-1 text-[12px] text-[var(--ink-3)]">
        {v.valid_to ? '有效期至 ' + v.valid_to : '长期有效'}
        {v.granted_at ? ' · ' + v.granted_at.slice(0, 10) + ' 抽中' : ''}
      </div>
      <div className="mt-2.5 flex items-center gap-2">
        <span className="text-[12px] text-[var(--ink-3)]">券码</span>
        <code className="font-[family-name:var(--mono)] text-[13px] font-medium">{v.code || '-'}</code>
        <span className="flex flex-1 justify-end gap-1.5">
          {v.code && (
            <button className={btnXs} onClick={() => setShowQr(!showQr)}>
              二维码
            </button>
          )}
          <button
            className={btnXs}
            onClick={async () => {
              try {
                await copyText(v.code || '');
                toast('券码已复制', 'ok');
              } catch {
                toast('复制失败，请手动选择券码', 'err');
              }
            }}
          >
            复制
          </button>
        </span>
      </div>
      {showQr && v.code && (
        <div
          className="mt-2.5 flex justify-center rounded-lg bg-white p-2"
          dangerouslySetInnerHTML={{ __html: (() => { try { return qrSVG(qrMatrix(v.code!), 148); } catch { return '<span style="color:#999">生成失败</span>'; } })() }}
        />
      )}
    </div>
  );
}

function VouchersDialog({ onClose }: { onClose: () => void }) {
  const [data, setData] = useState<VoucherAccount[] | null>(null);
  const [error, setError] = useState('');

  const load = async () => {
    setError('');
    try {
      const d = await api<{ accounts: VoucherAccount[] }>('school/vouchers');
      setData(d.accounts || []);
    } catch (e) {
      setError((e as Error).message);
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const ok = (data || []).filter((a) => !a.error);
  const total = ok.reduce((n, a) => n + (a.vouchers || []).length, 0);
  const errs = (data || []).filter((a) => a.error);

  return (
    <Dialog
      title="开学季 · 我的券码"
      hint="抽奖抽中的第三方券（KFC / 瑞幸 / 酷狗等），券码到店到对应 app/小程序兑换。"
      onClose={onClose}
      footer={
        <>
          <span className="text-[12px] text-[var(--ink-3)]">{data ? (total ? `${total} 张券 · ` + ok.filter((a) => !(a.vouchers || []).length).length + ' 个账号未抽中' : '') : ''}</span>
          <span className="flex-1" />
          <button className={btnXs} onClick={() => void load()}>
            刷新
          </button>
          <button className={btnXs + ' !border-transparent !bg-[var(--accent)] !text-[var(--accent-ink)]'} onClick={onClose}>
            关闭
          </button>
        </>
      }
    >
      {error ? (
        <State kind="err">{error}</State>
      ) : data == null ? (
        <State>
          <Dots>查询中</Dots>
        </State>
      ) : (
        <div className="flex flex-col gap-4">
          {ok.filter((a) => (a.vouchers || []).length).map((a) => (
            <div key={a.uid}>
              <div className="mb-2 flex items-center justify-between text-[13px]">
                <span className="font-medium">{a.nickname || a.uid}</span>
                <span className="text-[var(--ink-3)]">{a.vouchers!.length} 张</span>
              </div>
              <div className="grid grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-2.5">
                {(a.vouchers || []).map((v, i) => (
                  <VoucherCard key={i} v={v} />
                ))}
              </div>
            </div>
          ))}
          {!total && (
            <Empty big="🎟️">还没有抽到券</Empty>
          )}
          {errs.length > 0 && (
            <div className="text-[12.5px] text-[var(--warn)]">
              查询失败：{errs.map((a) => `${a.nickname || a.uid.slice(0, 8)}（${a.error}）`).join('、')}
            </div>
          )}
        </div>
      )}
    </Dialog>
  );
}

export function TasksCenterView() {
  const [groups, setGroups] = useState<Group[] | null>(null);
  const [progress, setProgress] = useState<QueueStatus | null>(null);
  const [summary, setSummary] = useState('');
  const [scanning, setScanning] = useState(false);
  const [starting, setStarting] = useState(false);
  const [conc, setConc] = useState(1);
  const [vouchers, setVouchers] = useState(false);
  const [emptyText, setEmptyText] = useState<{ t: string; d: string }>({ t: '还没有扫描过', d: '扫描所有账号的成长任务与开学季待办，把没做的排成一列，一键执行。' });
  const timerRef = useRef<number | null>(null);
  const seqRef = useRef(0);

  const stopPoll = () => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
  };
  useEffect(() => stopPoll, []);

  const render = (gs: Group[], q: QueueStatus | null) => {
    setGroups(gs);
    setProgress(q);
    setSummary(gs.reduce((n, g) => n + g.rows.length, 0) + ' 项');
  };

  const startPolling = () => {
    stopPoll();
    timerRef.current = setInterval(async () => {
      try {
        const q = await api<QueueStatus>('tasks/queue');
        if (!q.started) return;
        if (seqRef.current && q.seq !== seqRef.current) return;
        render(groupsFromQueue(q.items || []), q);
        if (!q.running) {
          stopPoll();
          toast('任务队列执行结束', 'ok');
        }
      } catch {
        /* 轮询失败静默 */
      }
    }, 3000);
  };

  /* 切回视图时若本页启动的队列仍在跑则恢复轮询。 */
  useEffect(() => {
    (async () => {
      try {
        const q = await api<QueueStatus>('tasks/queue');
        if (timerRef.current) return;
        if (q.started && q.running && (!seqRef.current || q.seq === seqRef.current)) startPolling();
      } catch {
        /* 静默 */
      }
    })();
    return stopPoll;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const scanAll = async () => {
    stopPoll(); // 显式扫描切到待办视图，在途队列不再回写本视图
    setScanning(true);
    try {
      const d = await api<{ accounts: ScanAccount[] }>('tasks/scan_all', { method: 'POST' });
      const gs = groupsFromScan(d);
      render(gs, null);
      if (!gs.length) setEmptyText({ t: '没有待办任务 🎉', d: '全部账号的成长任务与开学季活动都已完成，明日再来。' });
    } catch (e) {
      toast((e as Error).message, 'err');
    } finally {
      setScanning(false);
    }
  };

  const runQueue = async () => {
    const c = conc || 1;
    if (!ask(`扫描全部账号待办并排队执行（账号并发 ${c}，账号内串行）。\n含真实对话的任务耗时较长，确认继续？`)) return;
    setStarting(true);
    try {
      const r = await api<{ started?: boolean; message?: string; seq?: number; total?: number }>('tasks/run_queue', {
        method: 'POST',
        body: JSON.stringify({ concurrency: c }),
      });
      if (!r.started) {
        toast(r.message || '没有待办任务', 'ok');
        return;
      }
      seqRef.current = r.seq || 0;
      toast(`队列已启动：${r.total} 项（并发 ${c}）`, 'ok');
      startPolling();
    } catch (e) {
      toast((e as Error).message, 'err');
    } finally {
      setStarting(false);
    }
  };

  const items = progress?.items || [];
  const done = items.filter((it) => it.status === 'done' || it.status === 'error' || it.status === 'skipped').length;

  return (
    <div className="grid grid-cols-[240px_1fr] gap-4">
      {/* 侧栏 */}
      <aside className="flex flex-col gap-3 self-start rounded-xl border border-[var(--line)] bg-[var(--surface)] p-4">
        <div className="flex items-center gap-1.5 text-[13px] font-medium">
          <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.6">
            <path d="M3 2.5h10v9H8l-3 3v-3H3zM6 6.5h4M6 9h2.5" />
          </svg>
          任务控制台
        </div>
        <button className={btnPrimary + ' w-full'} disabled={starting} onClick={() => void runQueue()}>
          {starting ? '启动中…' : '执行全部待办'}
        </button>
        <button className={btnGhost + ' w-full'} disabled={scanning} onClick={() => void scanAll()}>
          {scanning ? '扫描中…' : '扫描账号待办'}
        </button>
        <button className={btnGhost + ' w-full'} title="开学季抽奖抽中的第三方券码" onClick={() => setVouchers(true)}>
          查询抽奖券码
        </button>
        <div className="mt-1 flex flex-col gap-1">
          <span className="text-[12px] text-[var(--ink-3)]">执行并发限制</span>
          <select className="wb-select px-2 py-1.5 text-[13px]" value={conc} onChange={(e) => setConc(Number(e.target.value) || 1)}>
            <option value={1}>1 个账号并发</option>
            <option value={2}>2 个账号并发</option>
            <option value={3}>3 个账号并发</option>
          </select>
          <span className="text-[11.5px] text-[var(--ink-3)]">单次推进的并发账号数</span>
        </div>
        <div className="mt-2 border-t border-[var(--line-soft)] pt-3 text-[11.5px] leading-relaxed text-[var(--ink-3)]">
          全账号待办自动排队推进；签到、领 Buddy、对话活跃上报已完全免端自动完成。
        </div>
      </aside>

      {/* 队列 */}
      <div className="rounded-xl border border-[var(--line)] bg-[var(--surface)]">
        <header className="flex items-center gap-2.5 px-4 py-3">
          <h3 className="text-[14px] font-semibold">成长任务队列</h3>
          <span className="flex-1" />
          <span className="text-[12px] text-[var(--ink-3)]">{summary}</span>
        </header>

        {progress && items.length > 0 && (
          <div className="flex items-center gap-3 px-4 pt-3.5 pb-1">
            <div className="h-2 flex-1 overflow-hidden rounded-full bg-[var(--surface-2)] p-0.5">
              <i
                className="block h-full rounded-full bg-[var(--accent)] transition-[width] duration-500 shadow-xs"
                style={{ width: (items.length ? Math.round((done / items.length) * 100) : 0) + '%' }}
              />
            </div>
            <span className="tabular text-[12px] font-medium text-[var(--ink-2)]">
              {progress.running ? <span className="text-[var(--accent)]">执行中 </span> : '已结束 '}
              {done} / {items.length} ({items.length ? Math.round((done / items.length) * 100) : 0}%)
            </span>
          </div>
        )}

        {groups == null || !groups.length ? (
          <div className="flex flex-col items-center gap-1.5 px-4 py-10 text-center text-[var(--ink-3)]">
            <svg viewBox="0 0 48 48" width="42" height="42" fill="none" stroke="currentColor" strokeWidth="2" className="mb-1.5 opacity-55">
              <rect x="8" y="6" width="24" height="36" rx="3" />
              <path d="M14 16h12M14 24h12M14 32h7" opacity=".45" />
              <circle cx="35" cy="33" r="8" />
              <path d="m40 38 5 5" />
            </svg>
            <div className="font-medium text-[var(--ink-2)]">{emptyText.t}</div>
            <div className="max-w-[34em] text-[12.5px]">{emptyText.d}</div>
          </div>
        ) : (
          <div className="p-4 space-y-3">
            {groups.map((g) => (
              <div key={g.uid} className="rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)]/30 p-3.5 shadow-2xs">
                <header className="mb-2.5 flex items-center justify-between">
                  <div className="flex items-center gap-2">
                    <span className="font-semibold text-[13.5px] text-[var(--ink)]">{g.nick || g.uid.slice(0, 12)}</span>
                    <span className="font-[family-name:var(--mono)] text-[11px] text-[var(--ink-3)]">{g.uid.slice(0, 8)}</span>
                  </div>
                  <span className="rounded-full bg-[var(--surface)] px-2 py-0.5 text-[11px] tabular text-[var(--ink-3)] border border-[var(--line-soft)]">
                    {g.rows.length} 项待办
                  </span>
                </header>
                <div className="divide-y divide-[var(--line-soft)]/50 rounded-md border border-[var(--line-soft)]/60 bg-[var(--surface)] overflow-hidden">
                  {g.rows.map((it, i) => {
                    const dotCls =
                      it.status === 'running'
                        ? 'bg-[var(--accent)] wb-pulse-anim'
                        : it.status === 'error'
                          ? 'bg-[var(--bad)]'
                          : it.status === 'skipped'
                            ? 'bg-[var(--ink-3)]'
                            : it.status === 'done'
                              ? 'bg-[var(--ok)]'
                              : 'bg-[var(--line)]';
                    return (
                      <div
                        key={i}
                        className="flex items-center gap-3 px-3 py-2 text-[12.5px] transition-colors hover:bg-[var(--surface-2)]/50"
                        title={it.message || ''}
                      >
                        <span className="font-[family-name:var(--mono)] text-[11px] text-[var(--ink-3)] min-w-[70px] truncate">{it.code}</span>
                        <span className="flex-1 font-medium text-[var(--ink)] truncate">
                          {it.kind === 'school' ? <span>开学季闭环 <Tag tone="mute">开学季</Tag></span> : it.code}
                        </span>
                        {it.prog && <span className="tabular text-[11.5px] text-[var(--ink-3)]">{it.prog}</span>}
                        <span className="flex items-center gap-1.5 text-[11.5px] min-w-[55px]">
                          <span className={'h-2 w-2 rounded-full shrink-0 ' + dotCls} />
                          <span className="font-medium text-[var(--ink-2)]">{ST_WORDS[it.status || ''] || it.status}</span>
                        </span>
                        {it.message && <span className="max-w-[200px] truncate text-[11px] text-[var(--ink-3)]">{it.message}</span>}
                      </div>
                    );
                  })}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {vouchers && <VouchersDialog onClose={() => setVouchers(false)} />}
    </div>
  );
}
