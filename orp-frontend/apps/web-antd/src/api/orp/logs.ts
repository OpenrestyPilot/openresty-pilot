/** 实时日志 API */
import type { LogLine } from './types';

import { redirectToLoginOnUnauthorized, requestClient } from '#/api/request';
import { useAccessStore } from '@vben/stores';

/**
 * 获取指定本地节点或服务的最新真实日志；页面按行 ID 去重轮询。
 * @param target 日志目标，如 node:1 / http:2 / stream:3
 */
export function getLogsApi(params: {
  target: string;
}): Promise<{ logs: LogLine[] }> {
  return requestClient.get('/orp/logs', { params });
}

/** Stream persisted Filebeat/Kafka log lines for the selected target. */
export async function consumeLogEvents(
  target: string,
  signal: AbortSignal,
  onLines: (lines: LogLine[]) => void,
  onError: (message: string) => void,
): Promise<void> {
  const token = useAccessStore().accessToken;
  const query = new URLSearchParams({ target });
  const response = await fetch(`/api/orp/logs/events?${query}`, {
    headers: {
      Accept: 'text/event-stream',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    signal,
  });
  if (!response.ok || !response.body) {
    if (response.status === 401) {
      await redirectToLoginOnUnauthorized();
      throw new Error('登录状态已失效，正在跳转登录页');
    }
    if (response.status === 403) {
      throw new Error('当前账号没有查看实时日志的权限');
    }
    throw new Error(`日志实时连接失败（HTTP ${response.status}）`);
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
        if (event === 'logs') onLines(JSON.parse(payload) as LogLine[]);
        else if (event === 'log-auth-error') {
          await redirectToLoginOnUnauthorized();
          throw new Error('登录状态已失效，正在跳转登录页');
        }
        else if (event === 'log-error') {
          const parsed = JSON.parse(payload) as { message?: string };
          onError(parsed.message || '读取 Kafka 日志失败');
        }
      }
      boundary = buffer.indexOf('\n\n');
    }
  }
  await reader.cancel().catch(() => undefined);
  if (!signal.aborted) throw new Error('日志实时连接已断开');
}
