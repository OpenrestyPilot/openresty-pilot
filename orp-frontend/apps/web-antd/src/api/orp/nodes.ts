/** 节点实例管理 API */
import type {
  LatencyHistory,
  NodeItem,
  NodeMetrics,
  NodePayload,
  NodeStatus,
  PageParams,
  PageResult,
} from './types';

import { redirectToLoginOnUnauthorized, requestClient } from '#/api/request';
import { useAccessStore } from '@vben/stores';

export function listNodesApi(
  params: PageParams & {
    centerId?: number;
    keyword?: string;
    status?: NodeStatus | '';
  },
): Promise<PageResult<NodeItem>> {
  return requestClient.get('/orp/nodes', { params });
}

/** 查询单节点近 N 次探活采样的延迟响应趋势与统计摘要 */
export function getNodeLatencyHistoryApi(
  id: number,
  limit = 50,
): Promise<LatencyHistory> {
  return requestClient.get(`/orp/nodes/${id}/latency-history`, {
    params: { limit },
  });
}

export function createNodeApi(data: NodePayload): Promise<NodeItem> {
  return requestClient.post('/orp/nodes', data);
}

export interface AgentBootstrapResponse {
  agentId: string;
  commands: { curl: string; docker: string; wget: string };
  controlPlaneUrl: string;
  nodeId: number;
  token: string;
}

/** Rotate a node Agent token and return one-time copyable install commands. */
export function bootstrapNodeAgentApi(id: number): Promise<AgentBootstrapResponse> {
  return requestClient.post(`/orp/nodes/${id}/agent/bootstrap`, {});
}

export function updateNodeApi(
  id: number,
  data: NodePayload,
): Promise<NodeItem> {
  return requestClient.put(`/orp/nodes/${id}`, data);
}

export function deleteNodeApi(id: number): Promise<null> {
  return requestClient.delete(`/orp/nodes/${id}`);
}

/** 修改节点运行状态（状态不变时静默返回，不产生变更与审计） */
export function updateNodeStatusApi(
  id: number,
  status: NodeStatus,
): Promise<NodeItem> {
  return requestClient.put(`/orp/nodes/${id}/status`, { status });
}

/** 节点基础运行指标 */
export function getNodeMetricsApi(
  id: number,
): Promise<{ metrics: NodeMetrics; node: NodeItem }> {
  return requestClient.get(`/orp/nodes/${id}/metrics`);
}

/** Stream one-second node metric snapshots over the authenticated SSE endpoint. */
export async function consumeNodeMetricEvents(
  id: number,
  signal: AbortSignal,
  onMetrics: (value: NodeMetrics) => void,
  onConnection: (connected: boolean) => void,
): Promise<void> {
  const token = useAccessStore().accessToken;
  let retryDelay = 1000;
  while (!signal.aborted) {
    try {
      const response = await fetch(`/api/orp/nodes/${id}/metrics/events`, {
        headers: {
          Accept: 'text/event-stream',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        signal,
      });
      if (!response.ok || !response.body) {
        if (response.status === 401) {
          await redirectToLoginOnUnauthorized();
          throw new Error('登录状态已失效');
        }
        if (response.status === 403) throw new Error('当前账号没有查看该节点的权限');
        throw new Error(`节点指标连接失败（HTTP ${response.status}）`);
      }
      onConnection(true);
      retryDelay = 1000;
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
          if (event === 'metrics-auth-error') {
            await redirectToLoginOnUnauthorized();
            throw new Error('登录状态已失效');
          }
          if (event === 'metrics' && data.length > 0) {
            const envelope = JSON.parse(data.join('\n')) as {
              data?: { metrics?: NodeMetrics };
            };
            if (envelope.data?.metrics) onMetrics(envelope.data.metrics);
          }
          boundary = buffer.indexOf('\n\n');
        }
      }
      await reader.cancel().catch(() => undefined);
      if (!signal.aborted) throw new Error('节点指标连接已断开');
    } catch (error) {
      if (signal.aborted) return;
      onConnection(false);
      if (error instanceof Error && /登录状态已失效|没有查看该节点的权限/.test(error.message)) throw error;
      await new Promise<void>((resolve) => {
        const timer = setTimeout(resolve, retryDelay);
        signal.addEventListener('abort', () => { clearTimeout(timer); resolve(); }, { once: true });
      });
      retryDelay = Math.min(retryDelay * 2, 10_000);
    }
  }
}
