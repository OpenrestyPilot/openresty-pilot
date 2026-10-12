/**
 * ORP 资源 Mock 存储
 *
 * 内存态种子数据 + 增删改查助手，供 backend-mock 的 /api/orp/** 路由使用。
 * 所有写操作自动生成审计记录（审计留痕规则）。
 */
export interface OrpCenter {
  code: string;
  createdAt: string;
  description: string;
  id: number;
  name: string;
  updatedAt: string;
}

export type OrpNodeStatus = 'maintenance' | 'offline' | 'online';

export interface OrpNode {
  activeVersion: string;
  centerId: number;
  controlEndpoint: string;
  createdAt: string;
  host: string;
  id: number;
  name: string;
  osInfo: string;
  status: OrpNodeStatus;
}

export interface OrpLocationRule {
  id: number;
  path: string;
  proxyTimeoutMs: number;
  upstream: string;
}

export interface OrpHttpListener {
  centerId: number;
  createdAt: string;
  domain: string;
  id: number;
  port: number;
  routes: OrpLocationRule[];
  tlsCertId: null | number;
}

export interface OrpStreamService {
  backends: string[];
  centerId: number;
  createdAt: string;
  description: string;
  id: number;
  listenAddress: string;
  listenPort: number;
  protocol: 'tcp' | 'udp';
}

export interface OrpCertificate {
  certificate: string;
  certificateChain?: string;
  certificateChain?: string;
  certificateChain?: string;
  certificateChain?: string;
  certificateChain?: string;
  certificateChain?: string;
  centerScope: 'all' | number[];
  createdAt: string;
  domains: string;
  id: number;
  name: string;
  notAfter: string;
  notBefore: string;
  privateKey: string;
}

export interface OrpDnsResolver {
  address: string;
  cacheTtlSec: number;
  centerId: number;
  id: number;
  port: number;
  timeoutSec: number;
}

export interface OrpAuditLog {
  action: 'create' | 'delete' | 'offline' | 'update';
  createdAt: string;
  detail: string;
  id: number;
  ip: string;
  module: 'center' | 'dns' | 'http' | 'node' | 'stream' | 'tls' | 'upstream';
  operator: string;
  target: string;
}

/* ------------------------------------------------------------------ */
/* 种子数据                                                            */
/* ------------------------------------------------------------------ */

export const centers: OrpCenter[] = [
  {
    code: 'cn-east-1',
    createdAt: '2026-03-12 10:20:00',
    description: '华东生产中心，承载核心 API 网关与静态资源分发',
    id: 1,
    name: '华东生产中心',
    updatedAt: '2026-09-20 14:02:11',
  },
  {
    code: 'cn-north-2',
    createdAt: '2026-04-02 09:15:00',
    description: '华北金融中心，交易链路四层代理',
    id: 2,
    name: '华北金融中心',
    updatedAt: '2026-09-18 16:44:30',
  },
  {
    code: 'cn-south-1',
    createdAt: '2026-05-21 15:40:00',
    description: '华南算力中心，模型推理入口',
    id: 3,
    name: '华南算力中心',
    updatedAt: '2026-09-25 11:18:05',
  },
  {
    code: 'cn-west-1',
    createdAt: '2026-07-08 13:26:00',
    description: '西南容灾中心，冷备流量入口',
    id: 4,
    name: '西南容灾中心',
    updatedAt: '2026-09-12 09:51:44',
  },
];

export const nodes: OrpNode[] = [
  {
    activeVersion: 'v2.4.1',
    centerId: 1,
    controlEndpoint: 'http://10.60.1.11:8081/control',
    createdAt: '2026-03-12 11:00:00',
    host: '10.60.1.11',
    id: 1,
    name: 'or-sh-ngx-01',
    osInfo: 'Ubuntu 22.04 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.4.1',
    centerId: 1,
    controlEndpoint: 'http://10.60.1.12:8081/control',
    createdAt: '2026-03-12 11:05:00',
    host: '10.60.1.12',
    id: 2,
    name: 'or-sh-ngx-02',
    osInfo: 'Ubuntu 22.04 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.3.9',
    centerId: 1,
    controlEndpoint: 'http://10.60.1.13:8081/control',
    createdAt: '2026-03-15 09:30:00',
    host: '10.60.1.13',
    id: 3,
    name: 'or-sh-ngx-03',
    osInfo: 'Ubuntu 22.04 / OpenResty 1.25.3',
    status: 'maintenance',
  },
  {
    activeVersion: 'v2.4.1',
    centerId: 1,
    controlEndpoint: 'http://10.60.1.14:8081/control',
    createdAt: '2026-04-01 14:12:00',
    host: '10.60.1.14',
    id: 4,
    name: 'or-sh-ngx-04',
    osInfo: 'Ubuntu 22.04 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.4.0',
    centerId: 2,
    controlEndpoint: 'http://10.61.2.21:8081/control',
    createdAt: '2026-04-02 10:00:00',
    host: '10.61.2.21',
    id: 5,
    name: 'or-bj-ngx-01',
    osInfo: 'CentOS 8 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.4.0',
    centerId: 2,
    controlEndpoint: 'http://10.61.2.22:8081/control',
    createdAt: '2026-04-02 10:06:00',
    host: '10.61.2.22',
    id: 6,
    name: 'or-bj-ngx-02',
    osInfo: 'CentOS 8 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.3.9',
    centerId: 2,
    controlEndpoint: 'http://10.61.2.23:8081/control',
    createdAt: '2026-05-10 16:20:00',
    host: '10.61.2.23',
    id: 7,
    name: 'or-bj-ngx-03',
    osInfo: 'CentOS 8 / OpenResty 1.25.3',
    status: 'offline',
  },
  {
    activeVersion: 'v2.4.1',
    centerId: 3,
    controlEndpoint: 'http://10.62.3.31:8081/control',
    createdAt: '2026-05-21 16:00:00',
    host: '10.62.3.31',
    id: 8,
    name: 'or-gz-ngx-01',
    osInfo: 'Debian 12 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.4.1',
    centerId: 3,
    controlEndpoint: 'http://10.62.3.32:8081/control',
    createdAt: '2026-05-21 16:05:00',
    host: '10.62.3.32',
    id: 9,
    name: 'or-gz-ngx-02',
    osInfo: 'Debian 12 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.4.0',
    centerId: 3,
    controlEndpoint: 'http://10.62.3.33:8081/control',
    createdAt: '2026-06-18 11:40:00',
    host: '10.62.3.33',
    id: 10,
    name: 'or-gz-ngx-03',
    osInfo: 'Debian 12 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.3.7',
    centerId: 4,
    controlEndpoint: 'http://10.63.4.41:8081/control',
    createdAt: '2026-07-08 14:00:00',
    host: '10.63.4.41',
    id: 11,
    name: 'or-cd-ngx-01',
    osInfo: 'Ubuntu 20.04 / OpenResty 1.25.3',
    status: 'online',
  },
  {
    activeVersion: 'v2.3.7',
    centerId: 4,
    controlEndpoint: 'http://10.63.4.42:8081/control',
    createdAt: '2026-07-08 14:06:00',
    host: '10.63.4.42',
    id: 12,
    name: 'or-cd-ngx-02',
    osInfo: 'Ubuntu 20.04 / OpenResty 1.25.3',
    status: 'offline',
  },
];

export const httpListeners: OrpHttpListener[] = [
  {
    centerId: 1,
    createdAt: '2026-03-13 09:00:00',
    domain: 'api.example.cn',
    id: 1,
    port: 8443,
    routes: [
      { id: 101, path: '/v2/*', proxyTimeoutMs: 30_000, upstream: 'http://web-frontend' },
      { id: 102, path: '/v3/*', proxyTimeoutMs: 60_000, upstream: 'http://order-grpc' },
      { id: 103, path: '/static/*', proxyTimeoutMs: 10_000, upstream: 'http://svc-static:80' },
    ],
    tlsCertId: 2,
  },
  {
    centerId: 1,
    createdAt: '2026-03-13 09:20:00',
    domain: 'console.example.cn',
    id: 2,
    port: 443,
    routes: [
      { id: 104, path: '/', proxyTimeoutMs: 30_000, upstream: 'http://svc-console:3000' },
    ],
    tlsCertId: 3,
  },
  {
    centerId: 1,
    createdAt: '2026-04-11 13:45:00',
    domain: 'cdn.example.cn',
    id: 3,
    port: 80,
    routes: [
      { id: 105, path: '/assets/*', proxyTimeoutMs: 15_000, upstream: 'http://svc-cdn-cache:8081' },
      { id: 106, path: '/media/*', proxyTimeoutMs: 15_000, upstream: 'http://svc-media:80' },
    ],
    tlsCertId: null,
  },
  {
    centerId: 2,
    createdAt: '2026-04-03 10:12:00',
    domain: 'trade.example.cn',
    id: 4,
    port: 9443,
    routes: [
      { id: 107, path: '/order/*', proxyTimeoutMs: 20_000, upstream: 'http://svc-trade-order:8080' },
      { id: 108, path: '/match/*', proxyTimeoutMs: 20_000, upstream: 'http://svc-trade-match:8080' },
    ],
    tlsCertId: 4,
  },
  {
    centerId: 2,
    createdAt: '2026-04-03 10:30:00',
    domain: 'ops.example.cn',
    id: 5,
    port: 443,
    routes: [
      { id: 109, path: '/', proxyTimeoutMs: 30_000, upstream: 'http://svc-ops-portal:8080' },
    ],
    tlsCertId: 3,
  },
  {
    centerId: 3,
    createdAt: '2026-05-22 09:40:00',
    domain: 'infer.example.cn',
    id: 6,
    port: 8443,
    routes: [
      { id: 110, path: '/llm/*', proxyTimeoutMs: 300_000, upstream: 'http://svc-infer-llm:9000' },
      { id: 111, path: '/vision/*', proxyTimeoutMs: 120_000, upstream: 'http://svc-infer-vision:9000' },
    ],
    tlsCertId: 5,
  },
  {
    centerId: 3,
    createdAt: '2026-06-19 15:22:00',
    domain: 'gpu-metrics.example.cn',
    id: 7,
    port: 9090,
    routes: [
      { id: 112, path: '/', proxyTimeoutMs: 10_000, upstream: 'http://svc-gpu-metrics:9091' },
    ],
    tlsCertId: null,
  },
  {
    centerId: 4,
    createdAt: '2026-07-09 10:05:00',
    domain: 'backup.example.cn',
    id: 8,
    port: 443,
    routes: [
      { id: 113, path: '/*', proxyTimeoutMs: 60_000, upstream: 'http://svc-backup-origin:8080' },
    ],
    tlsCertId: 3,
  },
];

export const streamServices: OrpStreamService[] = [
  {
    backends: ['session-sticky:8080', 'mysql-cluster-02:3306'],
    centerId: 1,
    createdAt: '2026-03-14 10:00:00',
    description: 'MySQL 主从四层代理',
    id: 1,
    listenAddress: '0.0.0.0',
    listenPort: 3306,
    protocol: 'tcp',
  },
  {
    backends: ['dns-resolver-01:53', 'dns-resolver-02:53'],
    centerId: 1,
    createdAt: '2026-03-14 10:12:00',
    description: '内网 DNS UDP 转发',
    id: 2,
    listenAddress: '0.0.0.0',
    listenPort: 53,
    protocol: 'udp',
  },
  {
    backends: ['redis-cluster-01:6379', 'redis-cluster-02:6379'],
    centerId: 2,
    createdAt: '2026-04-04 11:00:00',
    description: 'Redis 集群入口',
    id: 3,
    listenAddress: '0.0.0.0',
    listenPort: 6379,
    protocol: 'tcp',
  },
  {
    backends: ['kafka-broker-01:9092', 'kafka-broker-02:9092', 'kafka-broker-03:9092'],
    centerId: 2,
    createdAt: '2026-04-04 11:20:00',
    description: 'Kafka 消息队列入口',
    id: 4,
    listenAddress: '0.0.0.0',
    listenPort: 9092,
    protocol: 'tcp',
  },
  {
    backends: ['pg-cluster-01:5432', 'pg-cluster-02:5432'],
    centerId: 3,
    createdAt: '2026-05-23 14:30:00',
    description: 'PostgreSQL 向量库代理',
    id: 5,
    listenAddress: '0.0.0.0',
    listenPort: 5432,
    protocol: 'tcp',
  },
];

export const certificates: OrpCertificate[] = [
  {
    certificate: '-----BEGIN CERTIFICATE-----\n' +
     'MIIC7jCCAdagAwIBAgIUfM3cgKA+Bwwy7Mk1ASPUB252pSQwDQYJKoZIhvcNAQEL\n' +
     'BQAwFzEVMBMGA1UEAwwMKi5leGFtcGxlLmNuMB4XDTI2MDMxMTAwMDAwMFoXDTI3\n' +
     'MDMxMTIzNTk1OVowFzEVMBMGA1UEAwwMKi5leGFtcGxlLmNuMIIBIjANBgkqhkiG\n' +
     '9w0BAQEFAAOCAQ8AMIIBCgKCAQEAj6GgOklECl+8nr6eFgCJ2YUVJGozrh5zEoKF\n' +
     '9/Bun85+OPH/VdO97pFElwDr4uwm+QxsI9mnMn0QWBpoMLFmZCMyr1N8uaCvikoV\n' +
     'aB+svXdHx9qpNp+Q9N0lRct+K7mS5NSeJJ10bRJguqKoVjzsL7q+3b8Kmb6grpXU\n' +
     'hxBSv04oFlNEepZshc0wg78AlTsM+ggfIIzVe6DSaAOdT1PVNBlhEuD0/gwowMdP\n' +
     'lSV7OTgZYd01Tcrk+QPlLMzYODFyU7r+HiA4ALJeMhkBQQ+9VMlX+R/VK/kcCemX\n' +
     'Q7coAJ6urAFqeJLi1dZsedt9G5lyNl9CfSpCnfKXnuoPXlliAwIDAQABozIwMDAJ\n' +
     'BgNVHRMEAjAAMCMGA1UdEQQcMBqCDCouZXhhbXBsZS5jboIKZXhhbXBsZS5jbjAN\n' +
     'BgkqhkiG9w0BAQsFAAOCAQEAMG7O8M9TWtdSL4Q4MYOYSViFmvFQVZ95Rkhf2BBX\n' +
     'IH3xtrdIhz8GtU8d9b3bEWMPLDv98a32udPVdSe54w35dP9/pZbXtJ+dyw5HkTDJ\n' +
     'aLvf5Er2c8JKs/gqItGGIH0fsyLK83+4it9RvUZPOYzxtQbaQdevRWqDOb07iErv\n' +
     '72AeA4CUcb8FwRABR11SP1niW12a72PlP+mvbhXjHpV6nuWWw64kTnudNjr6RYcy\n' +
     '5SP30RLdrW9LPqg2DyndOuoXAbfp2VUeEfvl9oFKHugsK3c00e9X93YMbVk2cWuZ\n' +
     'KVgtIVAWomsmHzOR1pPif1KV6bD4wqmRjGtE2n/gEbKX7A==\n' +
     '-----END CERTIFICATE-----',
    certificateChain: '-----BEGIN CERTIFICATE-----\n' +
     'MIIC7jCCAdagAwIBAgIUfwahlzt8lxtoknjW2duXZbQtjfUwDQYJKoZIhvcNAQEL\n' +
     'BQAwJTEjMCEGA1UEAwwaRXhhbXBsZSBDTiBJbnRlcm1lZGlhdGUgQ0EwHhcNMjYw\n' +
     'MTAxMDAwMDAwWhcNMjkwMTAxMDAwMDAwWjAlMSMwIQYDVQQDDBpFeGFtcGxlIENO\n' +
     'IEludGVybWVkaWF0ZSBDQTCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEB\n' +
     'AJLMOerdmqWZs7qk9il9w7/pLUxaTzibJvf7maz70bnbCR2pfeZnmC4XL04qkPmH\n' +
     'fyKrgjuXQ3TiF9rbddjmpOdNiPVgefUY/QdIaNcF4i/qLN1eadJRGDIGQCFcPzf3\n' +
     'E9t6/EblZ+vOBcy3AwpiaKl5vCudNZgXrCzMn45ZG/k/4IrnGIt82yPcnHNLnB7X\n' +
     'qvW9V1pUfreSBDnAoWIbmoYRqXe3xAHMvEV0kmr+0NRVdmCRIQt9bViqNrTyWHlk\n' +
     '4Z35gZhPQmg8AALabsCxlE12UfJbzK/C2BaF4gdF/H64SMKSKg6mpumbHyEZJGut\n' +
     'QJ0pXn2lSLdmHlqwcn27kRUCAwEAAaMWMBQwEgYDVR0TAQH/BAgwBgEB/wIBATAN\n' +
     'BgkqhkiG9w0BAQsFAAOCAQEANcbMN72tllBycczeirWeJT2yN1uTVQMaFoimx0wH\n' +
     'jTCiRxB0xOSoo8AQscWmQmeNimp+37lAVR21fvTKieEvdYSUg+INOgoiV1TAGQta\n' +
     '9mUltLuz9ABPsiXFzxlqlALmCAsGKOojPwGD6QZcXpqQC2fWaf+6L3iLBUjpmaGu\n' +
     '0NaR8H5frza/1Dpe/DvDGHDR3xAgKns12bO6Y5B95FDUJ+B9h0c0MhliUvAH8uUq\n' +
     'c85vNzcDriKa280DrujhldJhUrSIBKSSLWY302jJ5+vwUjrZTIuTXEz+Fz2DkGaK\n' +
     'hVKSashjke0Qh27gghO9yrmQ8eG9pVQ65aO9m0UtS5yi3w==\n' +
     '-----END CERTIFICATE-----',
    centerScope: 'all',
    createdAt: '2026-03-11 09:00:00',
    domains: '*.example.cn, example.cn',
    id: 1,
    name: 'example-cn-wildcard',
    notAfter: '2027-03-11 23:59:59',
    notBefore: '2026-03-11 00:00:00',
    privateKey: 'REDACTED_DEMO_PRIVATE_KEY',
  },
  {
    certificate: '-----BEGIN CERTIFICATE-----\n' +
     'MIIC6DCCAdCgAwIBAgIUeovfFDmYzlP3iXsy4ULqOavdeccwDQYJKoZIhvcNAQEL\n' +
     'BQAwGTEXMBUGA1UEAwwOYXBpLmV4YW1wbGUuY24wHhcNMjUxMjAyMDAwMDAwWhcN\n' +
     'MjYxMjAyMjM1OTU5WjAZMRcwFQYDVQQDDA5hcGkuZXhhbXBsZS5jbjCCASIwDQYJ\n' +
     'KoZIhvcNAQEBBQADggEPADCCAQoCggEBAK6uDjCRDe+9LKMxqeW73oeOpajZRqfn\n' +
     'dZdE4kPLWvVDtcfgtZkPAiPUqbrAUoauqhTMlzjMGUtLt6EbtkcdNawXjUvJbnpX\n' +
     'WQp9ebv7qaz5RI7SaK8JW6nIHUNUD1Glx33s82Dr+7/rzfcFT9bSwnl9a7nQC2UY\n' +
     'aKqEKJHnsbP+PQL8l/u9fYHsHIYxvhmIRe8KTHM31dzj5rWygoq9veD3Le4xUs7D\n' +
     'lXlHKx5Ci8Nbet2FZg7mogxU6Ua8tqQusQ2ee6RHvLguIlfROduxdfTo6TWSjW0t\n' +
     'inakrIYCcVwK6ZPd854xyUP/OJv5r3mr7tr4G4Ra0L6I/GNhse5jQQECAwEAAaMo\n' +
     'MCYwCQYDVR0TBAIwADAZBgNVHREEEjAQgg5hcGkuZXhhbXBsZS5jbjANBgkqhkiG\n' +
     '9w0BAQsFAAOCAQEAil165a1UoshYgXLbPAYYkQ9OfE+cr/MHkfpdceO+rmpWVtQ6\n' +
     'mDW5CQFY5NQ+Fw7V5nuVtVqm/P6gv1XXAmDrrstZlJ97hiNLpzg/7/9LOlpi6dkW\n' +
     'ANkfKaRsgtAY7R/iozK0179TsswwuZL+oCzywFOpPahZD2JfXxaxr/0TWDI+osBe\n' +
     'EyKBZl8vRN//FhmrH+xbSxuJotUcAd/8JSilKCXq6ljtfVQQo3z9IHWu71FTXPfH\n' +
     'YaOG+6z9ppbbxS+Qsljyq7RUl18D+a6yKouqW+GozIG120WK9e5Qc/liCteXaigN\n' +
     'dDNhyhIDzIs9ihjwkr/fdLIDiab5w40/yGpOzQ==\n' +
     '-----END CERTIFICATE-----',
    centerScope: 'all',
    createdAt: '2026-03-11 09:12:00',
    domains: 'api.example.cn',
    id: 2,
    name: 'api-example-cn',
    notAfter: '2026-12-02 23:59:59',
    notBefore: '2025-12-02 00:00:00',
    privateKey: 'REDACTED_DEMO_PRIVATE_KEY',
  },
  {
    certificate: '-----BEGIN CERTIFICATE-----\n' +
     'MIIC9DCCAdygAwIBAgIUGLyBj3VTrIrOVqKKOBdRsbJOoxkwDQYJKoZIhvcNAQEL\n' +
     'BQAwHTEbMBkGA1UEAwwSY29uc29sZS5leGFtcGxlLmNuMB4XDTI1MTExOTAwMDAw\n' +
     'MFoXDTI2MTExOTIzNTk1OVowHTEbMBkGA1UEAwwSY29uc29sZS5leGFtcGxlLmNu\n' +
     'MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA1RoRNC+x+bOo+N6q9Njz\n' +
     'UolF24MEycOCGAD6iVOl1g1nzTFPdyzpk9Ps9mVVY1c76tkyLcyNdZzRzLvI4JnP\n' +
     'Sgbc+NogwdAtNpGfBGBA4NYZIrJJKBicR4fAHcEftC1Y7F+RrExky6OYYJ1LU2K4\n' +
     'VeavIS3BeanNjADY6YMr8+j6Xe+TyaxeclXly/ZV72An7CNJnJs3HhNCwxVDozfv\n' +
     '1MjZIevdJmlymf9EVReSJdJmtTzlay5OXV+/7o1096HxUIla8bFlvYEpvZKw7nDV\n' +
     '3gtIQxtJjZtgo+NopIpdpd6UQuVSrgT6DcFgDWQ0Jz2f8XypzfUVMxfXzwkkpreL\n' +
     'UwIDAQABoywwKjAJBgNVHRMEAjAAMB0GA1UdEQQWMBSCEmNvbnNvbGUuZXhhbXBs\n' +
     'ZS5jbjANBgkqhkiG9w0BAQsFAAOCAQEARkA7QG5hYDV2lhVRZEfm324eWy99yvBT\n' +
     'Wbuhw1RaGA4hsOfDdFrgxN/Dh3+TuNl7HzCjXciEnaNfSI+kgO2DnYaoSKNYDuHs\n' +
     'JV6bVHfP+qf1UwYO4qbl238GPo62HEe/lq+0+uWJ0k7KguliKs7MCN73O9dbJULg\n' +
     '7rA1QaGZPIcMIMEEOGKPl3snNT7ZfDF+soNDTl8YkXk50VnGK3lF/B32Rg4qv/z9\n' +
     'g+l+L+S+1CKjqsp9gzvSNTrDQ00o2Rhn/xVpyp34wLHX3hhLPiOayVeZIq/N5k6F\n' +
     '+VL1DYVsbECYbUUH1MdArRy8znKLZ2qrhTO+SsTpZIAO1X87SFiBjg==\n' +
     '-----END CERTIFICATE-----',
    centerScope: [1, 2, 4],
    createdAt: '2026-04-20 15:00:00',
    domains: 'console.example.cn',
    id: 3,
    name: 'console-example-cn',
    notAfter: '2026-11-19 23:59:59',
    notBefore: '2025-11-19 00:00:00',
    privateKey: 'REDACTED_DEMO_PRIVATE_KEY',
  },
  {
    certificate: '-----BEGIN CERTIFICATE-----\n' +
     'MIIC7jCCAdagAwIBAgIUagtTQ8OX4vuFkdmRZhLwVi7qw8QwDQYJKoZIhvcNAQEL\n' +
     'BQAwGzEZMBcGA1UEAwwQdHJhZGUuZXhhbXBsZS5jbjAeFw0yNjAxMDgwMDAwMDBa\n' +
     'Fw0yNzAxMDgyMzU5NTlaMBsxGTAXBgNVBAMMEHRyYWRlLmV4YW1wbGUuY24wggEi\n' +
     'MA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCppsmqJzrrn8kE2arz14vlT0j7\n' +
     '0qYxl23hzr7VricVyJH0A//nCHwCBpmU0HNMEyIguNVsbXgy/jei+tgquN3Rm/tS\n' +
     'e3F65+OJd9HgVcQ9EkTwy5u32sz4lKMlYU/rIl47oncExe0MjWFkkkAPgI6qlMZT\n' +
     'Y1nOHkSAm5g3Gke1mcxfS2pssSnNmN+vCUzT2sDEMRcOrmDmdyLMUlGydNmbKxOc\n' +
     'DhoEtRZOY1Vkgd1o71GC8LDc+D8sQbLXWIYfTvUCr+bUeypLkZxXsCawc8Ge8IKl\n' +
     'GnEeQLdCBN7qAe1TJuk9UnIqvgQM8hbdcq97RbD10z2d+Vtgg/VFzw6Ba23xAgMB\n' +
     'AAGjKjAoMAkGA1UdEwQCMAAwGwYDVR0RBBQwEoIQdHJhZGUuZXhhbXBsZS5jbjAN\n' +
     'BgkqhkiG9w0BAQsFAAOCAQEAUDFfFV/Qv0TACqzl0sxwUc0W2St+KMA6ucfhPXjO\n' +
     'by+O8pTa1gXra9mg59zO/7Udygp5YtqYFjCmy1vwK+zXtOasXkoN3SOXJA4Vh9ek\n' +
     '8c+A2dt+itgtrmHIOvEnIDCmFnR62UmJku6RiacHW+6AYIoUcB/RFeRkBGR+5oxK\n' +
     'YmVmi5SiIEhLQ9hpk4+metRDqTQ6y41mFjazpGZiELIk1ayOaJ1pz/7WAfaPzQJk\n' +
     'xJ6O89muZLVi7I21iC6uTkQ1GLxSAIBGDHY1TOocdjvKW81tWIfXDMffa/K/ZV6F\n' +
     '1BcLtY/RkQhbSjRP0x2LXctrDmRonSXDLnG35Zpr4HPcUg==\n' +
     '-----END CERTIFICATE-----',
    centerScope: [2],
    createdAt: '2026-06-01 10:30:00',
    domains: 'trade.example.cn',
    id: 4,
    name: 'trade-example-cn',
    notAfter: '2027-01-08 23:59:59',
    notBefore: '2026-01-08 00:00:00',
    privateKey: 'REDACTED_DEMO_PRIVATE_KEY',
  },
  {
    certificate: '-----BEGIN CERTIFICATE-----\n' +
     'MIIC7jCCAdagAwIBAgIUAJ9IB02YGHnSnJ2oRUZuvmdNYB4wDQYJKoZIhvcNAQEL\n' +
     'BQAwGzEZMBcGA1UEAwwQaW5mZXIuZXhhbXBsZS5jbjAeFw0yNTEwMzAwMDAwMDBa\n' +
     'Fw0yNjEwMjAyMzU5NTlaMBsxGTAXBgNVBAMMEGluZmVyLmV4YW1wbGUuY24wggEi\n' +
     'MA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQDlGjspiD2BfGLO0wBYSlZ06aFb\n' +
     'mVOoFmbhyIBGKKF0qw4c7Nr2gHUAVmIVVTER7v0OmBTTR4ytWVkG166G0gvAX0ys\n' +
     '/FGcYqG0J/4TpnO7NIzesC52j0MsfWqqX7ifJOyhZ+9+LyTO78HnVlBs6903Oh0n\n' +
     'Q2xLd8udsAq5EjSCr1eZJH3MmIh9QCJfLKDazAYOJ+nPpcTOd2Ooy3J0eIGVDfFw\n' +
     'Uxjye/CValmiNV9htIQwCoh9eZJ95uYvebGGeBdZiwixaUT7LIujJDQSaYNJEOvm\n' +
     'w8nMCw8d5pjzTVHYm7UtSVw1YSh8W8dKSlL9x2jJoWTPbGuTD97tUaQRmcx9AgMB\n' +
     'AAGjKjAoMAkGA1UdEwQCMAAwGwYDVR0RBBQwEoIQaW5mZXIuZXhhbXBsZS5jbjAN\n' +
     'BgkqhkiG9w0BAQsFAAOCAQEAPNCOwM2can7YxNvopiq/5pinWtUiO+sZnvPbPNi7\n' +
     'C0DYyWNRkr+i63B7QEeNPBS0XFiFA4mABUZX60HTrnPAdoGKLa4nOXo5aZF3o2dq\n' +
     'mA3zS07YawGdLKBmDb2S7pNXHjh3URYWVRNceINxRn8lqiV3twWWvWHIzWqGU4WX\n' +
     '7KFfOUEB3UpKxkYfMJ2vUdrwsvEQMGJlIEVXLgyb99cJs6oQhRRG5XcSWaLmBwGe\n' +
     'qCSMYS/bWM6ejiFWgaYLIvoVFR7taEqaQHJaPnhqbtniqKCq83J4ijAO/437CDWq\n' +
     'g4BcMh1FkiCRWr0By41Ss5zcp33jq3g3JexoGX9nbqskDw==\n' +
     '-----END CERTIFICATE-----',
    centerScope: [3],
    createdAt: '2026-07-15 13:20:00',
    domains: 'infer.example.cn',
    id: 5,
    name: 'infer-example-cn',
    notAfter: '2026-10-20 23:59:59',
    notBefore: '2025-10-30 00:00:00',
    privateKey: 'REDACTED_DEMO_PRIVATE_KEY',
  },
  {
    certificate: '-----BEGIN CERTIFICATE-----\n' +
     'MIIC8TCCAdmgAwIBAgIUOBFMgLRPDt/Mf75OKQlE7Qw+sRowDQYJKoZIhvcNAQEL\n' +
     'BQAwHDEaMBgGA1UEAwwRbGVnYWN5LmV4YW1wbGUuY24wHhcNMjUwMTAxMDAwMDAw\n' +
     'WhcNMjYwODAxMjM1OTU5WjAcMRowGAYDVQQDDBFsZWdhY3kuZXhhbXBsZS5jbjCC\n' +
     'ASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAMMlSI4j6q7iAojTSPjSG4ZR\n' +
     '62bh7wRfnZchMU+67sTHHg/GyZIPYUpy+K2CDvebg/JHOEUJS87rjYoyA0l1sqWW\n' +
     'w9wP8UAdhgQ6LhtgSW7Oua9cLtAyhW8W6wCpDsWCuNNWJhjUoUVxAWzaQqTbDsPw\n' +
     'Esj0dT0DsAREEROmFR/Ao91r3TpKdYyyNBIW0fkc3mbnGuMtJVgVZkU8GBUPA+Fb\n' +
     'fsxHHGto+p1zRRYpn8KHnngYAQ7OcP8DuLbVbzmbcwvhjPEEugCZYQ4HJzrCiVN8\n' +
     '65sBjQm6cB/TGzw7/Sj7iiu7e3oaTXCj/dVcpoFecCGTdAtgVKKy9SR6qAefe0EC\n' +
     'AwEAAaMrMCkwCQYDVR0TBAIwADAcBgNVHREEFTATghFsZWdhY3kuZXhhbXBsZS5j\n' +
     'bjANBgkqhkiG9w0BAQsFAAOCAQEADYHIC/ynJfBdHBxMlDpyIBmYP3O1FcqzcZQg\n' +
     'nIi/4cg+jb66y0a3kyO03pTxuTPtE+AK0HzJnqE5Z7gACABMF/piqtVSC5DEfekr\n' +
     'HxZ3HKK4tTKTL+Qww6Sp7is4WIAguqIwbqGT8Mi1VCGVJMK8JerEZObRv/12PXeK\n' +
     'BhsVGA50fUd7SfqQs7pokGI1ygjsXkA7JRhg4P7yc0Kmxle0RUIHrJxB6nPh30HS\n' +
     'DSKOfgX2TTHxWc7cmsLfmrP4PSNp60kgOGWzpnpTUqvksM1MPcFeAdXxs7msKjqZ\n' +
     '629G1rbKC4+KT8AFn0SDHUJf0IyaFbrgFiMZLYt8+cd8sv2zGA==\n' +
     '-----END CERTIFICATE-----',
    centerScope: [1],
    createdAt: '2025-01-06 10:00:00',
    domains: 'legacy.example.cn',
    id: 6,
    name: 'legacy-example-cn',
    notAfter: '2026-08-01 23:59:59',
    notBefore: '2025-01-01 00:00:00',
    privateKey: 'REDACTED_DEMO_PRIVATE_KEY',
  },
];

export const dnsResolvers: OrpDnsResolver[] = [
  {
    address: '10.60.0.11',
    cacheTtlSec: 30,
    centerId: 1,
    id: 1,
    port: 53,
    timeoutSec: 2,
  },  {
    address: '10.60.0.12',
    cacheTtlSec: 30,
    centerId: 1,
    id: 2,
    port: 53,
    timeoutSec: 2,
  },
  {
    address: '10.61.0.11',
    cacheTtlSec: 60,
    centerId: 2,
    id: 3,
    port: 53,
    timeoutSec: 3,
  },
  {
    address: '10.62.0.11',
    cacheTtlSec: 30,
    centerId: 3,
    id: 4,
    port: 53,
    timeoutSec: 2,
  },
  {
    address: '10.62.0.12',
    cacheTtlSec: 30,
    centerId: 3,
    id: 5,
    port: 53,
    timeoutSec: 2,
  },
  {
    address: '223.5.5.5',
    cacheTtlSec: 300,
    centerId: 4,
    id: 6,
    port: 53,
    timeoutSec: 5,
  },
];

/* ----------------------------- Upstream 上游服务器组 ----------------------------- */

export type OrpUpstreamLbPolicy =
  | 'consistent_hash'
  | 'hash'
  | 'ip_hash'
  | 'least_conn'
  | 'round_robin';

export type OrpUpstreamHealthCheckType = 'http' | 'none' | 'tcp';

export interface OrpUpstreamNode {
  backup: boolean;
  failTimeoutSec: number;
  host: string;
  maxFails: number;
  port: number;
  slowStartSec: number;
  weight: number;
}

export interface OrpUpstreamGroup {
  centerId: number;
  createdAt: string;
  description: string;
  healthCheck: {
    expectedStatus: number[];
    intervalSec: number;
    path: string;
    type: OrpUpstreamHealthCheckType;
  };
  id: number;
  lbPolicy: OrpUpstreamLbPolicy;
  name: string;
  nodes: OrpUpstreamNode[];
  tags: string[];
  updatedAt: string;
}

export const upstreamGroups: OrpUpstreamGroup[] = [
  {
    centerId: 1,
    createdAt: '2026-01-12 09:30:00',
    description: '主站 Web 应用服务器组',
    healthCheck: {
      expectedStatus: [200, 204],
      intervalSec: 5,
      path: '/healthz',
      type: 'http',
    },
    id: 1,
    lbPolicy: 'least_conn',
    name: 'web-frontend',
    nodes: [
      {
        backup: false,
        failTimeoutSec: 10,
        host: '10.60.1.11',
        maxFails: 3,
        port: 8080,
        slowStartSec: 0,
        weight: 3,
      },
      {
        backup: false,
        failTimeoutSec: 10,
        host: '10.60.1.12',
        maxFails: 3,
        port: 8080,
        slowStartSec: 0,
        weight: 3,
      },
      {
        backup: true,
        failTimeoutSec: 10,
        host: '10.60.1.13',
        maxFails: 2,
        port: 8080,
        slowStartSec: 30,
        weight: 1,
      },
    ],
    tags: ['web', 'prod'],
    updatedAt: '2026-08-20 15:10:00',
  },
  {
    centerId: 1,
    createdAt: '2026-02-03 14:00:00',
    description: '订单服务 gRPC 上游',
    healthCheck: {
      expectedStatus: [],
      intervalSec: 10,
      path: '',
      type: 'tcp',
    },
    id: 2,
    lbPolicy: 'round_robin',
    name: 'order-grpc',
    nodes: [
      {
        backup: false,
        failTimeoutSec: 10,
        host: '10.60.2.21',
        maxFails: 3,
        port: 9000,
        slowStartSec: 0,
        weight: 2,
      },
      {
        backup: false,
        failTimeoutSec: 10,
        host: '10.60.2.22',
        maxFails: 3,
        port: 9000,
        slowStartSec: 0,
        weight: 2,
      },
    ],
    tags: ['grpc', 'order'],
    updatedAt: '2026-07-11 11:40:00',
  },
  {
    centerId: 2,
    createdAt: '2026-03-18 10:15:00',
    description: '登录态保持会话粘性上游',
    healthCheck: {
      expectedStatus: [200],
      intervalSec: 5,
      path: '/healthz',
      type: 'http',
    },
    id: 3,
    lbPolicy: 'ip_hash',
    name: 'session-sticky',
    nodes: [
      {
        backup: false,
        failTimeoutSec: 15,
        host: '10.61.1.31',
        maxFails: 3,
        port: 8080,
        slowStartSec: 0,
        weight: 1,
      },
      {
        backup: false,
        failTimeoutSec: 15,
        host: '10.61.1.32',
        maxFails: 3,
        port: 8080,
        slowStartSec: 0,
        weight: 1,
      },
    ],
    tags: ['sticky', 'prod'],
    updatedAt: '2026-06-25 09:00:00',
  },
  {
    centerId: 3,
    createdAt: '2026-04-22 16:45:00',
    description: '缓存回源服务器组（一致性哈希）',
    healthCheck: {
      expectedStatus: [],
      intervalSec: 15,
      path: '',
      type: 'none',
    },
    id: 4,
    lbPolicy: 'consistent_hash',
    name: 'cache-origin',
    nodes: [
      {
        backup: false,
        failTimeoutSec: 10,
        host: '10.62.1.41',
        maxFails: 3,
        port: 3128,
        slowStartSec: 0,
        weight: 1,
      },
      {
        backup: false,
        failTimeoutSec: 10,
        host: '10.62.1.42',
        maxFails: 3,
        port: 3128,
        slowStartSec: 0,
        weight: 1,
      },
      {
        backup: true,
        failTimeoutSec: 10,
        host: '10.62.1.43',
        maxFails: 1,
        port: 3128,
        slowStartSec: 60,
        weight: 1,
      },
    ],
    tags: ['cache'],
    updatedAt: '2026-09-01 08:20:00',
  },
];

/** Upstream 名称唯一性校验（编辑时排除自身） */
export function upstreamNameExists(
  name: string,
  excludeId?: number,
): boolean {
  return upstreamGroups.some(
    (item) =>
      item.name === name && (excludeId === undefined || item.id !== excludeId),
  );
}

/** Upstream 引用检查：被 HTTP 路由或 Stream 服务引用时返回引用方列表 */
export function upstreamReferences(name: string): string[] {
  const refs: string[] = [];
  for (const listener of httpListeners) {
    for (const route of listener.routes) {
      // HTTP 路由 upstream 形如 "http://web-frontend"（协议前缀 + 组名）
      const target = route.upstream.replace(/^https?:\/\//, '').split(':')[0];
      if (target === name) {
        refs.push(`HTTP ${listener.domain}（路由 ${route.path}）`);
      }
    }
  }
  for (const service of streamServices) {
    // Stream backends 形如 "web-frontend:8080"（组名:端口）
    const targets = service.backends.map((b) => b.split(':')[0]);
    if (targets.includes(name)) {
      refs.push(`Stream ${service.listenAddress}:${service.listenPort}`);
    }
  }
  return refs;
}

export const auditLogs: OrpAuditLog[] = [
  {
    action: 'update',
    createdAt: '2026-09-28 16:42:10',
    detail:
      '变更前【策略：least_conn；节点 3 个】→ 变更后【策略：least_conn；节点 4 个【10.60.1.11:8080(w3)、10.60.1.12:8080(w3)、10.60.1.14:8080(w2)、10.60.1.13:8080(backup,w1)】；健康检查：http】',
    id: 1,
    ip: '10.60.0.5',
    module: 'upstream',
    operator: 'Admin',
    target: 'upstream:web-frontend',
  },
  {
    action: 'update',
    createdAt: '2026-09-28 14:05:36',
    detail: '节点 or-sh-ngx-03（华东生产中心）状态由 maintenance 变更为 online',
    id: 2,
    ip: '10.60.0.5',
    module: 'node',
    operator: 'Admin',
    target: 'node:or-sh-ngx-03',
  },
  {
    action: 'create',
    createdAt: '2026-09-28 11:20:04',
    detail: '地址：223.5.5.5:53；超时：5s；缓存：300s；中心：西南容灾中心',
    id: 3,
    ip: '10.63.0.8',
    module: 'dns',
    operator: 'Jack',
    target: 'dns:223.5.5.5:53',
  },
  {
    action: 'update',
    createdAt: '2026-09-27 19:31:52',
    detail:
      '变更前【路由 /v2/* 代理超时 30000ms】→ 变更后【路由 /v2/* 代理超时 45000ms，上游 http://web-frontend】',
    id: 4,
    ip: '10.60.0.5',
    module: 'http',
    operator: 'Admin',
    target: 'http:api.example.cn',
  },
  {
    action: 'offline',
    createdAt: '2026-09-27 17:12:40',
    detail: '节点 or-cd-ngx-02（西南容灾中心）状态由 online 变更为 offline',
    id: 5,
    ip: '10.63.0.8',
    module: 'node',
    operator: 'Jack',
    target: 'node:or-cd-ngx-02',
  },
  {
    action: 'update',
    createdAt: '2026-09-27 15:48:18',
    detail: '证书 *.example.cn 续期：有效期 2026-05-20 ~ 2027-09-01，关联域名 api/console/cdn',
    id: 6,
    ip: '10.60.0.5',
    module: 'tls',
    operator: 'Vben',
    target: 'tls:*.example.cn',
  },
  {
    action: 'create',
    createdAt: '2026-09-27 10:02:55',
    detail:
      '名称：infer-gateway；策略：least_conn；节点 3 个【10.62.3.41:9000(w2)、10.62.3.42:9000(w2)、10.62.3.43:9000(w1)】；健康检查：tcp',
    id: 7,
    ip: '10.62.0.6',
    module: 'upstream',
    operator: 'Admin',
    target: 'upstream:infer-gateway',
  },
  {
    action: 'delete',
    createdAt: '2026-09-26 20:15:30',
    detail: '地址：10.62.0.13:53；中心：华南算力中心',
    id: 8,
    ip: '10.62.0.6',
    module: 'dns',
    operator: 'Jack',
    target: 'dns:10.62.0.13:53',
  },
  {
    action: 'update',
    createdAt: '2026-09-26 16:40:12',
    detail: '监听 0.0.0.0:6379 backends 由 redis-cluster-01:6379、redis-cluster-02:6379 变更为新增 redis-cluster-03:6379',
    id: 9,
    ip: '10.61.0.4',
    module: 'stream',
    operator: 'Admin',
    target: 'stream:0.0.0.0:6379',
  },
  {
    action: 'create',
    createdAt: '2026-09-26 09:26:44',
    detail: '名称：or-gz-ngx-04；地址：10.62.3.34；中心：华南算力中心；通信端点 http://10.62.3.34:8081/control',
    id: 10,
    ip: '10.62.0.6',
    module: 'node',
    operator: 'Vben',
    target: 'node:or-gz-ngx-04',
  },
  {
    action: 'update',
    createdAt: '2026-09-25 18:54:20',
    detail: '变更前【描述：华北金融中心，交易链路四层代理】→ 变更后【描述：华北金融中心，交易与行情链路四层代理】',
    id: 11,
    ip: '10.61.0.4',
    module: 'center',
    operator: 'Admin',
    target: 'center:cn-north-2',
  },
  {
    action: 'delete',
    createdAt: '2026-09-25 14:22:08',
    detail: '路由 /legacy/*（上游 http://svc-legacy:8080）已下线删除',
    id: 12,
    ip: '10.60.0.5',
    module: 'http',
    operator: 'Jack',
    target: 'http:api.example.cn',
  },
  {
    action: 'update',
    createdAt: '2026-09-25 11:36:50',
    detail: '变更前【策略：round_robin】→ 变更后【策略：least_conn；节点 2 个；健康检查：tcp】',
    id: 13,
    ip: '10.60.0.5',
    module: 'upstream',
    operator: 'Admin',
    target: 'upstream:order-grpc',
  },
  {
    action: 'create',
    createdAt: '2026-09-24 17:08:26',
    detail: '域名 infer.example.cn 监听 8443，路由 /llm/* → http://svc-infer-llm:9000，绑定证书 infer.example.cn',
    id: 14,
    ip: '10.62.0.6',
    module: 'http',
    operator: 'Vben',
    target: 'http:infer.example.cn',
  },
  {
    action: 'offline',
    createdAt: '2026-09-24 10:44:32',
    detail: '节点 or-bj-ngx-03（华北金融中心）状态由 online 变更为 offline',
    id: 15,
    ip: '10.61.0.4',
    module: 'node',
    operator: 'Admin',
    target: 'node:or-bj-ngx-03',
  },
  {
    action: 'update',
    createdAt: '2026-09-23 16:30:15',
    detail: '监听 0.0.0.0:9092 backends 由 kafka-broker-01:9092、kafka-broker-02:9092 变更为新增 kafka-broker-03:9092',
    id: 16,
    ip: '10.61.0.4',
    module: 'stream',
    operator: 'Jack',
    target: 'stream:0.0.0.0:9092',
  },
  {
    action: 'create',
    createdAt: '2026-09-23 09:15:48',
    detail: '证书 gpu-metrics.example.cn 登记：有效期 2026-03-01 ~ 2027-03-01',
    id: 17,
    ip: '10.62.0.6',
    module: 'tls',
    operator: 'Vben',
    target: 'tls:gpu-metrics.example.cn',
  },
  {
    action: 'update',
    createdAt: '2026-09-22 15:52:33',
    detail: '变更前【地址：10.60.0.11:53；超时：2s；缓存：30s】→ 变更后【地址：10.60.0.11:53；超时：3s；缓存：60s；中心：华东生产中心】',
    id: 18,
    ip: '10.60.0.5',
    module: 'dns',
    operator: 'Admin',
    target: 'dns:10.60.0.11:53',
  },
  {
    action: 'update',
    createdAt: '2026-09-22 10:28:19',
    detail: '节点 or-sh-ngx-03（华东生产中心）状态由 online 变更为 maintenance',
    id: 19,
    ip: '10.60.0.5',
    module: 'node',
    operator: 'Admin',
    target: 'node:or-sh-ngx-03',
  },
  {
    action: 'create',
    createdAt: '2026-09-21 14:40:02',
    detail: '名称：华南算力中心；标识：cn-south-1；描述：华南算力中心，模型推理入口',
    id: 20,
    ip: '10.62.0.6',
    module: 'center',
    operator: 'Vben',
    target: 'center:cn-south-1',
  },
];

/* ------------------------------------------------------------------ */
/* 通用助手                                                            */
/* ------------------------------------------------------------------ */

let auditIdSeed = 1000;
let routeIdSeed = 2000;

export function nextId(list: { id: number }[]): number {
  return list.reduce((max, item) => Math.max(max, item.id), 0) + 1;
}

export function nextRouteId(): number {
  routeIdSeed += 1;
  return routeIdSeed;
}

export function now(): string {
  return new Date().toISOString().slice(0, 19).replaceAll('T', ' ');
}

export function recordAudit(
  operator: string,
  ip: string,
  log: Omit<OrpAuditLog, 'createdAt' | 'id' | 'ip' | 'operator'>,
) {
  auditIdSeed += 1;
  auditLogs.unshift({
    ...log,
    createdAt: now(),
    id: auditIdSeed,
    ip,
    operator,
  });
}

export function centerName(id: number): string {
  return centers.find((item) => item.id === id)?.name ?? `中心#${id}`;
}

/* 业务规则校验 ------------------------------------------------------- */

/** 中心删除约束：存在任一关联资源时禁止删除 */
export function centerUsage(id: number) {
  const nodeCount = nodes.filter((item) => item.centerId === id).length;
  const httpCount = httpListeners.filter((item) => item.centerId === id).length;
  const streamCount = streamServices.filter(
    (item) => item.centerId === id,
  ).length;
  const dnsCount = dnsResolvers.filter((item) => item.centerId === id).length;
  const upstreamCount = upstreamGroups.filter(
    (item) => item.centerId === id,
  ).length;
  const certCount = certificates.filter(
    (item) =>
      Array.isArray(item.centerScope) && item.centerScope.includes(id),
  ).length;
  const total =
    nodeCount + httpCount + streamCount + dnsCount + upstreamCount + certCount;
  return {
    certCount,
    detail: `节点 ${nodeCount}、HTTP 监听 ${httpCount}、Stream 服务 ${streamCount}、Upstream 组 ${upstreamCount}、DNS 解析器 ${dnsCount}、证书 ${certCount}`,
    dnsCount,
    httpCount,
    nodeCount,
    streamCount,
    total,
    upstreamCount,
  };
}

/** 端口冲突校验（HTTP：同中心同端口） */
export function httpPortConflict(
  centerId: number,
  port: number,
  excludeId?: number,
): OrpHttpListener | undefined {
  return httpListeners.find(
    (item) =>
      item.centerId === centerId && item.port === port && item.id !== excludeId,
  );
}

/** 端口冲突校验（Stream：同中心同协议同端口） */
export function streamPortConflict(
  centerId: number,
  port: number,
  protocol: 'tcp' | 'udp',
  excludeId?: number,
): OrpStreamService | undefined {
  return streamServices.find(
    (item) =>
      item.centerId === centerId &&
      item.protocol === protocol &&
      item.listenPort === port &&
      item.id !== excludeId,
  );
}

/** 证书格式校验（Mock 层面：PEM 头部结构检查） */
export function validateCertificatePem(
  certificate: string | undefined,
  privateKey: string | undefined,
): null | string {
  if (certificate && !certificate.includes('BEGIN CERTIFICATE')) {
    return '证书格式错误：公钥证书必须是 PEM 格式（缺少 BEGIN CERTIFICATE 头）';
  }
  if (privateKey && !privateKey.includes('PRIVATE KEY')) {
    return '证书格式错误：私钥必须是 PEM 格式（缺少 PRIVATE KEY 头）';
  }
  return null;
}
