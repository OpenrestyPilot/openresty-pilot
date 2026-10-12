<script lang="ts" setup>
import type { EChartsOption } from 'echarts';
import type {
  DashboardMetrics,
  DashboardRange,
  DashboardTopRankings,
  DashboardTrends,
} from '#/api/orp/types';

import { computed, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  Card,
  Col,
  Empty,
  Progress,
  Row,
  Select,
  Spin,
  Table,
  Tag,
  Tooltip,
} from 'ant-design-vue';

import { consumeDashboardEvents } from '#/api/orp/dashboard';
import { listCentersApi } from '#/api/orp/centers';

import EChart from './echart.vue';

defineOptions({ name: 'OrpDashboard' });

/* ----------------------------- 筛选状态 ----------------------------- */
const centers = ref<{ id: number; name: string }[]>([]);
const centerId = ref<number>(0); // 0 = 全部中心
const range = ref<DashboardRange>('today');
const loading = ref(false);
const loadError = ref(false);
const streamError = ref('大盘实时数据连接失败，正在重试；也可手动重新连接');
const lastUpdated = ref('');
const streamConnected = ref(false);

const RANGE_OPTIONS: { label: string; value: DashboardRange }[] = [
  { label: '今日', value: 'today' },
  { label: '近 30 分钟', value: '30m' },
  { label: '近 1 小时', value: '1h' },
  { label: '近 6 小时', value: '6h' },
  { label: '近 24 小时', value: '24h' },
  { label: '近 7 天', value: '7d' },
];

/* ----------------------------- 数据 ----------------------------- */
const metrics = ref<DashboardMetrics | null>(null);
const trends = ref<DashboardTrends | null>(null);
const rankings = ref<DashboardTopRankings | null>(null);

let streamController: AbortController | null = null;
let streamGeneration = 0;
let streamActive = false;

async function runStream(controller: AbortController, generation: number) {
  const signal = controller.signal;
  const params = {
    centerId: centerId.value || undefined,
    range: range.value,
  };
  const pause = () => new Promise<void>((resolve) => {
    const onAbort = () => {
      clearTimeout(timer);
      resolve();
    };
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', onAbort);
      resolve();
    }, 3000);
    signal.addEventListener('abort', onAbort, { once: true });
  });
  while (!signal.aborted && generation === streamGeneration) {
    try {
      await consumeDashboardEvents(params, signal, (snapshot) => {
        metrics.value = snapshot.metrics;
        trends.value = snapshot.trends;
        rankings.value = snapshot.rankings;
        lastUpdated.value = new Date(snapshot.updatedAt).toLocaleTimeString('zh-CN', { hour12: false });
        loading.value = false;
        loadError.value = false;
        streamError.value = '';
        streamConnected.value = true;
      }, (message) => {
        loadError.value = true;
        streamError.value = message;
      });
    } catch (error) {
      if (signal.aborted) break;
      streamConnected.value = false;
      loading.value = false;
      loadError.value = true;
      if (error instanceof Error) streamError.value = error.message;
      if (error instanceof Error && error.message.includes('登录状态')) break;
      await pause();
    }
  }
}

function startStream(force = false) {
  if (streamActive && !force) return;
  streamController?.abort();
  streamActive = true;
  streamGeneration += 1;
  streamController = new AbortController();
  loading.value = true;
  loadError.value = false;
  streamConnected.value = false;
  void runStream(streamController, streamGeneration);
}

function load() {
  startStream(true);
}

function stopStream() {
  streamActive = false;
  streamController?.abort();
  streamController = null;
  if (debounceTimer) {
    clearTimeout(debounceTimer);
    debounceTimer = null;
  }
}

/* 筛选变更 → 300ms 防抖后重连，以新条件订阅 */
let debounceTimer: null | ReturnType<typeof setTimeout> = null;
function debouncedLoad() {
  if (debounceTimer) clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => startStream(true), 300);
}

onBeforeUnmount(() => {
  stopStream();
});

onMounted(async () => {
  try {
    const page = await listCentersApi({ page: 1, pageSize: 50 });
    centers.value = (page?.items ?? []).map((c) => ({ id: c.id, name: c.name }));
  } catch {
    /* 保留空列表 */
  }
  startStream();
});

onActivated(startStream);
onDeactivated(stopStream);

/* ----------------------------- 格式化 ----------------------------- */
function fmtNum(n: number | undefined): string {
  if (n === undefined) return '-';
  if (n >= 1000_000_000) return `${(n / 1000_000_000).toFixed(2)}B`;
  if (n >= 1000_000) return `${(n / 1000_000).toFixed(2)}M`;
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`;
  return String(n);
}

function fmtQps(n: number | undefined): string {
  return n === undefined ? '-' : String(Number(n.toFixed(2)));
}

function fmtMbps(n: number | undefined): string {
  return n === undefined ? '-' : `${(n / 1000).toFixed(2)} Gbps`;
}

/* 状态码总请求数（用于环形图空态判断） */
const totalStatus = computed(() => {
  const sc = metrics.value?.statusCodes;
  return (sc?.c2xx ?? 0) + (sc?.c3xx ?? 0) + (sc?.c4xx ?? 0) + (sc?.c5xx ?? 0);
});


/* ----------------------------- 指标卡 ----------------------------- */
const cards = computed<Array<{ color: string; icon: string; label: string; tooltip?: string; value: string }>>(() => {
  const s = metrics.value?.summary;
  return [
    { label: '请求总量', value: fmtNum(s?.totalRequests), icon: '🔥', color: '#1668dc' },
    { label: 'QPS 峰值 / 均值', value: `${fmtQps(s?.qpsPeak)} / ${fmtQps(s?.qpsAvg)}`, icon: '⚡', color: '#13c2c2' },
    { label: '流入 / 流出带宽', value: `${fmtMbps(s?.bandwidthInMbps)} / ${fmtMbps(s?.bandwidthOutMbps)}`, icon: '🌐', color: '#2f54eb' },
    { label: '活跃连接数', value: fmtNum(s?.activeConns), icon: '🔗', color: '#722ed1' },
    { label: '错误率（4xx+5xx）', value: `${s?.errorRate ?? '-'}%`, icon: '⚠️', color: '#d4380d' },
    {
      label: '服务可用率',
      value: s?.availability == null ? '暂无数据' : `${s.availability.toFixed(2)}%`,
      icon: '✅',
      color: '#389e0d',
      tooltip: '按所选时间范围内节点探活成功次数 ÷ 总探测次数计算；没有探活样本时显示暂无数据。',
    },
  ];
});

/* ----------------------------- 趋势图：QPS（左轴）+ 带宽（右轴）双 Y 轴 ----------------------------- */
const trendOption = computed<EChartsOption>(() => {
  const points = trends.value?.points ?? [];
  return {
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
    },
    legend: { data: ['QPS', '流入带宽', '流出带宽', '平均延迟'], top: 0 },
    grid: { left: 64, right: 64, top: 34, bottom: 48 },
    xAxis: { type: 'category', data: points.map((p) => p.time), boundaryGap: false },
    yAxis: [
      { type: 'value', name: 'QPS (req/s)', axisLabel: { formatter: (v: number) => fmtNum(v) } },
      {
        type: 'value',
        name: '带宽 (Mbps)',
        splitLine: { show: false },
        axisLabel: { formatter: (v: number) => (v >= 1000 ? `${(v / 1000).toFixed(1)}G` : `${v}M`) },
      },
    ],
    dataZoom: [
      { type: 'inside', start: 0, end: 100 },
      { type: 'slider', height: 16, bottom: 6 },
    ],
    series: [
      {
        name: 'QPS',
        type: 'line',
        smooth: true,
        symbol: 'none',
        yAxisIndex: 0,
        data: points.map((p) => p.qps),
        itemStyle: { color: '#1668dc' },
        areaStyle: { opacity: 0.12 },
      },
      {
        name: '流入带宽',
        type: 'line',
        smooth: true,
        symbol: 'none',
        yAxisIndex: 1,
        data: points.map((p) => p.inMbps),
        itemStyle: { color: '#9254de' },
      },
      {
        name: '流出带宽',
        type: 'line',
        smooth: true,
        symbol: 'none',
        yAxisIndex: 1,
        data: points.map((p) => p.outMbps),
        itemStyle: { color: '#13c2c2' },
      },
      {
        name: '平均延迟',
        type: 'line',
        smooth: true,
        symbol: 'none',
        yAxisIndex: 1,
        data: points.map((p) => p.avgLatencyMs),
        itemStyle: { color: '#faad14' },
      },
    ],
  };
});

/* ----------------------------- 状态码分布环形图 ----------------------------- */
/** 按大类聚合细分状态码明细，供 Tooltip 下钻展示 */
function detailTextOf(category: string): string {
  const details = metrics.value?.statusCodes?.details ?? [];
  const prefix = category.slice(0, 1);
  const matched = details.filter((d) => String(d.code).startsWith(prefix));
  if (matched.length === 0) return '';
  return matched.map((d) => `${d.code}: ${fmtNum(d.count)}`).join(' / ');
}

const statusOption = computed<EChartsOption>(() => {
  const sc = metrics.value?.statusCodes;
  const data = [
    { name: '2xx 成功', value: sc?.c2xx ?? 0, itemStyle: { color: '#52c41a' } },
    { name: '3xx 重定向', value: sc?.c3xx ?? 0, itemStyle: { color: '#1677ff' } },
    { name: '4xx 客户端错误', value: sc?.c4xx ?? 0, itemStyle: { color: '#faad14' } },
    { name: '5xx 服务端错误', value: sc?.c5xx ?? 0, itemStyle: { color: '#ff4d4f' } },
  ];
  const total = data.reduce((sum, d) => sum + d.value, 0) || 1;
  return {
    tooltip: {
      trigger: 'item',
      formatter: (p: any) => {
        const details = detailTextOf(String(p.name));
        const lines = [
          `${p.marker}${p.name}`,
          `${fmtNum(p.value as number)}（${((p.value / total) * 100).toFixed(2)}%）`,
        ];
        if (details) lines.push(`细分：${details}`);
        return lines.join('<br/>');
      },
    },
    legend: {
      bottom: 0,
      formatter: (name: string) => {
        const item = data.find((d) => d.name === name);
        if (!item) return name;
        return `${name}  ${fmtNum(item.value)}（${((item.value / total) * 100).toFixed(1)}%）`;
      },
    },
    graphic: [
      {
        type: 'text',
        left: 'center',
        top: '40%',
        style: {
          text: fmtNum(total),
          textAlign: 'center',
          fill: 'rgba(0,0,0,0.88)',
          fontSize: 18,
          fontWeight: 700,
        },
      },
      {
        type: 'text',
        left: 'center',
        top: '52%',
        style: {
          text: '总请求数',
          textAlign: 'center',
          fill: 'rgba(0,0,0,0.45)',
          fontSize: 12,
        },
      },
    ],
    series: [
      {
        type: 'pie',
        radius: ['52%', '72%'],
        center: ['50%', '46%'],
        avoidLabelOverlap: true,
        itemStyle: { borderRadius: 4, borderColor: '#fff', borderWidth: 2 },
        label: { show: false },
        data,
      },
    ],
  };
});

/* ----------------------------- 延迟分布 ----------------------------- */
const latencyOption = computed<EChartsOption>(() => {
  const buckets = metrics.value?.latency?.buckets ?? [];
  return {
    tooltip: { trigger: 'axis' },
    grid: { left: 64, right: 24, top: 20, bottom: 32 },
    xAxis: { type: 'category', data: buckets.map((b) => b.label) },
    yAxis: { type: 'value', axisLabel: { formatter: (v: number) => fmtNum(v) } },
    series: [
      {
        name: '请求数',
        type: 'bar',
        barWidth: '52%',
        data: buckets.map((b) => b.count),
        itemStyle: { color: '#1668dc', borderRadius: [4, 4, 0, 0] },
      },
    ],
  };
});

/* ----------------------------- 表格列 ----------------------------- */
const domainColumns = [
  { title: '域名', dataIndex: 'domain', key: 'domain', ellipsis: true },
  { title: '请求数', dataIndex: 'requests', key: 'requests', width: 100, sorter: (a: any, b: any) => a.requests - b.requests },
  { title: '占比', dataIndex: 'percent', key: 'percent', width: 88 },
  { title: '平均延迟', dataIndex: 'avgLatencyMs', key: 'avgLatencyMs', width: 92 },
];

const routeColumns = [
  { title: '域名 / 路由', key: 'route', ellipsis: true },
  { title: '请求数', dataIndex: 'requests', key: 'requests', width: 96, sorter: (a: any, b: any) => a.requests - b.requests },
  { title: '占比', dataIndex: 'percent', key: 'percent', width: 84 },
  { title: '平均延迟', dataIndex: 'avgLatencyMs', key: 'avgLatencyMs', width: 92 },
];

const upstreamColumns = [
  { title: '上游组', dataIndex: 'name', key: 'name', ellipsis: true },
  { title: '中心', dataIndex: 'centerName', key: 'centerName', width: 120, ellipsis: true },
  { title: '策略', dataIndex: 'lbPolicy', key: 'lbPolicy', width: 118 },
  { title: '健康节点', key: 'healthy', width: 96 },
  { title: '在线率', dataIndex: 'onlineRate', key: 'onlineRate', width: 90 },
  { title: '状态', key: 'status', width: 84 },
];

const nodeColumns = [
  { title: '节点', dataIndex: 'name', key: 'name', ellipsis: true },
  { title: '中心', dataIndex: 'centerName', key: 'centerName', width: 120, ellipsis: true },
  { title: '状态', key: 'status', width: 88 },
  { title: 'CPU', key: 'cpu', width: 130 },
  { title: '内存', key: 'mem', width: 130 },
  { title: '连接数', dataIndex: 'conns', key: 'conns', width: 90, sorter: (a: any, b: any) => a.conns - b.conns },
];

const STATUS_TEXT: Record<string, { color: string; text: string }> = {
  degraded: { color: 'warning', text: '降级' },
  down: { color: 'error', text: '异常' },
  healthy: { color: 'success', text: '健康' },
  maintenance: { color: 'processing', text: '维护' },
  offline: { color: 'default', text: '离线' },
  online: { color: 'success', text: '在线' },
};

function statusOf(s: string) {
  return STATUS_TEXT[s] ?? { color: 'default', text: s };
}

const LB_TEXT: Record<string, string> = {
  consistent_hash: '一致性哈希',
  hash: '通用哈希',
  ip_hash: 'IP 哈希',
  least_conn: '最少连接',
  round_robin: '轮询',
};
</script>

<template>
  <Page title="控制面大盘">
    <div class="flex flex-col gap-4">
      <!-- 联动筛选栏 -->
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="text-muted-foreground text-sm">
          网关集群全局流量观测 · 访问日志与节点遥测实时推送
          <span v-if="lastUpdated" class="ml-2 text-xs">
            更新于 {{ lastUpdated }}
          </span>
          <Tag class="ml-2" :color="streamConnected ? 'success' : 'default'">
            {{ streamConnected ? 'SSE 实时推送中' : 'SSE 正在连接' }}
          </Tag>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <Select
            v-model:value="centerId"
            :options="[{ id: 0, name: '全部中心' }, ...centers].map((c) => ({ label: c.name, value: c.id }))"
            style="width: 160px"
            show-search
            option-filter-prop="label"
            placeholder="选择中心"
            @change="debouncedLoad"
          />
          <Select
            v-model:value="range"
            :options="RANGE_OPTIONS"
            style="width: 130px"
            @change="debouncedLoad"
          />
          <Button size="small" :loading="loading" @click="load">重新连接</Button>
        </div>
      </div>

      <!-- 加载失败重试 -->
      <div
        v-if="loadError"
        class="flex items-center justify-between rounded border border-red-200 bg-red-50 px-4 py-2 text-sm text-red-600"
      >
        <span>{{ streamError }}</span>
        <Button size="small" danger @click="load">点击重试</Button>
      </div>

      <Spin :spinning="loading">
        <!-- 指标卡 -->
        <div class="grid grid-cols-2 gap-3 md:grid-cols-3 lg:grid-cols-6">
          <Card
            v-for="card in cards"
            :key="card.label"
            size="small"
            class="shadow-none"
          >
            <div class="flex items-center gap-2 text-xs text-gray-500">
              <span>{{ card.icon }}</span>
              <Tooltip v-if="card.tooltip" :title="card.tooltip">
                <span class="cursor-help truncate border-b border-dotted border-current">{{ card.label }}</span>
              </Tooltip>
              <span v-else class="truncate">{{ card.label }}</span>
            </div>
            <div class="mt-1 text-lg font-semibold" :style="{ color: card.color }">
              {{ card.value }}
            </div>
          </Card>
        </div>

        <!-- 趋势图：QPS（左轴）与带宽（右轴）双 Y 轴 + DataZoom -->
        <Card size="small" class="mt-4" title="QPS 与带宽趋势">
          <Empty
            v-if="!loading && (trends?.points ?? []).length === 0"
            description="暂无监控数据"
          />
          <EChart v-else :option="trendOption" height="300px" />
        </Card>

        <!-- 状态码 + 延迟分布 + 延迟指标 -->
        <Row :gutter="12" class="mt-4">
          <Col :md="8" :xs="24">
            <Card size="small" title="HTTP 状态码分布" class="h-full">
              <Empty
                v-if="!loading && totalStatus === 0"
                description="暂无监控数据"
              />
              <EChart v-else :option="statusOption" height="240px" />
            </Card>
          </Col>
          <Col :md="8" :xs="24">
            <Card size="small" title="响应延迟分布" class="h-full">
              <EChart :option="latencyOption" height="240px" />
            </Card>
          </Col>
          <Col :md="8" :xs="24">
            <Card size="small" title="延迟分位指标" class="h-full">
              <div class="flex h-full flex-col justify-center gap-4 py-2">
                <div class="flex items-center justify-between">
                  <span class="text-sm text-gray-500">P50 平均延迟</span>
                  <span class="text-lg font-semibold text-blue-600">
                    {{ metrics?.latency?.p50Ms ?? '-' }} ms
                  </span>
                </div>
                <div class="flex items-center justify-between">
                  <span class="text-sm text-gray-500">P95 延迟</span>
                  <span class="text-lg font-semibold text-orange-500">
                    {{ metrics?.latency?.p95Ms ?? '-' }} ms
                  </span>
                </div>
                <div class="flex items-center justify-between">
                  <span class="text-sm text-gray-500">P99 延迟</span>
                  <span class="text-lg font-semibold text-red-500">
                    {{ metrics?.latency?.p99Ms ?? '-' }} ms
                  </span>
                </div>
              </div>
            </Card>
          </Col>
        </Row>

        <!-- Top 排行 -->
        <Row :gutter="12" class="mt-4">
          <Col :md="12" :xs="24">
            <Card size="small" title="Top 域名请求排行">
              <Table
                :columns="domainColumns"
                :data-source="rankings?.domains ?? []"
                :pagination="false"
                size="small"
                row-key="domain"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'requests'">
                    {{ fmtNum(record.requests) }}
                  </template>
                  <template v-else-if="column.key === 'avgLatencyMs'">
                    {{ record.avgLatencyMs }} ms
                  </template>
                  <template v-else-if="column.key === 'percent'">
                    <Progress
                      :percent="record.percent"
                      :show-info="false"
                      size="small"
                      stroke-color="#1668dc"
                    />
                    <span class="text-xs text-gray-500">{{ record.percent }}%</span>
                  </template>
                </template>
              </Table>
            </Card>
          </Col>
          <Col :md="12" :xs="24">
            <Card size="small" title="Top 路由请求排行">
              <Table
                :columns="routeColumns"
                :data-source="rankings?.routes ?? []"
                :pagination="false"
                size="small"
                row-key="path"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'route'">
                    <div class="truncate">
                      <div class="text-xs text-gray-400">{{ record.domain }}</div>
                      <span class="font-medium">{{ record.path }}</span>
                    </div>
                  </template>
                  <template v-else-if="column.key === 'requests'">
                    {{ fmtNum(record.requests) }}
                  </template>
                  <template v-else-if="column.key === 'avgLatencyMs'">
                    {{ record.avgLatencyMs }} ms
                  </template>
                  <template v-else-if="column.key === 'percent'">
                    <Progress
                      :percent="record.percent"
                      :show-info="false"
                      size="small"
                      stroke-color="#13c2c2"
                    />
                    <span class="text-xs text-gray-500">{{ record.percent }}%</span>
                  </template>
                </template>
              </Table>
            </Card>
          </Col>
        </Row>

        <!-- Upstream 健康 + 节点负载 -->
        <Row :gutter="12" class="mt-4">
          <Col :md="12" :xs="24">
            <Card size="small" title="上游服务器组健康状态">
              <template #extra>
                <span class="text-xs">
                  <Tag color="success">健康 {{ metrics?.upstreamHealth?.healthy ?? 0 }}</Tag>
                  <Tag color="warning">降级 {{ metrics?.upstreamHealth?.degraded ?? 0 }}</Tag>
                  <Tag color="error">异常 {{ metrics?.upstreamHealth?.down ?? 0 }}</Tag>
                </span>
              </template>
              <Table
                :columns="upstreamColumns"
                :data-source="metrics?.upstreamHealth?.groups ?? []"
                :pagination="false"
                :scroll="{ y: 260 }"
                size="small"
                row-key="name"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'lbPolicy'">
                    {{ LB_TEXT[record.lbPolicy] ?? record.lbPolicy }}
                  </template>
                  <template v-else-if="column.key === 'healthy'">
                    {{ record.healthyCount }} / {{ record.total }}
                  </template>
                  <template v-else-if="column.key === 'onlineRate'">
                    {{ record.onlineRate }}%
                  </template>
                  <template v-else-if="column.key === 'status'">
                    <Tag :color="statusOf(record.status).color">
                      {{ statusOf(record.status).text }}
                    </Tag>
                  </template>
                </template>
              </Table>
            </Card>
          </Col>
          <Col :md="12" :xs="24">
            <Card size="small" title="节点实例负载概览">
              <Table
                :columns="nodeColumns"
                :data-source="metrics?.nodeLoad ?? []"
                :pagination="false"
                :scroll="{ y: 260 }"
                size="small"
                row-key="name"
              >
                <template #bodyCell="{ column, record }">
                  <template v-if="column.key === 'status'">
                    <Tag :color="statusOf(record.status).color">
                      {{ statusOf(record.status).text }}
                    </Tag>
                  </template>
                  <template v-else-if="column.key === 'cpu'">
                    <Progress
                      :percent="record.cpuPercent"
                      :status="record.cpuPercent > 80 ? 'exception' : 'normal'"
                      size="small"
                    />
                  </template>
                  <template v-else-if="column.key === 'mem'">
                    <Progress
                      :percent="record.memPercent"
                      :status="record.memPercent > 85 ? 'exception' : 'normal'"
                      size="small"
                      stroke-color="#722ed1"
                    />
                  </template>
                  <template v-else-if="column.key === 'conns'">
                    {{ fmtNum(record.conns) }}
                  </template>
                </template>
              </Table>
            </Card>
          </Col>
        </Row>
      </Spin>
    </div>
  </Page>
</template>
