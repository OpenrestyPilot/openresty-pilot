/**
 * Service Worker：ORP 内嵌 Mock（浏览器端 /api/** 接管）
 *
 * 沙盒预览场景下，Vben Admin 生产包（public/vben/）由 vite 静态托管，
 * 后端 API 不存在 —— 本 SW 复用 web-antd mock 的 dispatchApi 纯函数，
 * 在浏览器内拦截全部 /api/** fetch 并返回与 vite 中间件一致的响应。
 */
import { dispatchApi } from './orp-mock';

declare const self: ServiceWorkerGlobalScope;

const SW_VERSION = 'orp-mock-v2';

/** SW 内部调试日志（GET /api/__swdebug 可读取） */
const swLogs: string[] = [];

function swLog(msg: string) {
  swLogs.push(`[${new Date().toISOString().slice(11, 23)}] ${msg}`);
  if (swLogs.length > 60) swLogs.shift();
}

self.addEventListener('install', () => {
  // 立即激活，替换旧版本 SW
  void self.skipWaiting();
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    (async () => {
      // 清理旧缓存
      const keys = await caches.keys();
      await Promise.all(
        keys.filter((key) => key !== SW_VERSION).map((key) => caches.delete(key)),
      );
      await self.clients.claim();
    })(),
  );
});

self.addEventListener('fetch', (event) => {
  const request = event.request;
  const url = new URL(request.url);

  if (url.origin !== self.location.origin || !url.pathname.startsWith('/api/')) {
    return;
  }

  // 调试端点：返回 SW 内部日志
  if (url.pathname === '/api/__swdebug' && request.method === 'GET') {
    swLog('READ __swdebug');
    event.respondWith(
      new Response(JSON.stringify(swLogs, null, 1), {
        headers: { 'content-type': 'application/json;charset=UTF-8' },
      }),
    );
    return;
  }

  swLog(`IN ${request.method} ${url.pathname}`);

  event.respondWith(
    (async () => {
      let body: Record<string, any> = {};
      if (['PATCH', 'POST', 'PUT'].includes(request.method.toUpperCase())) {
        try {
          const raw = await request.clone().text();
          body = raw ? (JSON.parse(raw) as Record<string, any>) : {};
          swLog(`BODY-OK len=${raw.length} keys=${Object.keys(body).join(',')}`);
        } catch (error) {
          swLog(`BODY-ERR ${String(error)}`);
          body = {};
        }
      }

      const headers: Record<string, string> = {};
      request.headers.forEach((value, key) => {
        headers[key.toLowerCase()] = value;
      });

      const out = dispatchApi({
        body,
        headers,
        method: request.method,
        path: url.pathname,
        query: Object.fromEntries(url.searchParams),
      });

      const bodyText = JSON.stringify(out.body);
      swLog(
        `OUT ${out.status} bodyLen=${bodyText === undefined ? 'UNDEF' : bodyText.length} head=${bodyText?.slice(0, 80)}`,
      );

      return new Response(bodyText ?? '', {
        headers: { 'content-type': 'application/json;charset=UTF-8' },
        status: out.status,
      });
    })(),
  );
});

export {};
