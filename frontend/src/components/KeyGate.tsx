/* KeyGate 密钥门：api_key 鉴权开启时（首次请求 401）要求输入密钥。
   验证通过后进入面板。 */
import { useState } from 'react';
import { api, setApiKey } from '../api';
import { btnPrimary } from '../components/buttons';

export function KeyGate({ onReady }: { onReady: () => void }) {
  const [val, setVal] = useState('');
  const [err, setErr] = useState(false);
  const [busy, setBusy] = useState(false);

  const submit = async () => {
    const v = val.trim();
    if (!v || busy) return;
    setBusy(true);
    try {
      setApiKey(v);
      await api('overview');
      setErr(false);
      onReady();
    } catch {
      setErr(true);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-[var(--bg)] p-4">
      <div className="w-full max-w-[420px] rounded-xl border border-[var(--line)] bg-[var(--surface)] p-6 shadow-2xl">
        <h3 className="text-[15px] font-semibold">需要访问密钥</h3>
        <p className="mt-1.5 text-[12.5px] leading-relaxed text-[var(--ink-3)]">
          该网关已启用 api_key 鉴权，请输入 config.json 中的密钥。
        </p>
        <input
          type="password"
          autoFocus
          autoComplete="off"
          placeholder="api_key"
          value={val}
          onChange={(e) => setVal(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && submit()}
          className="mt-4 w-full rounded-lg border border-[var(--line)] bg-[var(--surface-2)] px-3 py-2 text-[13.5px] outline-none focus:border-[var(--accent)]"
        />
        {err && <div className="mt-2.5 text-[12.5px] text-[var(--bad)]">密钥不正确，请重试。</div>}
        <div className="mt-4 flex justify-end">
          <button className={btnPrimary} disabled={busy} onClick={submit}>
            {busy ? '验证中…' : '进入'}
          </button>
        </div>
      </div>
    </div>
  );
}
