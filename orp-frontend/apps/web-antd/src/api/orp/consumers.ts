import type { PageResult } from './types';

import { requestClient } from '#/api/request';

export type ConsumerStatus = 'disabled' | 'enabled';
export type APIKeyStatus = 'active' | 'revoked';

export interface APIConsumer {
  activeKeyCount: number;
  createdAt: string;
  createdBy: string;
  description: string;
  id: number;
  name: string;
  status: ConsumerStatus;
  updatedAt: string;
}

export interface APIConsumerPayload {
  description: string;
  name: string;
  status?: ConsumerStatus;
}

export interface APIKey {
  consumerId: number;
  createdAt: string;
  createdBy: string;
  expiresAt?: null | string;
  id: number;
  keyPrefix: string;
  lastUsedAt?: null | string;
  revokedAt?: null | string;
  revokedBy?: string;
  rotatedFromId?: number;
  status: APIKeyStatus;
}

export interface APIKeyIssue extends APIKey {
  /** Returned once by issue/rotate only. Never persist this value. */
  secret: string;
}

export interface APIKeyIssuePayload {
  expiresAt?: string;
}

export function listConsumersApi(params: {
  page: number;
  pageSize: number;
}): Promise<PageResult<APIConsumer>> {
  return requestClient.get('/orp/consumers', { params });
}

export function createConsumerApi(payload: APIConsumerPayload): Promise<APIConsumer> {
  return requestClient.post('/orp/consumers', payload);
}

export function updateConsumerApi(
  id: number,
  payload: APIConsumerPayload,
): Promise<APIConsumer> {
  return requestClient.put(`/orp/consumers/${id}`, payload);
}

/** Soft-deletes a consumer, disables it and revokes all active API keys. */
export function disableConsumerApi(id: number): Promise<APIConsumer> {
  return requestClient.delete(`/orp/consumers/${id}`);
}

export function listConsumerKeysApi(
  consumerId: number,
  params: { page: number; pageSize: number },
): Promise<PageResult<APIKey>> {
  return requestClient.get(`/orp/consumers/${consumerId}/keys`, { params });
}

export function issueConsumerKeyApi(
  consumerId: number,
  payload: APIKeyIssuePayload,
): Promise<APIKeyIssue> {
  return requestClient.post(`/orp/consumers/${consumerId}/keys`, payload);
}

export function rotateConsumerKeyApi(
  consumerId: number,
  keyId: number,
  payload: APIKeyIssuePayload,
): Promise<APIKeyIssue> {
  return requestClient.post(
    `/orp/consumers/${consumerId}/keys/${keyId}/rotate`,
    payload,
  );
}

export function revokeConsumerKeyApi(
  consumerId: number,
  keyId: number,
): Promise<APIKey> {
  return requestClient.delete(`/orp/consumers/${consumerId}/keys/${keyId}`);
}
