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

import { requestClient } from '#/api/request';

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
