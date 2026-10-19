import type { PageResult } from './types';

import { requestClient } from '#/api/request';

/** 待下发实例节点 */
export interface PublishPendingNode {
  eventCount: number;
  host: string;
  id: number;
  lastChangeAt: null | string;
  name: string;
  status: string;
}

/** 按中心分组的待下发列表 */
export interface PublishGroup {
  centerId: number;
  centerName: string;
  nodes: PublishPendingNode[];
}

/** 待下发汇总（顶栏角标 + 比对页） */
export interface PublishSummary {
  activeBatchId: null | number;
  groups: PublishGroup[];
  pendingCount: number;
}

/** diff 行：1=新增 -1=删除 0=上下文；segs 为行内字符级差异区间 [起,止) */
export interface PublishDiffLine {
  s: string;
  segs?: [number, number][];
  t: -1 | 0 | 1;
}

/** 配置变更事件 */
export interface PublishChangeEvent {
  action: string;
  module: string;
  target: string;
  ts: string;
}

/** 成功下发基线与当前中心配置之间的最终资源级净差异 */
export interface PublishResourceDiff {
  action: string;
  fields?: string[];
  id: string;
  kind: string;
}

/** 单实例配置差异结果 */
export interface PublishDiffResult {
  addCount: number;
  centerId: number;
  centerName: string;
  delCount: number;
  diff: PublishDiffLine[];
  events: PublishChangeEvent[];
  expectedText: string;
  lastPublishedAt: null | string;
  node: { host: string; id: number; name: string; status: string };
  resourceDiff: PublishResourceDiff[];
  resourceDiffAvailable: boolean;
  runningText: string;
  version: number;
}

/** 下发批次条目状态 */
export type PublishItemStatus =
  | 'aborted'
  | 'deploying'
  | 'failed'
  | 'queued'
  | 'success'
  | 'validating';

export interface PublishBatchItem {
  batchIndex: number;
  centerId: number;
  centerName: string;
  error: null | string;
  finishedAt: null | string;
  itemId: number;
  nodeId: number;
  nodeName: string;
  requiresReconciliation: boolean;
  startedAt: null | string;
  status: PublishItemStatus;
  weight: number;
}

/** 分批批次组状态 */
export interface PublishBatchGroup {
  index: number;
  nodeIds: number[];
  status: 'aborted' | 'failed' | 'observing' | 'pending' | 'running' | 'success';
  weightSum: number;
}

/** 金丝雀观察配置 */
export interface PublishCanaryInfo {
  enabled: boolean;
  remaining: null | number;
  waitSeconds: number;
}

/** 下发批次实时状态 */
export interface PublishBatch {
  abortedCount: number;
  batchGroups: null | PublishBatchGroup[];
  canary: PublishCanaryInfo;
  createdAt: number;
  currentBatchIndex: number;
  done: boolean;
  failCount: number;
  id: number;
  items: PublishBatchItem[];
  mode: 'all' | 'weighted';
  operator: string;
  phase: 'aborted' | 'canary_observing' | 'done' | 'recovery_required' | 'running';
  successCount: number;
  totalBatches: number;
}

/** 下发历史记录 */
export interface PublishHistoryItem {
  addCount: number;
  batchId: number;
  centerId: number;
  centerName: string;
  createdAt: string;
  delCount: number;
  durationMs: number;
  events: PublishChangeEvent[];
  id: number;
  modules: string[];
  nodeId: number;
  nodeName: string;
  operator: string;
  result: 'aborted' | 'failed' | 'success';
}

export interface PublishHistoryParams {
  centerId?: number;
  nodeId?: number;
  page?: number;
  pageSize?: number;
}

/** 待下发变更汇总（顶栏角标 + 配置比对页分组数据） */
export function getPublishSummaryApi(): Promise<PublishSummary> {
  return requestClient.get('/orp/publish/summary');
}

/** 单实例配置差异（期望配置 vs 当前运行配置） */
export function getPublishDiffApi(nodeId: number): Promise<PublishDiffResult> {
  return requestClient.get('/orp/publish/diff', { params: { nodeId } });
}

/** 发起下发（单实例「保存并下发」或批量下发） */
export interface PublishStrategy {
  batchCount?: number;
  canary?: { enabled?: boolean; waitSeconds?: number };
  mode?: 'all' | 'weighted';
}

export function createPublishApi(
  nodeIds: number[],
  strategy?: PublishStrategy,
): Promise<{ batchId: number; total: number }> {
  return requestClient.post('/orp/publish', { nodeIds, strategy });
}

/** 下发批次实时状态轮询 */
export function getPublishBatchApi(batchId: number): Promise<PublishBatch> {
  return requestClient.get(`/orp/publish/batches/${batchId}`);
}

/** 下发历史（按中心 / 实例筛选） */
export function listPublishHistoryApi(
  params: PublishHistoryParams,
): Promise<PageResult<PublishHistoryItem>> {
  return requestClient.get('/orp/publish/history', { params });
}

/** 历史版本回滚结果 */
export interface PublishRollbackResult {
  batchId: number;
  nodeId: number;
  nodeName: string;
  rollbackTo: string;
}

/** 从历史快照重新预检并启动目标节点回滚发布 */
export function rollbackPublishApi(
  id: number,
): Promise<PublishRollbackResult> {
  return requestClient.post('/orp/publish/rollback', { id });
}

/** 语法预检错误条目 */
export interface PublishPrecheckError {
  line: number;
  message: string;
}

/** 单实例语法预检结果 */
export interface PublishPrecheckItem {
	addCount?: number;
	baselineRelease?: null | string;
	candidateDigest?: string;
	centerId: number;
  centerName: string;
	command: string;
	delCount?: number;
  errors: PublishPrecheckError[];
  nodeId: number;
  nodeName: string;
  output: string;
	passed: boolean;
	semanticDiff?: Array<{ action: string; fields?: string[]; id: string; kind: string }>;
}

/** 语法预检汇总结果 */
export interface PublishPrecheckResult {
  items: PublishPrecheckItem[];
  passed: boolean;
}

/** 在目标节点的非活动候选目录执行 nginx -t */
export function precheckPublishApi(
  nodeIds: number[],
): Promise<PublishPrecheckResult> {
  return requestClient.post('/orp/publish/precheck', { nodeIds });
}

/** 高频变更中心排行条目 */
export interface PublishHistoryTopCenter {
  count: number;
  name: string;
  percent: number;
}

/** 下发历史统计（成功率 + 高频变更中心 Top） */
export interface PublishHistoryStats {
  abortedBatches: number;
  failCount: number;
  successCount: number;
  successRate: number;
  topCenters: PublishHistoryTopCenter[];
  totalBatches: number;
  totalCount: number;
}

/** 下发历史统计卡片数据 */
export function getPublishHistoryStatsApi(): Promise<PublishHistoryStats> {
  return requestClient.get('/orp/publish/history/stats');
}

// 中止下发批次
export function abortPublishBatchApi(
  batchId: number,
): Promise<{ abortedCount: number; id: number; phase: string }> {
  return requestClient.post(`/orp/publish/batches/${batchId}/abort`);
}

// 立即推进（跳过观察等待期）
export function advancePublishBatchApi(
  batchId: number,
): Promise<{ id: number; phase: string }> {
  return requestClient.post(`/orp/publish/batches/${batchId}/advance`);
}

export interface PublishRecoveryConfirmation {
  itemId: number;
  observedReleaseUuid: string;
}

export interface PublishRecoveryContinueResult {
  alreadyAppliedCount: number;
  id: number;
  phase: 'running';
  resumedCount: number;
  skippedFailedCount: number;
}

/** Reconcile and safely resume a batch interrupted by a control-plane restart. */
export function reconcileContinuePublishBatchApi(
  batchId: number,
  confirmations: PublishRecoveryConfirmation[],
): Promise<PublishRecoveryContinueResult> {
  return requestClient.post(`/orp/publish/batches/${batchId}/reconcile-continue`, { confirmations });
}
