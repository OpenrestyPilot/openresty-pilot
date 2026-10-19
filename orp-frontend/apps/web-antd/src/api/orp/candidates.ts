import type { PageResult } from './types';
import type { PublishStrategy } from './publish';

import { requestClient } from '#/api/request';

export interface ConfigurationCandidatePayload {
  centerId: number;
  concurrency: number;
  nodeIds: number[];
  publishOnValidationSuccess?: boolean;
  publishStrategy?: PublishStrategy;
}

export type CandidatePublishState = 'aborted' | 'failed' | 'not_requested' | 'preparing' | 'recovery_required' | 'started' | 'succeeded' | 'waiting_validation';

export interface ConfigurationCandidate {
  archived: boolean;
  attemptId: null | number;
  attemptNo: null | number;
  attemptState: null | string;
  blocker: null | string;
  blocked: boolean;
  concurrency: null | number;
  createdAt: string;
  creator: string;
  digest: string;
  id: number;
  targetIds: number[];
  candidateUuid: string;
  centerId: number;
  publishBatchId: null | number;
  publishError: null | string;
  publishOnValidationSuccess: boolean;
  publishState: CandidatePublishState;
}

export interface CandidateDiff {
  addedLines: number;
  baselineDigest: null | string;
  baselineVersion: null | string;
  candidateDigest: string;
  deletedLines: number;
  nodeId: number;
  semanticDiff: Array<Record<string, unknown>>;
  textDiff: Array<{ s: string; t: -1 | 0 | 1 }>;
}

export interface CandidateJob {
  blocked: boolean;
  blocker: null | string;
  command: null | string;
  finishedAt: null | string;
  nodeId: number;
  output: null | string;
  startedAt: null | string;
  state: 'adapter_required' | 'cancelled' | 'failed' | 'paused' | 'passed' | 'queued' | 'running' | 'unknown';
}

export interface CandidateAttempt {
  attemptNo: number;
  blocked: boolean;
  blocker: null | string;
  concurrency: number;
  createdAt: string;
  finishedAt: null | string;
  id: number;
  jobs: CandidateJob[];
  state: string;
}

export interface CandidateDetail {
  archived: boolean;
  createdAt: string;
  creator: string;
  digest: string;
  id: number;
  candidateUuid: string;
  centerId: number;
  targetIds: number[];
  publishBatchId: null | number;
  publishError: null | string;
  publishOnValidationSuccess: boolean;
  publishState: CandidatePublishState;
  attempts: CandidateAttempt[];
  diffs?: CandidateDiff[];
}

export interface ValidationCenterPolicy {
  centerId: number;
  default?: boolean;
  maxInflight: number;
}

export function listConfigurationCandidatesApi(params: {
  archived?: boolean;
  centerId?: number;
  page?: number;
  pageSize?: number;
}): Promise<PageResult<ConfigurationCandidate>> {
  return requestClient.get('/orp/candidates', { params });
}

export function createConfigurationCandidateApi(
  payload: ConfigurationCandidatePayload,
): Promise<{ attemptId: number; candidateUuid: string; id: number; maxInflight: number; publishBatchId: null; publishError: null; publishOnValidationSuccess: boolean; publishState: CandidatePublishState; status: string }> {
  return requestClient.post('/orp/candidates', payload);
}

export interface CandidatePublishResult {
  batchId: number;
  candidateId: number;
  mode: 'all' | 'weighted';
  phase: 'running';
  total: number;
}

export function publishConfigurationCandidateApi(
  id: number,
  strategy?: PublishStrategy,
): Promise<CandidatePublishResult> {
  return requestClient.post(`/orp/candidates/${id}/publish`, strategy ? { strategy } : {});
}

export function getConfigurationCandidateApi(id: number): Promise<CandidateDetail> {
  return requestClient.get(`/orp/candidates/${id}`);
}

export type CandidateAction = 'archive' | 'continue' | 'restore' | 'stop';

export function actOnConfigurationCandidateApi(
  id: number,
  action: CandidateAction | 'retry',
  body?: { nodeId: number },
): Promise<{ action: string; id: number }> {
  return requestClient.post(`/orp/candidates/${id}/${action}`, body);
}

export function getValidationCenterPolicyApi(centerId: number): Promise<ValidationCenterPolicy> {
  return requestClient.get(`/orp/validation-centers/${centerId}/policy`);
}

export function updateValidationCenterPolicyApi(
  centerId: number,
  maxInflight: number,
): Promise<ValidationCenterPolicy> {
  return requestClient.put(`/orp/validation-centers/${centerId}/policy`, { maxInflight });
}
