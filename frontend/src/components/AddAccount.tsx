/* AddAccount 添加账号弹窗：浏览器 OAuth 授权链接（3s 轮询）+ cockpit JSON 导入。 */
import { useEffect, useRef, useState } from 'react';
import { api, apiUpload } from '../api';
import { Dialog } from './Dialog';
import { State, Dots } from './ui';
import { btnPrimary, btnGhost } from './buttons';
import { copyText } from '../clipboard';
import { toast } from '../toast';

export function AddAccountDialog({ onClose, onAdded }: { onClose: () => void; onAdded: () => void }) {
  const [tab, setTab] = useState<'login' | 'import'>('login');
  const [realm, setRealm] = useState<'cn' | 'global'>('cn');
  const [phase, setPhase] = useState<'pick' | 'loading' | 'ready' | 'done' | 'error'>('pick');
  const [url, setUrl] = useState('');
  const [msg, setMsg] = useState('');
  const [importMsg, setImportMsg] = useState<{ ok: boolean; text: string } | null>(null);
  const stateRef = useRef<string>('');
  const timerRef = useRef<number | null>(null);

  const stopPoll = () => {
    if (timerRef.current) {
      clearInterval(timerRef.current);
      timerRef.current = null;
    }
  };

  useEffect(() => stopPoll, []);

  const start = async () => {
    setPhase('loading');
    setMsg('');
    try {
      const r = await api<{ state: string; url: string }>('login/start', {
        method: 'POST',
        body: JSON.stringify({ realm }),
      });
      stateRef.current = r.state;
      setUrl(r.url);
      setPhase('ready');
      stopPoll();
      timerRef.current = setInterval(poll, 3000);
    } catch (e) {
      setMsg((e as Error).message);
      setPhase('error');
    }
  };

  const poll = async () => {
    const st = stateRef.current;
    if (!st) return;
    try {
      const r = await api<{ done?: boolean; nickname?: string; uid?: string; realm?: string; credits?: number; credits_total?: number }>(
        'login/poll?state=' + encodeURIComponent(st),
      );
      if (r.done) {
        stopPoll();
        const who = r.nickname || r.uid || '';
        const credit =
          r.credits != null && r.credits >= 0 ? ' · 积分 ' + r.credits + ((r.credits_total || 0) > 0 ? '/' + r.credits_total : '') : '';
        setMsg(`已添加 ${who}${r.realm === 'global' ? '（国际版）' : ''}${credit}，账号已载入池中`);
        setPhase('done');
        setTimeout(() => {
          onClose();
          onAdded();
        }, 1600);
      }
    } catch (e) {
      stopPoll();
      setMsg((e as Error).message + '（关闭后重新添加）');
      setPhase('error');
    }
  };

  const onFile = async (file: File) => {
    setImportMsg(null);
    const fd = new FormData();
    fd.append('file', file);
    try {
      const r = await apiUpload<{ imported: number; skipped?: number; errors?: string[] }>('import/cockpit', fd);
      setImportMsg({
        ok: true,
        text: '导入完成：成功 ' + r.imported + ' 个' + (r.skipped ? '，跳过 ' + r.skipped + ' 个' : ''),
      });
      onAdded();
    } catch (e) {
      setImportMsg({ ok: false, text: '导入失败：' + (e as Error).message });
    }
  };

  return (
    <Dialog
      title="添加账号"
      onClose={onClose}
      footer={
        <>
          <span className="flex-1" />
          {phase === 'pick' && (
            <button className={btnPrimary} onClick={start}>
              获取授权链接
            </button>
          )}
          {phase === 'ready' && (
            <>
              <button
                className={btnGhost}
                onClick={() => {
                  copyText(url)
                    .then(() => toast('链接已复制', 'ok'))
                    .catch(() => toast('复制失败，请手动选择复制', 'err'));
                }}
              >
                复制链接
              </button>
              <button className={btnPrimary} onClick={() => window.open(url, '_blank')}>
                在浏览器打开
              </button>
            </>
          )}
          <button className={btnGhost} onClick={onClose}>
            关闭
          </button>
        </>
      }
    >
      <div className="mb-4 flex gap-1.5">
        <button className={(tab === 'login' ? 'text-[var(--accent)] ' : 'text-[var(--ink-3)] hover:text-[var(--ink)] ') + 'border-b-2 px-3 pb-1.5 ' + (tab === 'login' ? 'border-[var(--accent)]' : 'border-transparent')} onClick={() => setTab('login')}>
          浏览器登录
        </button>
        <button className={(tab === 'import' ? 'text-[var(--accent)] ' : 'text-[var(--ink-3)] hover:text-[var(--ink)] ') + 'border-b-2 px-3 pb-1.5 ' + (tab === 'import' ? 'border-[var(--accent)]' : 'border-transparent')} onClick={() => setTab('import')}>
          导入 JSON
        </button>
      </div>

      {tab === 'login' ? (
        <div className="flex flex-col gap-3">
          {phase === 'pick' && (
            <>
              <div className="flex items-center gap-4 text-[13px]">
                <span className="text-[12px] text-[var(--ink-3)]">版本：</span>
                <label className="flex cursor-pointer items-center gap-1.5">
                  <input type="radio" checked={realm === 'cn'} onChange={() => setRealm('cn')} /> 国内版（CN）
                </label>
                <label className="flex cursor-pointer items-center gap-1.5">
                  <input type="radio" checked={realm === 'global'} onChange={() => setRealm('global')} /> 国际版（Global）
                </label>
              </div>
              <div className="text-[12px] text-[var(--ink-3)]">
                国际版登录后，网关自动完成注册地区、激活与试用额度领取，全程无需手动操作。
              </div>
            </>
          )}
          {phase === 'loading' && (
            <State>
              <Dots>正在获取授权链接</Dots>
            </State>
          )}
          {phase === 'ready' && (
            <>
              <State>在浏览器打开以下链接并登录：</State>
              <div className="break-all rounded-lg border border-[var(--line-soft)] bg-[var(--surface-2)] px-3 py-2 font-[family-name:var(--mono)] text-[12.5px] text-[var(--accent)]">
                {url}
              </div>
              <State>
                <Dots>等待授权完成，自动检测中</Dots>
              </State>
            </>
          )}
          {phase === 'done' && <State kind="ok">{msg}</State>}
          {phase === 'error' && <State kind="err">{msg}</State>}
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="text-[12.5px] text-[var(--ink-3)]">
            选择 cockpit tools 导出的 JSON 文件，批量导入账号。文件需为账号数组格式。
          </div>
          <input
            type="file"
            accept=".json"
            className="text-[13px]"
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void onFile(f);
              e.target.value = '';
            }}
          />
          {importMsg && <State kind={importMsg.ok ? 'ok' : 'err'}>{importMsg.text}</State>}
        </div>
      )}
    </Dialog>
  );
}
