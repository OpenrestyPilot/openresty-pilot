/** 控制面大盘（运维统计报表）API */
import type {
  DashboardMetrics,
  DashboardRange,
  DashboardTopRankings,
  DashboardTrends,
} from './types';

import { redirectToLoginOnUnauthorized, requestClient } from '#/api/request';
import { useAccessStore } from '@vben/stores';

export interface DashboardEventSnapshot {
  metrics: DashboardMetrics;
  rankings: DashboardTopRankings;
  trends: DashboardTrends;
  updatedAt: string;
}

/** Consume authenticated server-sent dashboard snapshots until aborted or disconnected. */
export async function consumeDashboardEvents(
  params: { centerId?: number; range?: DashboardRange },
  signal: AbortSignal,
  onSnapshot: (snapshot: DashboardEventSnapshot) => void,
  onError: (message: string) => void,
): Promise<void> {
  const accessToken = useAccessStore().accessToken;
  const query = new URLSearchParams();
  if (params.centerId) query.set('centerId', String(params.centerId));
  if (params.range) query.set('range', params.range);
  const response = await fetch(`/api/orp/dashboard/events?${query}`, {
    headers: {
      Accept: 'text/event-stream',
      ...(accessToken ? { Authorization: `Bearer ${accessToken}` } : {}),
    },
    signal,
  });
  if (!response.ok || !response.body) {
    if (response.status === 401) {
      await redirectToLoginOnUnauthorized();
      throw new Error('登录状态已失效，正在跳转登录页');
    }
    if (response.status === 403) {
      throw new Error('当前账号没有查看大盘的权限');
    }
    throw new Error(`大盘实时连接失败（HTTP ${response.status}）`);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  while (!signal.aborted) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true }).replaceAll('\r\n', '\n');
    let boundary = buffer.indexOf('\n\n');
    while (boundary >= 0) {
      const frame = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      let event = 'message';
      const data: string[] = [];
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim();
        else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
      }
      if (data.length > 0) {
        const payload = data.join('\n');
        if (event === 'dashboard') onSnapshot(JSON.parse(payload) as DashboardEventSnapshot);
        else if (event === 'dashboard-auth-error') {
          await redirectToLoginOnUnauthorized();
          throw new Error('登录状态已失效，正在跳转登录页');
        }
        else if (event === 'dashboard-error') {
          const parsed = JSON.parse(payload) as { message?: string };
          onError(parsed.message || '读取大盘数据失败');
        }
      }
      boundary = buffer.indexOf('\n\n');
    }
  }
  await reader.cancel().catch(() => undefined);
  if (!signal.aborted) throw new Error('大盘实时连接已断开');
}

export function getDashboardMetricsApi(params: {
  centerId?: number;
  range?: DashboardRange;
}): Promise<DashboardMetrics> {
  return requestClient.get('/orp/dashboard/metrics', { params });
}

export function getDashboardTrendsApi(params: {
  centerId?: number;
  range?: DashboardRange;
}): Promise<DashboardTrends> {
  return requestClient.get('/orp/dashboard/trends', { params });
}

export function getDashboardTopRankingsApi(params: {
  centerId?: number;
  range?: DashboardRange;
}): Promise<DashboardTopRankings> {
  return requestClient.get('/orp/dashboard/top-rankings', { params });
}
