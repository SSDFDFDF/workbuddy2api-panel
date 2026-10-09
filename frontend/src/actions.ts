/* 账号动作（签到/余额/解冻/禁用/暂停/恢复/代理/移除）：
   全视图共用的一组 API 调用与确认文案。 */
import { api } from './api';
import { ask, toast } from './toast';
import type { OverviewAccount } from './types';

export async function accountAction(a: string, uid: string, s: OverviewAccount | undefined, after: () => void) {
  const base = 'accounts/' + encodeURIComponent(uid) + '/';
  try {
    if (a === 'checkin') {
      const r = await api<{ credits?: number; credits_total?: number; checkin_message?: string }>(base + 'checkin', { method: 'POST' });
      toast(
        '签到完成' +
          (r.credits != null ? '，积分 ' + r.credits + ((r.credits_total || 0) > 0 ? '/' + r.credits_total : '') : '') +
          (r.checkin_message ? '（' + r.checkin_message + '）' : ''),
        'ok',
      );
    } else if (a === 'balance') {
      const r = await api<{ credits: number; credits_total: number }>(base + 'balance', { method: 'POST' });
      toast('余额已更新：' + r.credits + (r.credits_total > 0 ? ' / ' + r.credits_total : ''), 'ok');
    } else if (a === 'revive') {
      await api(base + 'revive', { method: 'POST' });
      toast('已解冻', 'ok');
    } else if (a === 'disable') {
      if (!ask('禁用后该账号不再参与选号（保号任务默认也跳过），需手动解冻才能恢复。若只是想临时让位、仍要保号，请改用「暂停选号」。确认禁用？')) return;
      await api(base + 'disable', { method: 'POST' });
      toast('已禁用', 'ok');
    } else if (a === 'pause') {
      await api(base + 'pause', { method: 'POST' });
      toast('已暂停选号（签到 / 保活照常）', 'ok');
    } else if (a === 'resume') {
      await api(base + 'resume', { method: 'POST' });
      toast('已恢复选号', 'ok');
    } else if (a === 'proxy') {
      const enabled = !(s && s.proxy_enabled);
      const r = await api<{ proxy_enabled: boolean }>(base + 'proxy', { method: 'POST', body: JSON.stringify({ enabled }) });
      toast('该账号代理已' + (r.proxy_enabled ? '开启' : '关闭（直连）'), 'ok');
    } else if (a === 'remove') {
      if (!ask('移除账号将删除池状态与 auths/ 下的凭证文件，且不可恢复。确认移除？')) return;
      const r = await api<{ file_error?: string }>(base + 'remove', { method: 'POST' });
      toast(r.file_error ? '已移除（凭证文件删除失败：' + r.file_error + '）' : '已移除', 'ok');
    }
  } catch (e) {
    toast((e as Error).message, 'err');
  }
  after();
}
