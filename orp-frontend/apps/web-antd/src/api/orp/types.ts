/** ORP 资源类型定义（与 backend-mock / Go 控制面契约对齐） */

/** 中心（Center） */
export interface Center {
  code: string;
  createdAt: string;
  description: string;
  id: number;
  latitude?: number | null;
  longitude?: number | null;
  name: string;
  updatedAt: string;
}

export interface CenterPayload {
  code: string;
  description: string;
  latitude?: number | null;
  longitude?: number | null;
  name: string;
}

/** 节点实例（Node Instance） */
/** 节点运行状态：运行中 / 已暂停 / 已停止 / 维护中 */
export type NodeStatus = 'maintenance' | 'paused' | 'running' | 'stopped';

/** 节点定时探活健康状态：在线 / 不稳定 / 离线 */
export type NodeHealthStatus = 'degraded' | 'offline' | 'online' | 'unknown';

/** 单次探活采样记录 */
export interface LatencySample {
  /** 本轮探活是否成功（超时/不可达为 false） */
  ok: boolean;
  /** 往返延迟（ms），探活失败时为 null */
  rtt: null | number;
  /** 采样时间（yyyy-MM-dd HH:mm:ss） */
  ts: string;
}

/** 节点延迟响应趋势（近 N 次探活采样 + 统计摘要） */
export interface LatencyHistory {
  centerName: string;
  healthStatus: NodeHealthStatus;
  /** 采样间隔（秒） */
  intervalSec: number;
  nodeId: number;
  nodeName: string;
  samples: LatencySample[];
  summary: {
    avgMs: null | number;
    /** 丢包率（%，超时采样占比） */
    lossRate: number;
    maxMs: null | number;
    minMs: null | number;
    p95Ms: null | number;
    sampleCount: number;
    /** 超时判定阈值（ms） */
    timeoutMs: number;
  };
}

/** nginx 指令键值对（main / http 上下文通用） */
export interface OrpDirective {
  name: string;
  value: string;
}

/** IP 访问控制策略（server/location 块级 allow/deny 指令组） */
export interface OrpIpPolicy {
  /** 是否启用（关闭时不生成 allow/deny 指令） */
  enabled: boolean;
  /** 白名单（allow）IP 或 CIDR 网段列表 */
  allowList: string[];
  /** 黑名单（deny）IP 或 CIDR 网段列表 */
  denyList: string[];
  /** 优先模式：白名单优先（allow 组在前）或黑名单优先（deny 组在前） */
  priority: 'allow-first' | 'deny-first';
}

/** IP 组（常用 IP / CIDR 成员集合，供三层级 IP 策略快捷导入复用） */
export interface OrpIpGroup {
  id: number;
  name: string;
  description: string;
  /** 成员 IP 或 CIDR 网段列表 */
  members: string[];
  createdAt: string;
  updatedAt: string;
}

export interface OrpIpGroupPayload {
  name: string;
  description: string;
  members: string[];
}

export interface NodeItem {
  activeVersion: string;
  centerId: number;
  centerName: string;
  controlEndpoint: string;
  createdAt: string;
  directives?: OrpDirective[];
  /** 健康检查采样间隔（秒） */
  healthCheckIntervalSec?: number;
  /** 健康状态判定规则说明（列表 Tooltip 展示） */
  healthRule?: string;
  /** 定时探活健康状态：在线 / 不稳定 / 离线 */
  healthStatus?: NodeHealthStatus;
  host: string;
  id: number;
  /** 最近一次探活采样时间 */
  lastCheckedAt?: null | string;
  /** 最近一次成功采样的往返延迟（ms），超时不可达时为 null */
  lastLatencyMs?: null | number;
  name: string;
  osInfo: string;
  status: NodeStatus;
  weight: number;
}

export interface NodePayload {
  activeVersion?: string;
  centerId: number;
  weight?: number;
  controlEndpoint?: string;
  directives?: OrpDirective[];
  host: string;
  name: string;
  osInfo?: string;
  status?: NodeStatus;
}

export interface NodeMetrics {
  bandwidthInMbps: number;
  bandwidthOutMbps: number;
  connections: number;
  cpuPercent: number;
  errorRatePercent: number;
  memPercent: number;
  qps: number;
  requestTotal24h: number;
  uptimeDays: number;
}

/** HTTP 监听（域名 + 端口 + Location 路由） */

/** Location 三类互斥路由类型：proxy 代理 / 静态资源 / 直接返回 */
export type LocationType = 'direct' | 'proxy' | 'static';

/** Location 匹配修饰符：精确 = / 前缀 / ^~ / 区分大小写 ~ / 不区分大小写 ~* */
export type LocationMatchType = '=' | '^~' | 'prefix' | '~' | '~*';

export interface LocationRule {
  /** 直接返回类：响应体（文本或跳转 URL） */
  returnBody?: string;
  /** 直接返回类：响应状态码 */
  returnCode?: number;
  /** Location 块内部指令 KV 列表 */
  directives?: OrpDirective[];
  id: number;
  /** 匹配修饰符 */
  matchType?: LocationMatchType;
  /** 匹配路径，如 /api */
  path: string;
  /** 代理类：转发超时（毫秒） */
  proxyTimeoutMs?: number;
  /** 静态资源类：目录索引文件，如 index.html index.htm */
  indexFiles?: string;
  /** 静态资源类：是否开启 autoindex 目录浏览 */
  autoindex?: boolean;
  /** 静态资源类：root 目录 / alias 别名路径 */
  rootPath?: string;
  /** 静态资源类：目录指定方式 root 或 alias */
  staticMode?: 'alias' | 'root';
  /** 路由类型（三类互斥） */
  type: LocationType;
  /** 代理类：转发目标（协议 + Upstream 组名，如 http://web-frontend） */
  upstream?: string;
  /** 代理类：上游协议（表单状态字段，保存时组合进 upstream 值） */
  upstreamProtocol?: 'http' | 'https';
  /** Location 块级 IP 访问控制策略（allow/deny 指令组） */
  ipPolicy?: OrpIpPolicy;
}

export interface HttpListener {
  centerId: number;
  centerName: string;
  certName: string;
  createdAt: string;
  /** Server 块指令（server {} 上下文） */
  directives?: OrpDirective[];
  /** Server 块错误页面覆盖：状态码或 状态码|Content-Type -> 页面文本 */
  errorPages?: Record<string, string>;
  /** server 块级 IP 访问控制策略（allow/deny 指令组） */
  ipPolicy?: OrpIpPolicy;
  domain: string;
  id: number;
  port: number;
  routeCount: number;
  routes: LocationRule[];
  tlsCertId: null | number;
}

export interface HttpListenerPayload {
  centerId: number;
  directives?: OrpDirective[];
  /** Server 块错误页面覆盖；未配置的状态码继承 HTTP 块默认值 */
  errorPages?: Record<string, string>;
  /** server 块级 IP 访问控制策略 */
  ipPolicy?: OrpIpPolicy;
  domain: string;
  port: number;
  routes?: Array<
    Partial<Omit<LocationRule, 'id'>> & {
      id?: number;
      path: string;
      type: LocationType;
    }
  >;
  tlsCertId: null | number;
}

/** Stream 四层服务 */
export interface StreamService {
  backends: string[];
  centerId: number;
  centerName: string;
  createdAt: string;
  description: string;
  /** Stream Server 块指令（stream server {} 上下文） */
  directives?: OrpDirective[];
  id: number;
  listenAddress: string;
  listenPort: number;
  protocol: 'tcp' | 'udp';
  /** stream server 块级 IP 访问控制策略（allow/deny 指令组） */
  ipPolicy?: OrpIpPolicy;
}

export interface StreamServicePayload {
  backends: string[];
  centerId: number;
  description: string;
  directives?: OrpDirective[];
  /** stream server 块级 IP 访问控制策略 */
  ipPolicy?: OrpIpPolicy;
  listenAddress: string;
  listenPort: number;
  protocol: 'tcp' | 'udp';
}

/** log_format 日志格式配置（http 块 / stream 块各自独立维护） */
export interface LogFormatConfig {
  /** 格式串（支持 $ 变量引用） */
  format: string;
  /** 格式名称，如 main / tcp_main */
  name: string;
}

/** 实时日志行 */
export interface LogLine {
  /** 单调递增行号 */
  id: number;
  /** 日志级别 */
  level: 'DEBUG' | 'ERROR' | 'INFO' | 'WARN';
  /** 日志正文 */
  line: string;
  /** 时间戳 */
  ts: string;
}

/** 实时日志目标类型：节点实例 / HTTP Server / Stream Server */
export type LogTargetType = 'http' | 'node' | 'stream';

/** TLS 证书 */
export interface Certificate {
  /** 证书链（PEM，可选） */
  certificateChain?: string;
  centerScope: 'all' | number[];
  createdAt: string;
  domains: string;
  id: number;
  name: string;
  notAfter: string;
  notBefore: string;
}

export interface CertificatePayload {
  certificate: string;
  /** 证书链（PEM，可选录入） */
  certificateChain?: string;
  centerScope: 'all' | number[];
  domains: string;
  name: string;
  notAfter: string;
  notBefore: string;
  privateKey: string;
}

/** DNS 解析器 */
export interface DnsResolver {
  address: string;
  cacheTtlSec: number;
  centerId: number;
  centerName: string;
  id: number;
  port: number;
  timeoutSec: number;
}

export interface DnsResolverPayload {
  address: string;
  cacheTtlSec: number;
  centerId: number;
  port: number;
  timeoutSec: number;
}

/** Upstream 上游服务器组 */
export type UpstreamLbPolicy =
  | 'consistent_hash'
  | 'hash'
  | 'ip_hash'
  | 'least_conn'
  | 'round_robin';

export type UpstreamHealthCheckType = 'http' | 'none' | 'tcp';

export interface UpstreamNode {
  backup: boolean;
  failTimeoutSec: number;
  host: string;
  maxFails: number;
  port: number;
  slowStartSec: number;
  weight: number;
}

export interface UpstreamHealthCheck {
  expectedStatus: number[];
  intervalSec: number;
  path: string;
  type: UpstreamHealthCheckType;
}

export interface UpstreamGroup {
  centerId: number;
  centerName: string;
  createdAt: string;
  description: string;
  /** upstream {} 块级指令列表 */
  directives?: OrpDirective[];
  healthCheck: UpstreamHealthCheck;
  hashKey?: string;
  id: number;
  lbPolicy: UpstreamLbPolicy;
  name: string;
  nodes: UpstreamNode[];
  referenced: string[];
  tags: string[];
  updatedAt: string;
}

export interface UpstreamGroupPayload {
  centerId: number;
  description: string;
  /** upstream {} 块级指令列表 */
  directives?: OrpDirective[];
  healthCheck: UpstreamHealthCheck;
  hashKey?: string;
  lbPolicy: UpstreamLbPolicy;
  name: string;
  nodes: UpstreamNode[];
  tags: string[];
}

/** 控制面大盘（运维统计报表） */
export type DashboardRange = '1h' | '24h' | '30m' | '6h' | '7d' | 'today';

export interface GeoIPDatabaseStatus {
  buildTime?: string;
  databaseType?: string;
  enabled: boolean;
  fileName?: string;
  importedAt?: string;
  importedBy?: string;
  message?: string;
  sha256?: string;
  sizeBytes?: number;
  source: 'environment' | 'managed' | 'none';
  state: 'disabled' | 'error' | 'ready';
}

/** 状态码细分明细（如 200/301/404/502 的计数） */
export interface DashboardStatusCodeDetail {
  code: number;
  count: number;
}

export interface DashboardSummary {
  activeConns: number;
  availability: null | number;
  bandwidthInMbps: number;
  bandwidthOutMbps: number;
  errorRate: number;
  qpsAvg: number;
  qpsPeak: number;
  totalRequests: number;
}

export interface DashboardLatency {
  buckets: { count: number; label: string }[];
  p50Ms: number;
  p95Ms: number;
  p99Ms: number;
}

export interface DashboardStatusCodes {
  c2xx: number;
  c3xx: number;
  c4xx: number;
  c5xx: number;
  /** 细分状态码明细（悬停下钻展示） */
  details?: DashboardStatusCodeDetail[];
}

export interface DashboardUpstreamGroupHealth {
  centerName: string;
  healthyCount: number;
  lbPolicy: string;
  name: string;
  onlineRate: number;
  status: 'degraded' | 'down' | 'healthy';
  total: number;
}

export interface DashboardUpstreamHealth {
  degraded: number;
  down: number;
  groups: DashboardUpstreamGroupHealth[];
  healthy: number;
}

export interface DashboardNodeLoad {
  centerName: string;
  conns: number;
  cpuPercent: number;
  memPercent: number;
  name: string;
  status: string;
}

export interface DashboardMetrics {
  geography: {
    centers: { coord: [number, number]; id: number; name: string }[];
    flows: { center: string; coords: [[number, number], [number, number]]; requests: number; source: string }[];
    points: { coord: [number, number]; countryCode: string; name: string; requests: number }[];
    reason?: string;
    sampledEvents?: number;
    sampleLimit: number;
    state: 'disabled' | 'error' | 'ready';
    unknownRequests: number;
  };
  latency: DashboardLatency;
  nodeLoad: DashboardNodeLoad[];
  statusCodes: DashboardStatusCodes;
  summary: DashboardSummary;
  upstreamHealth: DashboardUpstreamHealth;
}

export interface DashboardTrendPoint {
  avgLatencyMs: number;
  inMbps: number;
  outMbps: number;
  qps: number;
  time: string;
}

export interface DashboardTrends {
  points: DashboardTrendPoint[];
}

export interface DashboardTopDomain {
  avgLatencyMs: number;
  domain: string;
  percent: number;
  requests: number;
}

export interface DashboardTopRoute {
  avgLatencyMs: number;
  domain: string;
  path: string;
  percent: number;
  requests: number;
}

export interface DashboardTopRankings {
  domains: DashboardTopDomain[];
  routes: DashboardTopRoute[];
}

/** 审计日志 */
export type AuditAction = 'create' | 'delete' | 'offline' | 'update';
export type AuditModule =
  | 'center'
  | 'dns'
  | 'http'
  | 'node'
  | 'setting'
  | 'stream'
  | 'tls'
  | 'upstream';

export interface AuditLog {
  action: AuditAction;
  createdAt: string;
  detail: string;
  id: number;
  ip: string;
  module: AuditModule;
  operator: string;
  target: string;
}

/** 系统配置参数类型 */
export type SettingType = 'boolean' | 'json' | 'number' | 'string';

/** 系统配置启用状态 */
export type SettingStatus = 'disabled' | 'enabled';

/** 系统配置项 */
export interface OrpSetting {
  createdAt: string;
  /** 配置键名（全局唯一） */
  key: string;
  /** 配置名称 */
  name: string;
  /** 所属分组 */
  group: string;
  /** 参数类型 */
  type: SettingType;
  /** 配置键值（列表返回脱敏值） */
  value: string;
  /** 是否敏感信息 */
  isSensitive: boolean;
  status: SettingStatus;
  description: string;
  updatedAt: string;
  id: number;
}

/** 系统配置新建/编辑载荷 */
export interface SettingPayload {
  key: string;
  name: string;
  group: string;
  type: SettingType;
  value: string;
  isSensitive?: boolean;
  status?: SettingStatus;
  description?: string;
}

/** 系统配置分组实体 */
export interface OrpSettingGroup {
  createdAt: string;
  /** 分组编码（全局唯一，系统分组锁定不可改） */
  code: string;
  description: string;
  id: number;
  /** 是否系统预置分组（禁止删除与重命名） */
  isSystem: boolean;
  /** 分组名称（配置项归属关联键，重命名时级联更新组内配置项） */
  name: string;
  /** 排序序号（升序展示，越小越靠前） */
  sortOrder: number;
  /** 组内配置项数量 */
  settingCount: number;
  updatedAt: string;
}

/** 分组新建/编辑载荷 */
export interface SettingGroupPayload {
  /** 分组编码（仅新建时填写，创建后锁定） */
  code?: string;
  description?: string;
  name: string;
  sortOrder?: number;
}

/** 通用分页查询参数 */
export interface PageParams {
  page?: number;
  pageSize?: number;
}

/** 通用分页响应（与 vxe-table proxyConfig 对齐） */
export interface PageResult<T> {
  items: T[];
  total: number;
}
