import type { GeoIPDatabaseStatus } from './types';

import { useAccessStore } from '@vben/stores';

import { redirectToLoginOnUnauthorized, requestClient } from '#/api/request';

export function getGeoIPDatabaseStatusApi(): Promise<GeoIPDatabaseStatus> {
  return requestClient.get('/orp/geoip-database');
}

export async function importGeoIPDatabaseApi(file: File): Promise<GeoIPDatabaseStatus> {
  const form = new FormData();
  form.append('file', file);
  const token = useAccessStore().accessToken;
  const response = await fetch('/api/orp/geoip-database/import', {
    body: form,
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    method: 'POST',
  });
  if (response.status === 401) {
    await redirectToLoginOnUnauthorized();
    throw new Error('登录状态已失效，正在跳转登录页');
  }
  const result = (await response.json()) as {
    code?: number;
    data?: GeoIPDatabaseStatus;
    message?: string;
  };
  if (!response.ok || result.code !== 0 || !result.data) {
    throw new Error(result.message || `GeoIP 数据库导入失败（HTTP ${response.status}）`);
  }
  return result.data;
}
