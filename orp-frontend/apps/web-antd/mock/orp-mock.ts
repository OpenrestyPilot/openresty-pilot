/**
 * ORP 内嵌 Mock 服务（vite dev 中间件版）
 *
 * 在沙盒预览/免后端环境下于 vite dev server 进程内承接全部 /api/** 请求，
 * 与 backend-mock（Nitro, 端口 5320）保持接口契约一致：
 *   - 数据与业务规则直接复用 backend-mock/utils/orp-store（单一数据源）
 *   - 认证采用简化令牌（mock-access-<username>），不做真实验签
 *   - 响应结构统一 code=0 成功 / code=-1 失败并携带 message
 */
import type { IncomingMessage, ServerResponse } from 'node:http';
import type {
  OrpLocationRule,
  OrpNodeStatus,
  OrpUpstreamGroup,
  OrpUpstreamNode,
} from '../../backend-mock/utils/orp-store';
import type { Plugin } from 'vite';

import type { UserInfo } from '../../backend-mock/utils/mock-data';

import { MOCK_USERS } from '../../backend-mock/utils/mock-data';
import {
  auditLogs,
  centerName,
  centerUsage,
  centers,
  certificates,
  dnsResolvers,
  httpListeners,
  httpPortConflict,
  nextId,
  nextRouteId,
  nodes,
  now,
  recordAudit,
  streamPortConflict,
  streamServices,
  upstreamGroups,
  upstreamNameExists,
  upstreamReferences,
  validateCertificatePem,
} from '../../backend-mock/utils/orp-store';

/* ----------------------------- 基础工具 ----------------------------- */

function ok<T>(data: T) {
  return { code: 0, data, error: null, message: 'ok' };
}

function fail(message: string, error: unknown = null) {
  return { code: -1, data: null, error: error ?? message, message };
}

function paginate<T>(pageNo: number, pageSize: number, array: T[]): T[] {
  const offset = (pageNo - 1) * pageSize;
  return offset + pageSize >= array.length
    ? array.slice(offset)
    : array.slice(offset, offset + pageSize);
}

function pageOk<T>(page: number, pageSize: number, list: T[]) {
  return ok({ items: paginate(page, pageSize, list), total: list.length });
}

function toInt(value: unknown, fallback: number): number {
  const parsed = Number.parseInt(String(value ?? ''), 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

/* ----------------------------- 认证（简化令牌，纯函数） ----------------------------- */

function userFromHeaders(
  headers: Record<string, string>,
): null | (typeof MOCK_USERS)[number] {
  const raw = headers.authorization ?? '';
  // 兼容 "Bearer mock-access-<user>" 与裸 "mock-access-<user>" 两种形态
  const token = raw.replace(/^Bearer\s+/i, '').trim();
  if (!token.startsWith('mock-access-')) {
    return null;
  }
  return MOCK_USERS.find((item) => item.username === token.slice(12)) ?? null;
}

function ipFromHeaders(headers: Record<string, string>): string {
  const forwarded = headers['x-forwarded-for'] ?? '';
  return forwarded.split(',')[0]?.trim() || '127.0.0.1';
}

function unauthorized() {
  return { status: 401, body: fail('Unauthorized Exception') };
}

/* ----------------------------- 路由注册 ----------------------------- */

interface Ctx {
  body: Record<string, any>;
  id: number;
  ip: string;
  operator: string;
  query: Record<string, string>;
  user: null | UserInfo;
}

type Handler = (ctx: Ctx) => { body: any; status?: number };
interface RouteDef {
  handler: Handler;
  method: string;
  pattern: null | RegExp;
  path: string;
}

const routes: RouteDef[] = [];

function route(method: string, path: string, handler: Handler) {
  const pattern = path.includes(':')
    ? new RegExp(`^${path.replace(/:[^/]+/g, '([^/]+)')}$`)
    : null;
  routes.push({ handler, method, path, pattern });
}

async function readBody(
  req: IncomingMessage,
): Promise<Record<string, any>> {
  if (!['PATCH', 'POST', 'PUT'].includes((req.method ?? '').toUpperCase())) {
    return {};
  }
  const chunks: Buffer[] = [];
  for await (const chunk of req) {
    chunks.push(chunk as Buffer);
  }
  const raw = Buffer.concat(chunks).toString('utf8');
  if (!raw) {
    return {};
  }
  try {
    return JSON.parse(raw) as Record<string, any>;
  } catch {
    return {};
  }
}

function sendJson(res: ServerResponse, status: number, payload: unknown) {
  res.statusCode = status;
  res.setHeader('content-type', 'application/json;charset=utf-8');
  res.end(JSON.stringify(payload));
}

/* ----------------------------- 认证/用户接口 ----------------------------- */

route('POST', '/api/auth/login', ({ body }) => {
  const { password, username } = body;
  if (!password || !username) {
    return {
      status: 400,
      body: fail('BadRequestException', 'Username and password are required'),
    };
  }
  const findUser = MOCK_USERS.find(
    (item) => item.username === username && item.password === password,
  );
  if (!findUser) {
    return { status: 403, body: fail('Username or password is incorrect.') };
  }
  const { password: _pwd, ...userInfo } = findUser;
  return ok({ ...userInfo, accessToken: `mock-access-${username}` });
});

route('POST', '/api/auth/logout', () => ok(null));

route('POST', '/api/auth/refresh', ({ user }) => {
  if (!user) {
    return unauthorized();
  }
  return ok({ accessToken: `mock-access-${user.username}` });
});

route('GET', '/api/auth/codes', () => ok([]));

route('GET', '/api/user/info', ({ user }) => {
  if (!user) {
    return unauthorized();
  }
  const { password: _pwd, ...userInfo } = user;
  return ok(userInfo);
});

route('GET', '/api/menu/all', ({ user }) => {
  if (!user) {
    return unauthorized();
  }
  return ok([]);
});

/* ----------------------------- 中心管理 ----------------------------- */

const CODE_PATTERN = /^[a-z0-9-]{2,32}$/;

route('GET', '/api/orp/centers', ({ query }) => {
  const keyword = (query.keyword ?? '').trim();
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...centers].sort((a, b) => b.id - a.id);
  if (keyword) {
    list = list.filter(
      (item) => item.name.includes(keyword) || item.code.includes(keyword),
    );
  }
  return { body: pageOk(page, pageSize, list) };
});

route('POST', '/api/orp/centers', ({ body, ip, operator }) => {
  const code = String(body?.code ?? '').trim();
  const name = String(body?.name ?? '').trim();
  const description = String(body?.description ?? '').trim();

  if (!code || !name) {
    return { status: 400, body: fail('中心名称与标识码为必填项') };
  }
  if (!CODE_PATTERN.test(code)) {
    return {
      status: 400,
      body: fail('标识码格式不正确：仅允许小写字母、数字与中划线，长度 2-32'),
    };
  }
  if (centers.some((item) => item.code === code)) {
    return { status: 400, body: fail(`中心标识码 ${code} 已存在`) };
  }

  const center = {
    code,
    createdAt: now(),
    description,
    id: nextId(centers),
    name,
    updatedAt: now(),
  };
  centers.push(center);
  recordAudit(operator, ip, {
    action: 'create',
    detail: `名称：${name}；标识：${code}；描述：${description || '无'}`,
    module: 'center',
    target: `center:${code}`,
  });
  return { body: ok(center) };
});

route('PUT', '/api/orp/centers/:id', ({ body, id, ip, operator }) => {
  const center = centers.find((item) => item.id === id);
  if (!center) {
    return { status: 404, body: fail('中心不存在或已被删除') };
  }

  const code = String(body?.code ?? center.code).trim();
  const name = String(body?.name ?? '').trim();
  const description = String(body?.description ?? '').trim();

  if (!name) {
    return { status: 400, body: fail('中心名称为必填项') };
  }
  if (!CODE_PATTERN.test(code)) {
    return {
      status: 400,
      body: fail('标识码格式不正确：仅允许小写字母、数字与中划线，长度 2-32'),
    };
  }
  if (centers.some((item) => item.code === code && item.id !== id)) {
    return { status: 400, body: fail(`中心标识码 ${code} 已存在`) };
  }

  const before = `名称：${center.name}；标识：${center.code}；描述：${center.description || '无'}`;
  Object.assign(center, { code, description, name, updatedAt: now() });
  recordAudit(operator, ip, {
    action: 'update',
    detail: `变更前【${before}】→ 变更后【名称：${name}；标识：${code}；描述：${description || '无'}】`,
    module: 'center',
    target: `center:${code}`,
  });
  return { body: ok(center) };
});

route('DELETE', '/api/orp/centers/:id', ({ id, ip, operator }) => {
  const index = centers.findIndex((item) => item.id === id);
  if (index === -1) {
    return { status: 404, body: fail('中心不存在或已被删除') };
  }

  const usage = centerUsage(id);
  if (usage.total > 0) {
    return {
      status: 400,
      body: fail(`中心下仍存在关联资源（${usage.detail}），请先清空后再删除`),
    };
  }

  const [removed] = centers.splice(index, 1);
  recordAudit(operator, ip, {
    action: 'delete',
    detail: `名称：${removed?.name}；标识：${removed?.code}`,
    module: 'center',
    target: `center:${removed?.code}`,
  });
  return { body: ok(null) };
});

/* ----------------------------- 节点实例 ----------------------------- */

const HOST_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9.-]*$/;
const STATUS_LIST: OrpNodeStatus[] = ['maintenance', 'offline', 'online'];

route('GET', '/api/orp/nodes', ({ query }) => {
  const centerId = toInt(query.centerId, Number.NaN);
  const keyword = (query.keyword ?? '').trim();
  const status = query.status ?? '';
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...nodes].sort((a, b) => a.id - b.id);
  if (centerId) {
    list = list.filter((item) => item.centerId === centerId);
  }
  if (status && status !== 'all') {
    list = list.filter((item) => item.status === status);
  }
  if (keyword) {
    list = list.filter(
      (item) => item.name.includes(keyword) || item.host.includes(keyword),
    );
  }
  const items = list.map((item) => ({
    ...item,
    centerName: centerName(item.centerId),
  }));
  return { body: pageOk(page, pageSize, items) };
});

route('POST', '/api/orp/nodes', ({ body, ip, operator }) => {
  const centerId = toInt(body?.centerId, Number.NaN);
  const name = String(body?.name ?? '').trim();
  const host = String(body?.host ?? '').trim();
  const controlEndpoint = String(body?.controlEndpoint ?? '').trim();
  const osInfo = String(body?.osInfo ?? '').trim();
  const activeVersion = String(body?.activeVersion ?? '').trim();

  if (!name || !host || !centerId) {
    return { status: 400, body: fail('节点名称、主机地址与所属中心为必填项') };
  }
  if (!centers.some((item) => item.id === centerId)) {
    return { status: 400, body: fail('所属中心不存在，请刷新后重试') };
  }
  if (!HOST_PATTERN.test(host)) {
    return { status: 400, body: fail('主机地址格式不正确（支持 IPv4 或主机名）') };
  }
  if (nodes.some((item) => item.name === name)) {
    return { status: 400, body: fail(`节点名称 ${name} 已存在`) };
  }
  const status: OrpNodeStatus = STATUS_LIST.includes(body?.status)
    ? body.status
    : 'offline';

  const node = {
    activeVersion: activeVersion || '未发布',
    centerId,
    controlEndpoint: controlEndpoint || '未登记',
    createdAt: now(),
    host,
    id: nextId(nodes),
    name,
    osInfo: osInfo || '未登记',
    status,
  };
  nodes.push(node);
  recordAudit(operator, ip, {
    action: 'create',
    detail: `名称：${name}；地址：${host}；所属中心：${centerName(centerId)}`,
    module: 'node',
    target: `node:${name}`,
  });
  return { body: ok(node) };
});

route('PUT', '/api/orp/nodes/:id', ({ body, id, ip, operator }) => {
  const node = nodes.find((item) => item.id === id);
  if (!node) {
    return { status: 404, body: fail('节点不存在或已被删除') };
  }

  const centerId = toInt(body?.centerId ?? node.centerId, Number.NaN);
  const name = String(body?.name ?? node.name).trim();
  const host = String(body?.host ?? node.host).trim();

  if (!name || !host || !centerId) {
    return { status: 400, body: fail('节点名称、主机地址与所属中心为必填项') };
  }
  if (!centers.some((item) => item.id === centerId)) {
    return { status: 400, body: fail('所属中心不存在，请刷新后重试') };
  }
  if (!HOST_PATTERN.test(host)) {
    return { status: 400, body: fail('主机地址格式不正确（支持 IPv4 或主机名）') };
  }
  if (nodes.some((item) => item.name === name && item.id !== id)) {
    return { status: 400, body: fail(`节点名称 ${name} 已存在`) };
  }

  const before = `名称：${node.name}；地址：${node.host}；中心：${centerName(node.centerId)}`;
  node.centerId = centerId;
  node.name = name;
  node.host = host;
  node.controlEndpoint =
    String(body?.controlEndpoint ?? node.controlEndpoint).trim() || '未登记';
  node.osInfo = String(body?.osInfo ?? node.osInfo).trim() || '未登记';
  node.activeVersion =
    String(body?.activeVersion ?? node.activeVersion).trim() || '未发布';
  if (STATUS_LIST.includes(body?.status)) {
    node.status = body.status;
  }
  recordAudit(operator, ip, {
    action: 'update',
    detail: `变更前【${before}】→ 变更后【名称：${name}；地址：${host}；中心：${centerName(centerId)}】`,
    module: 'node',
    target: `node:${name}`,
  });
  return { body: ok(node) };
});

route('POST', '/api/orp/nodes/:id/offline', ({ id, ip, operator }) => {
  const node = nodes.find((item) => item.id === id);
  if (!node) {
    return { status: 404, body: fail('节点不存在或已被删除') };
  }

  const before = node.status;
  node.status = 'offline';
  recordAudit(operator, ip, {
    action: 'offline',
    detail: `节点 ${node.name}（${centerName(node.centerId)}）状态由 ${before} 变更为 offline`,
    module: 'node',
    target: `node:${node.name}`,
  });
  return { body: ok(node) };
});

route('DELETE', '/api/orp/nodes/:id', ({ id, ip, operator }) => {
  const index = nodes.findIndex((item) => item.id === id);
  if (index === -1) {
    return { status: 404, body: fail('节点不存在或已被删除') };
  }

  const removed = nodes[index];
  if (removed && removed.status === 'online') {
    return {
      status: 400,
      body: fail(`节点 ${removed.name} 处于在线状态，请先执行下线操作后再移除`),
    };
  }

  nodes.splice(index, 1);
  recordAudit(operator, ip, {
    action: 'delete',
    detail: `名称：${removed?.name}；地址：${removed?.host}`,
    module: 'node',
    target: `node:${removed?.name}`,
  });
  return { body: ok(null) };
});

route('GET', '/api/orp/nodes/:id/metrics', ({ id }) => {
  const node = nodes.find((item) => item.id === id);
  if (!node) {
    return { status: 404, body: fail('节点不存在或已被删除') };
  }

  const seed = node.id;
  const online = node.status === 'online';
  const round = (value: number, digits = 1) =>
    Number.parseFloat(value.toFixed(digits));

  return {
    body: ok({
      metrics: {
        bandwidthInMbps: online ? round(80 + ((seed * 37) % 260)) : 0,
        bandwidthOutMbps: online ? round(120 + ((seed * 53) % 340)) : 0,
        connections: online ? 1000 + seed * 837 : 0,
        cpuPercent: online ? round(18 + ((seed * 13) % 45)) : 0,
        errorRatePercent: online ? round((seed % 7) / 100, 2) : 0,
        memPercent: online ? round(35 + ((seed * 11) % 50)) : 0,
        qps: online ? 500 + seed * 321 : 0,
        requestTotal24h: online ? 800_000 + seed * 123_457 : 0,
        uptimeDays: seed * 3 + 12,
      },
      node: { ...node, centerName: centerName(node.centerId) },
    }),
  };
});

/* ----------------------------- HTTP 监听 ----------------------------- */

const DOMAIN_PATTERN = /^(\*\.)?([a-zA-Z0-9-]+\.)+[a-zA-Z]{2,}$/;

function normalizeRoutes(raw: unknown, keepExistingId: boolean): OrpLocationRule[] {
  if (!Array.isArray(raw)) {
    return [];
  }
  return raw
    .map((item) => {
      const route = item as Record<string, any>;
      const existingId = toInt(route?.id, 0);
      return {
        id:
          keepExistingId && existingId > 0 ? existingId : nextRouteId(),
        path: String(route?.path ?? '').trim(),
        proxyTimeoutMs: toInt(route?.proxyTimeoutMs ?? 30_000, 30_000) || 30_000,
        upstream: String(route?.upstream ?? '').trim(),
      };
    })
    .filter((item) => item.path && item.upstream);
}

route('GET', '/api/orp/http-listeners', ({ query }) => {
  const centerId = toInt(query.centerId, Number.NaN);
  const keyword = (query.keyword ?? '').trim();
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...httpListeners].sort((a, b) => a.id - b.id);
  if (centerId) {
    list = list.filter((item) => item.centerId === centerId);
  }
  if (keyword) {
    list = list.filter((item) => item.domain.includes(keyword));
  }
  const items = list.map((item) => ({
    ...item,
    centerName: centerName(item.centerId),
    certName: item.tlsCertId
      ? certificates.find((cert) => cert.id === item.tlsCertId)?.name ?? '-'
      : '-',
    routeCount: item.routes.length,
  }));
  return { body: pageOk(page, pageSize, items) };
});

route('POST', '/api/orp/http-listeners', ({ body, ip, operator }) => {
  const centerId = toInt(body?.centerId, Number.NaN);
  const domain = String(body?.domain ?? '').trim();
  const port = toInt(body?.port, Number.NaN);
  const tlsCertId = body?.tlsCertId ? Number(body.tlsCertId) : null;

  if (!centerId || !domain || !port) {
    return { status: 400, body: fail('所属中心、域名与监听端口为必填项') };
  }
  if (!DOMAIN_PATTERN.test(domain)) {
    return {
      status: 400,
      body: fail('域名格式不正确，示例：api.example.cn 或 *.example.cn'),
    };
  }
  if (!(port >= 1 && port <= 65_535)) {
    return { status: 400, body: fail('监听端口必须是 1-65535 之间的整数') };
  }
  const conflict = httpPortConflict(centerId, port);
  if (conflict) {
    return {
      status: 400,
      body: fail(
        `端口冲突：${centerName(centerId)} 下已存在 HTTP 监听端口 ${port}（${conflict.domain}）`,
      ),
    };
  }
  if (tlsCertId && !certificates.some((cert) => cert.id === tlsCertId)) {
    return { status: 400, body: fail('所选 TLS 证书不存在，请刷新后重试') };
  }

  const listener = {
    centerId,
    createdAt: now(),
    domain,
    id: nextId(httpListeners),
    port,
    routes: normalizeRoutes(body?.routes, false),
    tlsCertId,
  };
  httpListeners.push(listener);
  recordAudit(operator, ip, {
    action: 'create',
    detail: `域名：${domain}；端口：${port}；中心：${centerName(centerId)}；路由 ${listener.routes.length} 条`,
    module: 'http',
    target: `http:${domain}:${port}`,
  });
  return { body: ok(listener) };
});

route('PUT', '/api/orp/http-listeners/:id', ({ body, id, ip, operator }) => {
  const listener = httpListeners.find((item) => item.id === id);
  if (!listener) {
    return { status: 404, body: fail('监听配置不存在或已被删除') };
  }

  const centerId = toInt(body?.centerId ?? listener.centerId, Number.NaN);
  const domain = String(body?.domain ?? listener.domain).trim();
  const port = toInt(body?.port ?? listener.port, Number.NaN);
  const tlsCertId = body?.tlsCertId ? Number(body.tlsCertId) : null;

  if (!centerId || !domain || !port) {
    return { status: 400, body: fail('所属中心、域名与监听端口为必填项') };
  }
  if (!DOMAIN_PATTERN.test(domain)) {
    return {
      status: 400,
      body: fail('域名格式不正确，示例：api.example.cn 或 *.example.cn'),
    };
  }
  if (!(port >= 1 && port <= 65_535)) {
    return { status: 400, body: fail('监听端口必须是 1-65535 之间的整数') };
  }
  const conflict = httpPortConflict(centerId, port, id);
  if (conflict) {
    return {
      status: 400,
      body: fail(
        `端口冲突：${centerName(centerId)} 下已存在 HTTP 监听端口 ${port}（${conflict.domain}）`,
      ),
    };
  }
  if (tlsCertId && !certificates.some((cert) => cert.id === tlsCertId)) {
    return { status: 400, body: fail('所选 TLS 证书不存在，请刷新后重试') };
  }

  const before = `域名：${listener.domain}；端口：${listener.port}；路由 ${listener.routes.length} 条`;
  const routes = normalizeRoutes(body?.routes, true);
  Object.assign(listener, { centerId, domain, port, routes, tlsCertId });
  recordAudit(operator, ip, {
    action: 'update',
    detail: `变更前【${before}】→ 变更后【域名：${domain}；端口：${port}；路由 ${routes.length} 条】`,
    module: 'http',
    target: `http:${domain}:${port}`,
  });
  return { body: ok(listener) };
});

route('DELETE', '/api/orp/http-listeners/:id', ({ id, ip, operator }) => {
  const index = httpListeners.findIndex((item) => item.id === id);
  if (index === -1) {
    return { status: 404, body: fail('监听配置不存在或已被删除') };
  }

  const [removed] = httpListeners.splice(index, 1);
  recordAudit(operator, ip, {
    action: 'delete',
    detail: `域名：${removed?.domain}；端口：${removed?.port}；中心：${centerName(removed?.centerId ?? 0)}；路由 ${removed?.routes.length ?? 0} 条`,
    module: 'http',
    target: `http:${removed?.domain}:${removed?.port}`,
  });
  return { body: ok(null) };
});

/* ----------------------------- Stream 服务 ----------------------------- */

const BACKEND_PATTERN = /^[a-zA-Z0-9.-]+:\d{1,5}$/;

route('GET', '/api/orp/stream-services', ({ query }) => {
  const centerId = toInt(query.centerId, Number.NaN);
  const protocol = query.protocol ?? '';
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...streamServices].sort((a, b) => a.id - b.id);
  if (centerId) {
    list = list.filter((item) => item.centerId === centerId);
  }
  if (protocol && protocol !== 'all') {
    list = list.filter((item) => item.protocol === protocol);
  }
  const items = list.map((item) => ({
    ...item,
    centerName: centerName(item.centerId),
  }));
  return { body: pageOk(page, pageSize, items) };
});

route('POST', '/api/orp/stream-services', ({ body, ip, operator }) => {
  const centerId = toInt(body?.centerId, Number.NaN);
  const protocol = body?.protocol === 'udp' ? 'udp' : 'tcp';
  const listenPort = toInt(body?.listenPort, Number.NaN);
  const listenAddress = String(body?.listenAddress ?? '0.0.0.0').trim();
  const description = String(body?.description ?? '').trim();
  const backends = Array.isArray(body?.backends)
    ? body.backends.map((item: unknown) => String(item).trim()).filter(Boolean)
    : [];

  if (!centerId || !listenPort) {
    return { status: 400, body: fail('所属中心与监听端口为必填项') };
  }
  if (!(listenPort >= 1 && listenPort <= 65_535)) {
    return { status: 400, body: fail('监听端口必须是 1-65535 之间的整数') };
  }
  if (backends.length === 0) {
    return { status: 400, body: fail('至少配置一个后端转发地址（host:port）') };
  }
  if (backends.some((item: string) => !BACKEND_PATTERN.test(item))) {
    return {
      status: 400,
      body: fail('后端地址格式不正确，示例：mysql-cluster-01:3306'),
    };
  }
  const conflict = streamPortConflict(centerId, listenPort, protocol);
  if (conflict) {
    return {
      status: 400,
      body: fail(
        `端口冲突：${centerName(centerId)} 下已存在 ${protocol.toUpperCase()} 监听端口 ${listenPort}（${conflict.description || '未命名服务'}）`,
      ),
    };
  }

  const service = {
    backends,
    centerId,
    createdAt: now(),
    description,
    id: nextId(streamServices),
    listenAddress: listenAddress || '0.0.0.0',
    listenPort,
    protocol: protocol as 'tcp' | 'udp',
  };
  streamServices.push(service);
  recordAudit(operator, ip, {
    action: 'create',
    detail: `协议：${protocol.toUpperCase()}；监听：${listenAddress}:${listenPort}；后端：${backends.join(', ')}；中心：${centerName(centerId)}`,
    module: 'stream',
    target: `stream:${protocol}/${listenPort}`,
  });
  return { body: ok(service) };
});

route('PUT', '/api/orp/stream-services/:id', ({ body, id, ip, operator }) => {
  const service = streamServices.find((item) => item.id === id);
  if (!service) {
    return { status: 404, body: fail('Stream 服务不存在或已被删除') };
  }

  const centerId = toInt(body?.centerId ?? service.centerId, Number.NaN);
  const protocol =
    body?.protocol === 'udp'
      ? 'udp'
      : body?.protocol === 'tcp'
        ? 'tcp'
        : service.protocol;
  const listenPort = toInt(body?.listenPort ?? service.listenPort, Number.NaN);
  const listenAddress = String(
    body?.listenAddress ?? service.listenAddress,
  ).trim();
  const description = String(body?.description ?? service.description).trim();
  const backends = Array.isArray(body?.backends)
    ? body.backends.map((item: unknown) => String(item).trim()).filter(Boolean)
    : service.backends;

  if (!centerId || !listenPort) {
    return { status: 400, body: fail('所属中心与监听端口为必填项') };
  }
  if (!(listenPort >= 1 && listenPort <= 65_535)) {
    return { status: 400, body: fail('监听端口必须是 1-65535 之间的整数') };
  }
  if (backends.length === 0) {
    return { status: 400, body: fail('至少配置一个后端转发地址（host:port）') };
  }
  if (backends.some((item: string) => !BACKEND_PATTERN.test(item))) {
    return {
      status: 400,
      body: fail('后端地址格式不正确，示例：mysql-cluster-01:3306'),
    };
  }
  const conflict = streamPortConflict(centerId, listenPort, protocol, id);
  if (conflict) {
    return {
      status: 400,
      body: fail(
        `端口冲突：${centerName(centerId)} 下已存在 ${protocol.toUpperCase()} 监听端口 ${listenPort}（${conflict.description || '未命名服务'}）`,
      ),
    };
  }

  const before = `监听：${service.listenAddress}:${service.listenPort}（${service.protocol.toUpperCase()}）；后端：${service.backends.join(', ')}`;
  Object.assign(service, {
    backends,
    centerId,
    description,
    listenAddress: listenAddress || '0.0.0.0',
    listenPort,
    protocol,
  });
  recordAudit(operator, ip, {
    action: 'update',
    detail: `变更前【${before}】→ 变更后【监听：${listenAddress}:${listenPort}（${protocol.toUpperCase()}）；后端：${backends.join(', ')}】`,
    module: 'stream',
    target: `stream:${protocol}/${listenPort}`,
  });
  return { body: ok(service) };
});

route('DELETE', '/api/orp/stream-services/:id', ({ id, ip, operator }) => {
  const index = streamServices.findIndex((item) => item.id === id);
  if (index === -1) {
    return { status: 404, body: fail('Stream 服务不存在或已被删除') };
  }

  const [removed] = streamServices.splice(index, 1);
  recordAudit(operator, ip, {
    action: 'delete',
    detail: `协议：${removed?.protocol.toUpperCase()}；监听：${removed?.listenAddress}:${removed?.listenPort}；中心：${centerName(removed?.centerId ?? 0)}`,
    module: 'stream',
    target: `stream:${removed?.protocol}/${removed?.listenPort}`,
  });
  return { body: ok(null) };
});

/* ----------------------------- TLS 证书 ----------------------------- */

route('GET', '/api/orp/certificates', ({ query }) => {
  const keyword = (query.keyword ?? '').trim();
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...certificates].sort((a, b) => a.id - b.id);
  if (keyword) {
    list = list.filter(
      (item) => item.name.includes(keyword) || item.domains.includes(keyword),
    );
  }
  // 私钥不随列表下发
  const items = list.map(({ privateKey: _key, ...rest }) => rest);
  return { body: pageOk(page, pageSize, items) };
});

route('POST', '/api/orp/certificates', ({ body, ip, operator }) => {
  const name = String(body?.name ?? '').trim();
  const domains = String(body?.domains ?? '').trim();
  const notBefore = String(body?.notBefore ?? '').trim();
  const notAfter = String(body?.notAfter ?? '').trim();
  const certificate = String(body?.certificate ?? '').trim();
  const privateKey = String(body?.privateKey ?? '').trim();
  const certificateChain = String(body?.certificateChain ?? '').trim();
  const scopeRaw = body?.centerScope;

  if (!name || !domains || !notBefore || !notAfter) {
    return { status: 400, body: fail('证书名称、关联域名与有效期范围为必填项') };
  }
  const pemError = validateCertificatePem(certificate, privateKey);
  if (pemError) {
    return { status: 400, body: fail(pemError) };
  }
  if (certificateChain && !certificateChain.includes('BEGIN CERTIFICATE')) {
    return { status: 400, body: fail('证书链格式错误：必须是 PEM 格式（缺少 BEGIN CERTIFICATE 头）') };
  }
  if (Date.parse(notAfter) <= Date.parse(notBefore)) {
    return { status: 400, body: fail('有效期范围不正确：结束时间必须晚于开始时间') };
  }
  const centerScope: 'all' | number[] =
    scopeRaw === 'all' || scopeRaw === undefined || scopeRaw === null
      ? 'all'
      : Array.isArray(scopeRaw)
        ? scopeRaw.map(Number).filter((item: number) => Number.isFinite(item))
        : 'all';
  if (centerScope !== 'all' && centerScope.length === 0) {
    return { status: 400, body: fail('证书适用中心范围不能为空，或选择全部中心') };
  }

  const cert = {
    certificate,
    certificateChain: certificateChain || undefined,
    centerScope,
    createdAt: now(),
    domains,
    id: nextId(certificates),
    name,
    notAfter,
    notBefore,
    privateKey,
  };
  certificates.push(cert);
  recordAudit(operator, ip, {
    action: 'create',
    detail: `名称：${name}；域名：${domains}；有效期：${notBefore} ~ ${notAfter}`,
    module: 'tls',
    target: `tls:${name}`,
  });
  return { body: ok({ ...cert, privateKey: '******' }) };
});

route('PUT', '/api/orp/certificates/:id', ({ body, id, ip, operator }) => {
  const cert = certificates.find((item) => item.id === id);
  if (!cert) {
    return { status: 404, body: fail('证书不存在或已被删除') };
  }

  const name = String(body?.name ?? cert.name).trim();
  const domains = String(body?.domains ?? cert.domains).trim();
  const notBefore = String(body?.notBefore ?? cert.notBefore).trim();
  const notAfter = String(body?.notAfter ?? cert.notAfter).trim();
  const certificate = String(body?.certificate ?? cert.certificate).trim();
  const privateKey = String(body?.privateKey ?? cert.privateKey).trim();
  const certificateChain = String(body?.certificateChain ?? cert.certificateChain ?? '').trim();
  const scopeRaw = body?.centerScope;

  if (!name || !domains || !notBefore || !notAfter) {
    return { status: 400, body: fail('证书名称、关联域名与有效期范围为必填项') };
  }
  const pemError = validateCertificatePem(certificate, privateKey);
  if (pemError) {
    return { status: 400, body: fail(pemError) };
  }
  if (certificateChain && !certificateChain.includes('BEGIN CERTIFICATE')) {
    return { status: 400, body: fail('证书链格式错误：必须是 PEM 格式（缺少 BEGIN CERTIFICATE 头）') };
  }
  if (Date.parse(notAfter) <= Date.parse(notBefore)) {
    return { status: 400, body: fail('有效期范围不正确：结束时间必须晚于开始时间') };
  }
  const centerScope: 'all' | number[] =
    scopeRaw === 'all' || scopeRaw === undefined || scopeRaw === null
      ? cert.centerScope
      : Array.isArray(scopeRaw)
        ? scopeRaw.map(Number).filter((item: number) => Number.isFinite(item))
        : cert.centerScope;
  if (centerScope !== 'all' && centerScope.length === 0) {
    return { status: 400, body: fail('证书适用中心范围不能为空，或选择全部中心') };
  }

  const before = `名称：${cert.name}；域名：${cert.domains}；有效期：${cert.notBefore} ~ ${cert.notAfter}`;
  Object.assign(cert, {
    certificate,
    certificateChain: certificateChain || undefined,
    centerScope,
    domains,
    name,
    notAfter,
    notBefore,
    privateKey,
  });
  recordAudit(operator, ip, {
    action: 'update',
    detail: `变更前【${before}】→ 变更后【名称：${name}；域名：${domains}；有效期：${notBefore} ~ ${notAfter}】`,
    module: 'tls',
    target: `tls:${name}`,
  });
  return { body: ok({ ...cert, privateKey: '******' }) };
});

route('DELETE', '/api/orp/certificates/:id', ({ id, ip, operator }) => {
  const index = certificates.findIndex((item) => item.id === id);
  if (index === -1) {
    return { status: 404, body: fail('证书不存在或已被删除') };
  }

  const referencing = httpListeners.filter((item) => item.tlsCertId === id);
  if (referencing.length > 0) {
    return {
      status: 400,
      body: fail(
        `该证书仍被 ${referencing.length} 个 HTTP 监听域名引用（${referencing.map((item) => item.domain).join('、')}），请先解除绑定`,
      ),
    };
  }

  const [removed] = certificates.splice(index, 1);
  recordAudit(operator, ip, {
    action: 'delete',
    detail: `名称：${removed?.name}；域名：${removed?.domains}；有效期：${removed?.notBefore} ~ ${removed?.notAfter}`,
    module: 'tls',
    target: `tls:${removed?.name}`,
  });
  return { body: ok(null) };
});

route('GET', '/api/orp/certificates/:id/detail', ({ id }) => {
  const cert = certificates.find((item) => item.id === id);
  if (!cert) {
    return { status: 404, body: fail('证书不存在或已被删除') };
  }
  return {
    body: ok({
      certificate: cert.certificate,
      certificateChain: cert.certificateChain ?? '',
      privateKey: cert.privateKey,
    }),
  };
});

/* ----------------------------- DNS 解析器 ----------------------------- */

const IPV4_PATTERN = /^(\d{1,3}\.){3}\d{1,3}$/;

route('GET', '/api/orp/dns-resolvers', ({ query }) => {
  const centerId = toInt(query.centerId, Number.NaN);
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...dnsResolvers].sort((a, b) => a.id - b.id);
  if (centerId) {
    list = list.filter((item) => item.centerId === centerId);
  }
  const items = list.map((item) => ({
    ...item,
    centerName: centerName(item.centerId),
  }));
  return { body: pageOk(page, pageSize, items) };
});

route('POST', '/api/orp/dns-resolvers', ({ body, ip, operator }) => {
  const centerId = toInt(body?.centerId, Number.NaN);
  const address = String(body?.address ?? '').trim();
  const port = toInt(body?.port ?? 53, 53);
  const timeoutSec = toInt(body?.timeoutSec ?? 2, 2);
  const cacheTtlSec = toInt(body?.cacheTtlSec ?? 30, 30);

  if (!centerId || !address) {
    return { status: 400, body: fail('所属中心与解析服务器地址为必填项') };
  }
  if (!IPV4_PATTERN.test(address)) {
    return {
      status: 400,
      body: fail('解析服务器地址必须是 IPv4 格式，示例：10.60.0.11'),
    };
  }
  if (!(port >= 1 && port <= 65_535)) {
    return { status: 400, body: fail('端口必须是 1-65535 之间的整数') };
  }
  if (!(timeoutSec >= 1 && timeoutSec <= 60)) {
    return { status: 400, body: fail('超时时间必须是 1-60 之间的秒数') };
  }
  if (!(cacheTtlSec >= 1 && cacheTtlSec <= 3600)) {
    return { status: 400, body: fail('有效缓存时间必须是 1-3600 之间的秒数') };
  }
  if (!centers.some((item) => item.id === centerId)) {
    return { status: 400, body: fail('所属中心不存在，请刷新后重试') };
  }
  if (
    dnsResolvers.some(
      (item) =>
        item.centerId === centerId &&
        item.address === address &&
        item.port === port,
    )
  ) {
    return {
      status: 400,
      body: fail(`该中心下已存在解析服务器 ${address}:${port}`),
    };
  }

  const resolver = {
    address,
    cacheTtlSec,
    centerId,
    id: nextId(dnsResolvers),
    port,
    timeoutSec,
  };
  dnsResolvers.push(resolver);
  recordAudit(operator, ip, {
    action: 'create',
    detail: `地址：${address}:${port}；超时：${timeoutSec}s；缓存：${cacheTtlSec}s；中心：${centerName(centerId)}`,
    module: 'dns',
    target: `dns:${address}:${port}`,
  });
  return { body: ok(resolver) };
});

route('PUT', '/api/orp/dns-resolvers/:id', ({ body, id, ip, operator }) => {
  const resolver = dnsResolvers.find((item) => item.id === id);
  if (!resolver) {
    return { status: 404, body: fail('解析器不存在或已被删除') };
  }

  const centerId = toInt(body?.centerId ?? resolver.centerId, Number.NaN);
  const address = String(body?.address ?? resolver.address).trim();
  const port = toInt(body?.port ?? resolver.port, 53);
  const timeoutSec = toInt(body?.timeoutSec ?? resolver.timeoutSec, 2);
  const cacheTtlSec = toInt(body?.cacheTtlSec ?? resolver.cacheTtlSec, 30);

  if (!centerId || !address) {
    return { status: 400, body: fail('所属中心与解析服务器地址为必填项') };
  }
  if (!IPV4_PATTERN.test(address)) {
    return {
      status: 400,
      body: fail('解析服务器地址必须是 IPv4 格式，示例：10.60.0.11'),
    };
  }
  if (!(port >= 1 && port <= 65_535)) {
    return { status: 400, body: fail('端口必须是 1-65535 之间的整数') };
  }
  if (!(timeoutSec >= 1 && timeoutSec <= 60)) {
    return { status: 400, body: fail('超时时间必须是 1-60 之间的秒数') };
  }
  if (!(cacheTtlSec >= 1 && cacheTtlSec <= 3600)) {
    return { status: 400, body: fail('有效缓存时间必须是 1-3600 之间的秒数') };
  }
  if (
    dnsResolvers.some(
      (item) =>
        item.id !== id &&
        item.centerId === centerId &&
        item.address === address &&
        item.port === port,
    )
  ) {
    return {
      status: 400,
      body: fail(`该中心下已存在解析服务器 ${address}:${port}`),
    };
  }

  const before = `地址：${resolver.address}:${resolver.port}；超时：${resolver.timeoutSec}s；缓存：${resolver.cacheTtlSec}s`;
  Object.assign(resolver, { address, cacheTtlSec, centerId, port, timeoutSec });
  recordAudit(operator, ip, {
    action: 'update',
    detail: `变更前【${before}】→ 变更后【地址：${address}:${port}；超时：${timeoutSec}s；缓存：${cacheTtlSec}s；中心：${centerName(centerId)}】`,
    module: 'dns',
    target: `dns:${address}:${port}`,
  });
  return { body: ok(resolver) };
});

route('DELETE', '/api/orp/dns-resolvers/:id', ({ id, ip, operator }) => {
  const index = dnsResolvers.findIndex((item) => item.id === id);
  if (index === -1) {
    return { status: 404, body: fail('解析器不存在或已被删除') };
  }

  const [removed] = dnsResolvers.splice(index, 1);
  recordAudit(operator, ip, {
    action: 'delete',
    detail: `地址：${removed?.address}:${removed?.port}；中心：${centerName(removed?.centerId ?? 0)}`,
    module: 'dns',
    target: `dns:${removed?.address}:${removed?.port}`,
  });
  return { body: ok(null) };
});

/* ----------------------------- 审计日志 ----------------------------- */

route('GET', '/api/orp/audit-logs', ({ query }) => {
  const module = query.module ?? '';
  const action = query.action ?? '';
  const operator = (query.operator ?? '').trim();
  const startTime = query.startTime ?? '';
  const endTime = query.endTime ?? '';
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...auditLogs];
  if (module && module !== 'all') {
    list = list.filter((item) => item.module === module);
  }
  if (action && action !== 'all') {
    list = list.filter((item) => item.action === action);
  }
  if (operator) {
    list = list.filter((item) => item.operator.includes(operator));
  }
  if (startTime) {
    list = list.filter((item) => item.createdAt >= startTime);
  }
  if (endTime) {
    list = list.filter((item) => item.createdAt <= `${endTime} 23:59:59`);
  }
  return { body: pageOk(page, pageSize, list) };
});

/* ----------------------------- Upstream 上游服务器组 ----------------------------- */

const LB_POLICIES = [
  'consistent_hash',
  'hash',
  'ip_hash',
  'least_conn',
  'round_robin',
] as const;
const HEALTH_TYPES = ['http', 'none', 'tcp'] as const;
const UPSTREAM_HOST_PATTERN = /^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$/;

/** 解析并校验节点列表（校验失败返回 { error }） */
function parseUpstreamNodes(
  raw: unknown,
): { error: string } | { nodes: OrpUpstreamNode[] } {
  if (!Array.isArray(raw) || raw.length === 0) {
    return { error: '至少配置一个后端服务器节点' };
  }
  const nodes: OrpUpstreamNode[] = [];
  for (const [index, item] of raw.entries()) {
    const node = item as Record<string, any>;
    const host = String(node?.host ?? '').trim();
    const port = Number(node?.port);
    if (!host || !UPSTREAM_HOST_PATTERN.test(host)) {
      return { error: `第 ${index + 1} 个节点地址格式不正确：${host || '空'}` };
    }
    if (!(Number.isInteger(port) && port >= 1 && port <= 65_535)) {
      return { error: `第 ${index + 1} 个节点端口必须是 1-65535 之间的整数` };
    }
    const weight = Number(node?.weight ?? 1);
    if (!(Number.isInteger(weight) && weight >= 1 && weight <= 100)) {
      return { error: `第 ${index + 1} 个节点权重必须是 1-100 之间的整数` };
    }
    const maxFails = Number(node?.maxFails ?? 3);
    if (!(Number.isInteger(maxFails) && maxFails >= 0 && maxFails <= 100)) {
      return { error: `第 ${index + 1} 个节点 max_fails 必须是 0-100 之间的整数` };
    }
    const failTimeoutSec = Number(node?.failTimeoutSec ?? 10);
    if (
      !(
        Number.isInteger(failTimeoutSec) &&
        failTimeoutSec >= 1 &&
        failTimeoutSec <= 300
      )
    ) {
      return { error: `第 ${index + 1} 个节点 fail_timeout 必须是 1-300 秒` };
    }
    const slowStartSec = Number(node?.slowStartSec ?? 0);
    if (
      !(
        Number.isInteger(slowStartSec) &&
        slowStartSec >= 0 &&
        slowStartSec <= 600
      )
    ) {
      return { error: `第 ${index + 1} 个节点慢启动必须是 0-600 秒` };
    }
    nodes.push({
      backup: Boolean(node?.backup),
      failTimeoutSec,
      host,
      maxFails,
      port,
      slowStartSec,
      weight,
    });
  }
  if (nodes.every((item) => item.backup)) {
    return { error: '不允许全部节点都标记为 backup，至少保留一个主节点' };
  }
  return { nodes };
}

/** 解析并校验健康检查配置 */
function parseHealthCheck(
  raw: unknown,
): { error: string } | { healthCheck: OrpUpstreamGroup['healthCheck'] } {
  const hc = (raw ?? {}) as Record<string, any>;
  const type = hc.type ?? 'none';
  if (!HEALTH_TYPES.includes(type)) {
    return { error: `健康检查类型必须是 ${HEALTH_TYPES.join(' / ')}` };
  }
  const intervalSec = Number(hc.intervalSec ?? 5);
  if (!(Number.isInteger(intervalSec) && intervalSec >= 1 && intervalSec <= 300)) {
    return { error: '探测间隔必须是 1-300 秒' };
  }
  const expectedStatus = Array.isArray(hc.expectedStatus)
    ? hc.expectedStatus.map((item: unknown) => Number(item)).filter(Number.isInteger)
    : [];
  if (type === 'http' && expectedStatus.length === 0) {
    return { error: 'HTTP 主动探测必须配置至少一个期望状态码' };
  }
  if (
    expectedStatus.some(
      (item: number) => !(Number.isInteger(item) && item >= 100 && item <= 599),
    )
  ) {
    return { error: '期望状态码必须是 100-599 之间的整数' };
  }
  const path = String(hc.path ?? '').trim();
  if (type === 'http' && !path.startsWith('/')) {
    return { error: 'HTTP 探测路径必须以 / 开头' };
  }
  return {
    healthCheck: { expectedStatus, intervalSec, path: type === 'http' ? path : '', type },
  };
}

function nodeSummary(nodes: OrpUpstreamNode[]): string {
  return nodes
    .map(
      (item) =>
        `${item.host}:${item.port}(${item.backup ? 'backup,' : ''}w${item.weight})`,
    )
    .join('、');
}

route('GET', '/api/orp/upstream-groups', ({ query }) => {
  const centerId = toInt(query.centerId, Number.NaN);
  const keyword = String(query.keyword ?? '').trim().toLowerCase();
  const page = toInt(query.page, 1);
  const pageSize = toInt(query.pageSize, 20);

  let list = [...upstreamGroups].sort((a, b) => a.id - b.id);
  if (centerId) {
    list = list.filter((item) => item.centerId === centerId);
  }
  if (keyword) {
    list = list.filter(
      (item) =>
        item.name.toLowerCase().includes(keyword) ||
        item.description.toLowerCase().includes(keyword) ||
        item.tags.some((tag) => tag.toLowerCase().includes(keyword)),
    );
  }
  const items = list.map((item) => ({
    ...item,
    centerName: centerName(item.centerId),
    referenced: upstreamReferences(item.name),
  }));
  return { body: pageOk(page, pageSize, items) };
});

route('POST', '/api/orp/upstream-groups', ({ body, ip, operator }) => {
  const centerId = toInt(body?.centerId, Number.NaN);
  const name = String(body?.name ?? '').trim();
  const lbPolicy = String(body?.lbPolicy ?? 'round_robin');
  const description = String(body?.description ?? '').trim();
  const tags = Array.isArray(body?.tags)
    ? body.tags.map((item: unknown) => String(item).trim()).filter(Boolean)
    : [];

  if (!centerId || !name) {
    return { status: 400, body: fail('所属中心与上游组名称为必填项') };
  }
  if (!/^[a-zA-Z][a-zA-Z0-9_-]{1,62}$/.test(name)) {
    return {
      status: 400,
      body: fail('名称必须以字母开头，2-63 位字母数字下划线连字符（nginx upstream 命名规范）'),
    };
  }
  if (!LB_POLICIES.includes(lbPolicy as (typeof LB_POLICIES)[number])) {
    return { status: 400, body: fail(`负载均衡策略必须是 ${LB_POLICIES.join(' / ')}`) };
  }
  if (!centers.some((item) => item.id === centerId)) {
    return { status: 400, body: fail('所属中心不存在，请刷新后重试') };
  }
  if (upstreamNameExists(name)) {
    return { status: 400, body: fail(`上游组名称 ${name} 已存在`) };
  }

  const nodesResult = parseUpstreamNodes(body?.nodes);
  if ('error' in nodesResult) {
    return { status: 400, body: fail(nodesResult.error) };
  }
  const hcResult = parseHealthCheck(body?.healthCheck);
  if ('error' in hcResult) {
    return { status: 400, body: fail(hcResult.error) };
  }

  const group = {
    centerId,
    createdAt: now(),
    description,
    healthCheck: hcResult.healthCheck,
    id: nextId(upstreamGroups),
    lbPolicy,
    name,
    nodes: nodesResult.nodes,
    tags,
    updatedAt: now(),
  };
  upstreamGroups.push(group);
  recordAudit(operator, ip, {
    action: 'create',
    detail: `名称：${name}；策略：${lbPolicy}；节点 ${nodesResult.nodes.length} 个【${nodeSummary(nodesResult.nodes)}】；健康检查：${hcResult.healthCheck.type}`,
    module: 'upstream',
    target: `upstream:${name}`,
  });
  return { body: ok(group) };
});

route('PUT', '/api/orp/upstream-groups/:id', ({ body, id, ip, operator }) => {
  const group = upstreamGroups.find((item) => item.id === id);
  if (!group) {
    return { status: 404, body: fail('上游组不存在或已被删除') };
  }

  const centerId = toInt(body?.centerId ?? group.centerId, Number.NaN);
  const name = String(body?.name ?? group.name).trim();
  const lbPolicy = String(body?.lbPolicy ?? group.lbPolicy);
  const description = String(body?.description ?? group.description).trim();
  const tags = Array.isArray(body?.tags)
    ? body.tags.map((item: unknown) => String(item).trim()).filter(Boolean)
    : group.tags;

  if (!centerId || !name) {
    return { status: 400, body: fail('所属中心与上游组名称为必填项') };
  }
  if (!/^[a-zA-Z][a-zA-Z0-9_-]{1,62}$/.test(name)) {
    return {
      status: 400,
      body: fail('名称必须以字母开头，2-63 位字母数字下划线连字符（nginx upstream 命名规范）'),
    };
  }
  if (!LB_POLICIES.includes(lbPolicy as (typeof LB_POLICIES)[number])) {
    return { status: 400, body: fail(`负载均衡策略必须是 ${LB_POLICIES.join(' / ')}`) };
  }
  if (!centers.some((item) => item.id === centerId)) {
    return { status: 400, body: fail('所属中心不存在，请刷新后重试') };
  }
  // 重命名时检查引用方：旧名称被引用则禁止改名（需先解除引用）
  if (name !== group.name) {
    const refs = upstreamReferences(group.name);
    if (refs.length > 0) {
      return {
        status: 400,
        body: fail(`该上游组仍被引用（${refs.join('、')}），重命名前请先解除引用`),
      };
    }
  }
  if (upstreamNameExists(name, id)) {
    return { status: 400, body: fail(`上游组名称 ${name} 已存在`) };
  }

  const nodesResult = parseUpstreamNodes(body?.nodes ?? group.nodes);
  if ('error' in nodesResult) {
    return { status: 400, body: fail(nodesResult.error) };
  }
  const hcResult = parseHealthCheck(body?.healthCheck ?? group.healthCheck);
  if ('error' in hcResult) {
    return { status: 400, body: fail(hcResult.error) };
  }

  const before = `策略：${group.lbPolicy}；节点 ${group.nodes.length} 个`;
  Object.assign(group, {
    centerId,
    description,
    healthCheck: hcResult.healthCheck,
    lbPolicy,
    name,
    nodes: nodesResult.nodes,
    tags,
    updatedAt: now(),
  });
  recordAudit(operator, ip, {
    action: 'update',
    detail: `变更前【${before}】→ 变更后【策略：${lbPolicy}；节点 ${nodesResult.nodes.length} 个【${nodeSummary(nodesResult.nodes)}】；健康检查：${hcResult.healthCheck.type}】`,
    module: 'upstream',
    target: `upstream:${group.name}`,
  });
  return { body: ok(group) };
});

route('DELETE', '/api/orp/upstream-groups/:id', ({ id, ip, operator }) => {
  const index = upstreamGroups.findIndex((item) => item.id === id);
  if (index === -1) {
    return { status: 404, body: fail('上游组不存在或已被删除') };
  }

  const [removed] = upstreamGroups.splice(index, 1);
  const refs = upstreamReferences(removed?.name ?? '');
  if (refs.length > 0) {
    // 回滚删除并拦截
    upstreamGroups.splice(index, 0, removed!);
    return {
      status: 400,
      body: fail(
        `该上游组仍被 ${refs.length} 处引用（${refs.join('、')}），请先解除引用后再删除`,
      ),
    };
  }

  recordAudit(operator, ip, {
    action: 'delete',
    detail: `名称：${removed?.name}；策略：${removed?.lbPolicy}；节点 ${removed?.nodes.length ?? 0} 个【${nodeSummary(removed?.nodes ?? [])}】`,
    module: 'upstream',
    target: `upstream:${removed?.name}`,
  });
  return { body: ok(null) };
});

/* ----------------------------- 控制面大盘（运维统计报表） ----------------------------- */

/** mulberry32 确定性伪随机：同参数稳定输出，刷新不跳变 */
function seededRng(seed: number): () => number {
  let s = seed >>> 0;
  return () => {
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const DASH_RANGES = ['24h', '7d', 'today'] as const;
type DashRange = (typeof DASH_RANGES)[number];

const DASH_RANGE_SCALE: Record<DashRange, number> = {
  '24h': 1.06,
  '7d': 7.2,
  today: 1,
};

/** 大盘时间范围时间点数 */
function dashPointCount(range: DashRange): number {
  return range === '7d' ? 28 : 24;
}

/** 大盘时间轴标签 */
function dashTimeLabels(range: DashRange): string[] {
  const now = new Date();
  const labels: string[] = [];
  const count = dashPointCount(range);
  if (range === 'today') {
    for (let i = 0; i < count; i++) {
      labels.push(`${String(i).padStart(2, '0')}:00`);
    }
  } else if (range === '24h') {
    for (let i = count - 1; i >= 0; i--) {
      const d = new Date(now.getTime() - i * 3600_000);
      labels.push(`${String(d.getHours()).padStart(2, '0')}:00`);
    }
  } else {
    for (let i = count - 1; i >= 0; i--) {
      const d = new Date(now.getTime() - i * 6 * 3600_000);
      labels.push(
        `${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')} ${String(d.getHours()).padStart(2, '0')}时`,
      );
    }
  }
  return labels;
}

/** 大盘口径下按中心过滤的三类资源 */
function dashScope(centerId: number) {
  const node_list = centerId
    ? nodes.filter((item) => item.centerId === centerId)
    : nodes;
  const groups = centerId
    ? upstreamGroups.filter((item) => item.centerId === centerId)
    : upstreamGroups;
  const listeners = centerId
    ? httpListeners.filter((item) => item.centerId === centerId)
    : httpListeners;
  return { groups, listeners, node_list };
}

/** 基础流量规模（由节点数与域名数派生，范围缩放） */
function dashBaseVolume(centerId: number, range: DashRange): number {
  const { listeners, node_list } = dashScope(centerId);
  const scale = DASH_RANGE_SCALE[range];
  const domainWeight = listeners.reduce(
    (sum, item) => sum + (item.domain.includes('api') ? 3 : 1),
    0,
  );
  return Math.round((node_list.length * 128_000 + domainWeight * 96_000) * scale);
}

/** Upstream 组健康态（确定性） */
function dashUpstreamStatus(
  group: (typeof upstreamGroups)[number],
): {
  healthyCount: number;
  onlineRate: number;
  status: 'degraded' | 'down' | 'healthy';
} {
  const rng = seededRng(group.id * 97 + group.centerId * 31 + 7);
  let downCount = 0;
  for (const node of group.nodes) {
    if (node.backup) continue; // 备节点不计主健康口径
    if (rng() < 0.14) downCount += 1;
  }
  const primaries = group.nodes.filter((item) => !item.backup).length || 1;
  const healthyCount = Math.max(0, primaries - downCount);
  const onlineRate = Math.round((healthyCount / primaries) * 100);
  const status: 'degraded' | 'down' | 'healthy' =
    healthyCount === 0 ? 'down' : downCount > 0 ? 'degraded' : 'healthy';
  return { healthyCount, onlineRate, status };
}

function parseDashRange(value: unknown): DashRange {
  const range = String(value ?? 'today');
  return (DASH_RANGES as readonly string[]).includes(range)
    ? (range as DashRange)
    : 'today';
}

route('GET', '/api/orp/dashboard/metrics', ({ query }) => {
  const centerId = toInt(query.centerId, 0);
  const range = parseDashRange(query.range);
  const base = dashBaseVolume(centerId, range);
  const rng = seededRng(centerId * 131 + dashPointCount(range) * 17 + 3);

  const errorRate = Number((0.12 + rng() * 0.55).toFixed(2));
  const availability = Number((99.72 + rng() * 0.27).toFixed(2));
  const summary = {
    activeConns: Math.round(base / 860 * (9 + rng() * 4)),
    availability,
    bandwidthInMbps: Math.round(base / 1000 * (1.4 + rng() * 0.5)),
    bandwidthOutMbps: Math.round(base / 1000 * (5.2 + rng() * 1.8)),
    errorRate,
    qpsAvg: Math.round(base / 86_400 * (0.9 + rng() * 0.2)),
    qpsPeak: Math.round(base / 86_400 * (2.1 + rng() * 0.6)),
    totalRequests: base,
  };

  const latency = {
    buckets: [
      { count: Math.round(base * 0.548), label: '<10ms' },
      { count: Math.round(base * 0.301), label: '10-50ms' },
      { count: Math.round(base * 0.121), label: '50-200ms' },
      { count: Math.round(base * 0.03), label: '>200ms' },
    ],
    p50Ms: Number((14 + rng() * 8).toFixed(1)),
    p95Ms: Number((88 + rng() * 40).toFixed(1)),
    p99Ms: Number((196 + rng() * 90).toFixed(1)),
  };

  const c2xx = Math.round(base * 0.918);
  const c3xx = Math.round(base * 0.051);
  const c4xx = Math.round(base * 0.024);
  const statusCodes = {
    c2xx,
    c3xx,
    c4xx,
    c5xx: Math.max(0, base - c2xx - c3xx - c4xx),
  };

  const { groups, node_list } = dashScope(centerId);
  const upstreamHealth = {
    down: 0,
    degraded: 0,
    groups: groups.map((group) => {
      const health = dashUpstreamStatus(group);
      return {
        centerName: centerName(group.centerId),
        healthyCount: health.healthyCount,
        lbPolicy: group.lbPolicy,
        name: group.name,
        onlineRate: health.onlineRate,
        status: health.status,
        total: group.nodes.filter((item) => !item.backup).length,
      };
    }),
    healthy: 0,
  };
  for (const g of upstreamHealth.groups) {
    upstreamHealth[g.status] += 1;
  }

  const nodeLoad = node_list.map((node) => {
    const rngNode = seededRng(node.id * 53 + 11);
    return {
      centerName: centerName(node.centerId),
      conns: Math.round(1200 + rngNode() * 5200),
      cpuPercent: Math.round(18 + rngNode() * 62),
      memPercent: Math.round(32 + rngNode() * 44),
      name: node.name,
      status: node.status,
    };
  });

  return {
    body: ok({
      latency,
      nodeLoad,
      statusCodes,
      summary,
      upstreamHealth,
    }),
  };
});

route('GET', '/api/orp/dashboard/trends', ({ query }) => {
  const centerId = toInt(query.centerId, 0);
  const range = parseDashRange(query.range);
  const count = dashPointCount(range);
  const labels = dashTimeLabels(range);
  const rng = seededRng(centerId * 71 + count * 13 + 5);

  const points = labels.map((label, index) => {
    // 日周期正弦：白天高夜里低
    const hour = range === 'today' ? index : new Date().getHours() - (count - 1 - index);
    const phase = ((hour % 24) + 24) % 24;
    const cycle = 0.55 + 0.45 * Math.sin(((phase - 5) / 24) * Math.PI * 2);
    const noise = 0.92 + rng() * 0.16;
    const baseQps = dashBaseVolume(centerId, range) / 86_400;
    const qps = Math.max(80, Math.round(baseQps * cycle * noise));
    return {
      avgLatencyMs: Number((12 + (1 - cycle) * 46 + rng() * 9).toFixed(1)),
      inMbps: Math.round(qps * (0.052 + rng() * 0.012)),
      outMbps: Math.round(qps * (0.19 + rng() * 0.05)),
      qps,
      time: label,
    };
  });

  return { body: ok({ points }) };
});

route('GET', '/api/orp/dashboard/top-rankings', ({ query }) => {
  const centerId = toInt(query.centerId, 0);
  const range = parseDashRange(query.range);
  const { listeners } = dashScope(centerId);
  const base = dashBaseVolume(centerId, range);

  const domainStats = listeners.map((listener) => {
    const rng = seededRng(listener.id * 29 + 3);
    const requests = Math.round(base * (0.08 + rng() * 0.28));
    return {
      avgLatencyMs: Number((18 + rng() * 64).toFixed(1)),
      domain: listener.domain,
      requests,
    };
  });
  const domainTotal = domainStats.reduce((sum, item) => sum + item.requests, 0) || 1;
  const domains = domainStats
    .map((item) => ({
      ...item,
      percent: Number(((item.requests / domainTotal) * 100).toFixed(1)),
    }))
    .sort((a, b) => b.requests - a.requests)
    .slice(0, 8);

  const routeStats: {
    avgLatencyMs: number;
    domain: string;
    path: string;
    requests: number;
  }[] = [];
  for (const listener of listeners) {
    for (const rule of listener.routes) {
      const rng = seededRng(rule.id * 41 + 7);
      routeStats.push({
        avgLatencyMs: Number((14 + rng() * 82).toFixed(1)),
        domain: listener.domain,
        path: rule.path,
        requests: Math.round(base * (0.012 + rng() * 0.085)),
      });
    }
  }
  const routeTotal = routeStats.reduce((sum, item) => sum + item.requests, 0) || 1;
  const routes = routeStats
    .map((item) => ({
      ...item,
      percent: Number(((item.requests / routeTotal) * 100).toFixed(1)),
    }))
    .sort((a, b) => b.requests - a.requests)
    .slice(0, 10);

  return { body: ok({ domains, routes }) };
});

/* ----------------------------- 统一分发（纯函数） ----------------------------- */

/**
 * 纯函数 API 分发器：给定请求要素，返回响应体与状态码。
 * 供两类宿主共用同一份 Mock 逻辑（单一数据源）：
 *   1. vite dev 中间件（orpMockPlugin / configureServer）
 *   2. Service Worker（public/sw.js，沙盒预览 /api/** 浏览器端接管）
 */
export interface DispatchInput {
  body: Record<string, any>;
  headers: Record<string, string>;
  method: string;
  path: string;
  query: Record<string, string>;
}

export interface DispatchOutput {
  body: any;
  status: number;
}

export function dispatchApi(input: DispatchInput): DispatchOutput {
  const method = input.method.toUpperCase();
  const def = routes.find(
    (item) =>
      item.method === method &&
      (item.pattern ? item.pattern.test(input.path) : item.path === input.path),
  );
  if (!def) {
    return {
      status: 404,
      body: fail('接口不存在', `NOT FOUND ${method} ${input.path}`),
    };
  }

  let id = 0;
  if (def.pattern) {
    id = toInt(def.pattern.exec(input.path)?.[1], Number.NaN);
  }
  const user = userFromHeaders(input.headers);
  const ctx: Ctx = {
    body: input.body,
    id,
    ip: ipFromHeaders(input.headers),
    operator: user?.realName || user?.username || '系统管理员',
    query: input.query,
    user,
  };
  try {
    const out = def.handler(ctx);
    // handler 两种返回约定：
    //   1. 包装形态：{ status?, body } —— 带 status 的错误/分页等
    //   2. 直接形态：ok(...) 数据对象本身（如 auth/login 成功、user/info）
    const isWrapped =
      out !== null &&
      typeof out === 'object' &&
      ('body' in out || 'status' in out);
    return isWrapped
      ? { body: out.body, status: out.status ?? 200 }
      : { body: out, status: 200 };
  } catch (error) {
    return {
      status: 500,
      body: fail(error instanceof Error ? error.message : '服务器内部错误'),
    };
  }
}

/* ----------------------------- 插件导出 ----------------------------- */

/**
 * vite 插件：在 dev server 进程内承接 /api/** 全部 Mock 请求。
 * 中间件在 vite 内置中间件之前注册，且 web-antd 的 vite.config
 * 已移除指向本地 Nitro(5320) 的 /api 代理，两者不冲突。
 */
export function orpMockPlugin(): Plugin {
  return {
    name: 'orp-mock-server',
    configureServer(server) {
      server.middlewares.use(
        async (req: IncomingMessage, res: ServerResponse, next) => {
          const url = new URL(req.url ?? '/', 'http://localhost');
          const path = url.pathname;
          if (!path.startsWith('/api/')) {
            next();
            return;
          }

          const headers: Record<string, string> = {};
          for (const [key, value] of Object.entries(req.headers)) {
            headers[key] = Array.isArray(value)
              ? value.join(',')
              : String(value ?? '');
          }
          const body = await readBody(req);
          const out = dispatchApi({
            body,
            headers,
            method: req.method ?? 'GET',
            path,
            query: Object.fromEntries(url.searchParams),
          });
          sendJson(res, out.status, out.body);
        },
      );
    },
  };
}
