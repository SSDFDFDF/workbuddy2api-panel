/* fetch 封装：Bearer 鉴权 + 401 时弹出密钥门。
   密钥存 localStorage；所有视图统一经 api() 走 /panel/api/*。 */

export const LS_KEY = 'wb2api.key';

let onUnauthorized: (() => void) | null = null;

/** 注册 401 回调（密钥门组件挂载时设置）。 */
export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn;
}

export function apiKey(): string {
  return localStorage.getItem(LS_KEY) || '';
}

export function setApiKey(k: string) {
  localStorage.setItem(LS_KEY, k);
}

export class ApiError extends Error {
  status: number;
  constructor(message: string, status: number) {
    super(message);
    this.status = status;
  }
}

export async function api<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const h: Record<string, string> = { ...(opts.headers as Record<string, string>) };
  const k = apiKey();
  if (k) h['Authorization'] = 'Bearer ' + k;
  if (opts.body && !(opts.body instanceof FormData)) h['Content-Type'] = 'application/json';
  const r = await fetch('/panel/api/' + path, { ...opts, headers: h });
  if (r.status === 401) {
    onUnauthorized?.();
    throw new ApiError('密钥无效或未填写', 401);
  }
  const d = await r.json().catch(() => ({}));
  if (!r.ok) throw new ApiError(d.error || 'HTTP ' + r.status, r.status);
  return d as T;
}

/** FormData 上传（cockpit JSON 导入），同样带鉴权头。 */
export async function apiUpload<T = any>(path: string, fd: FormData): Promise<T> {
  const h: Record<string, string> = {};
  const k = apiKey();
  if (k) h['Authorization'] = 'Bearer ' + k;
  const r = await fetch('/panel/api/' + path, { method: 'POST', body: fd, headers: h });
  if (r.status === 401) {
    onUnauthorized?.();
    throw new ApiError('密钥无效或未填写', 401);
  }
  const d = await r.json().catch(() => ({}));
  if (!r.ok) throw new ApiError(d.error || 'HTTP ' + r.status, r.status);
  return d as T;
}
