/* 配置表单控件：受控 Field / Switch / TaskCard（表单值由父级统一持有）。 */

export interface FieldSpec {
  name: string;
  label: string;
  hint?: string;
  type?: 'text' | 'password' | 'number' | 'textarea';
  placeholder?: string;
  options?: [string, string][]; // 下拉
  min?: number;
  step?: string;
  rows?: number;
  eye?: boolean; // api_key 显示/隐藏
  fillBtn?: string; // 一键填入按钮文案
  onFill?: () => void;
  badge?: string; // 「即时生效」等徽标
}

const inputCls =
  'w-full rounded-lg border border-[var(--line)] bg-[var(--surface-2)] px-2.5 py-1.5 text-[13px] outline-none focus:border-[var(--accent)]';
const inputBad = ' !border-[var(--bad)]';

export function Field({
  spec,
  value,
  onChange,
  bad,
}: {
  spec: FieldSpec;
  value: string | boolean | number;
  onChange: (v: string) => void;
  bad?: boolean;
}) {
  return (
    <label className="flex flex-col gap-1 text-[13px]">
      <span className="flex items-center gap-1.5 text-[12.5px] font-medium text-[var(--ink-2)]">
        {spec.label}
        {spec.badge && <span className="rounded bg-[var(--ok-soft)] px-1 py-px text-[10.5px] font-medium text-[var(--ok)]">{spec.badge}</span>}
        {spec.name === '__restart__' && <span className="rounded bg-[var(--warn-soft)] px-1 py-px text-[10.5px] text-[var(--warn)]">需重启</span>}
      </span>
      {spec.type === 'textarea' ? (
        <textarea
          name={spec.name}
          className={inputCls + (bad ? inputBad : '')}
          rows={spec.rows || 5}
          placeholder={spec.placeholder}
          spellCheck={false}
          value={String(value ?? '')}
          onChange={(e) => onChange(e.target.value)}
        />
      ) : spec.options ? (
        <select name={spec.name} className={'wb-select px-2.5 py-1.5 text-[13px]'} value={String(value ?? '')} onChange={(e) => onChange(e.target.value)}>
          {spec.options.map(([v, l]) => (
            <option key={v} value={v}>
              {l}
            </option>
          ))}
        </select>
      ) : (
        <span className="flex gap-1.5">
          <input
            name={spec.name}
            type={spec.type || 'text'}
            className={inputCls + (bad ? inputBad : '')}
            placeholder={spec.placeholder}
            min={spec.min}
            step={spec.step}
            value={String(value ?? '')}
            onChange={(e) => onChange(e.target.value)}
          />
          {spec.eye && (
            <button
              type="button"
              className="wb-select shrink-0 px-2.5 py-1.5 text-[12px]"
              onClick={(e) => {
                const btn = e.currentTarget;
                const input = btn.previousElementSibling as HTMLInputElement;
                const show = input.type === 'password';
                input.type = show ? 'text' : 'password';
                btn.textContent = show ? '隐藏' : '显示';
              }}
            >
              显示
            </button>
          )}
          {spec.fillBtn && spec.onFill && (
            <button type="button" className="wb-select shrink-0 px-2.5 py-1.5 text-[12px]" onClick={spec.onFill}>
              {spec.fillBtn}
            </button>
          )}
        </span>
      )}
      {spec.hint && <span className="text-[11.5px] leading-relaxed text-[var(--ink-3)]">{spec.hint}</span>}
    </label>
  );
}

export function Switch({ label, name, checked, onChange, hint }: { label: React.ReactNode; name: string; checked: boolean; onChange: (v: boolean) => void; hint?: string }) {
  return (
    <label className="flex cursor-pointer items-center gap-2.5 text-[13px]">
      <span
        className={'relative h-[18px] w-[32px] shrink-0 rounded-full transition-colors ' + (checked ? 'bg-[var(--accent)]' : 'bg-[var(--line)]')}
      >
        <input type="checkbox" name={name} checked={checked} onChange={(e) => onChange(e.target.checked)} className="peer sr-only" />
        <i className={'absolute top-[2px] h-[14px] w-[14px] rounded-full bg-white transition-all ' + (checked ? 'left-[16px]' : 'left-[2px]')} />
      </span>
      <span className="text-[12.5px] font-medium text-[var(--ink-2)]">{label}</span>
      {hint && <span className="text-[11.5px] text-[var(--ink-3)]">{hint}</span>}
    </label>
  );
}
