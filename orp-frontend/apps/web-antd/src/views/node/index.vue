<script lang="ts" setup>
import type { VxeGridProps } from '#/adapter/vxe-table';
import type { NodeItem, NodeStatus } from '#/api';

import { onMounted, ref, watch } from 'vue';
import { useRouter } from 'vue-router';

import { Page, useVbenDrawer, useVbenModal } from '@vben/common-ui';

import { Modal as AntModal, Button, Card, Empty, Input, Pagination, Radio, Select, Spin, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteNodeApi, listCentersApi, listNodesApi } from '#/api';

import NodeFormModal from './form-modal.vue';
import AgentBootstrapModal from './agent-bootstrap-modal.vue';
import NodeLatencyDrawer from './latency-drawer.vue';
import NodeMetricsDrawer from './metrics-drawer.vue';
import { STATUS_META, STATUS_OPTIONS } from './status-meta';
import NodeStatusModal from './status-modal.vue';

defineOptions({ name: 'NodeManagement' });

const router = useRouter();

const viewMode = ref<'card' | 'table'>('card');
const centerId = ref<'all' | number>('all');
const status = ref<NodeStatus | undefined>();
const searchKeyword = ref('');
const keyword = ref('');
const centerOptions = ref<Array<{ label: string; value: 'all' | number }>>([
  { label: '全部中心', value: 'all' },
]);
const cardItems = ref<NodeItem[]>([]);
const cardTotal = ref(0);
const cardPage = ref(1);
const cardPageSize = ref(20);
const cardLoading = ref(false);
let cardRequestId = 0;
const quickBootstrapAfterCreate = ref(false);

async function loadCenters() {
  try {
    const result = await listCentersApi({ page: 1, pageSize: 200 });
    centerOptions.value = [
      { label: '全部中心', value: 'all' },
      ...result.items.map((item) => ({ label: item.name, value: item.id })),
    ];
  } catch {
    // 错误提示由请求拦截器统一处理
  }
}

async function loadCards() {
  const requestId = ++cardRequestId;
  cardLoading.value = true;
  try {
    const result = await listNodesApi({
      centerId: centerId.value === 'all' ? undefined : centerId.value,
      keyword: keyword.value || undefined,
      page: cardPage.value,
      pageSize: cardPageSize.value,
      status: status.value,
    });
    if (requestId !== cardRequestId) return;
    if (result.items.length === 0 && result.total > 0 && cardPage.value > 1) {
      cardPage.value -= 1;
      await loadCards();
      return;
    }
    cardItems.value = result.items;
    cardTotal.value = result.total;
  } catch {
    if (requestId !== cardRequestId) return;
    cardItems.value = [];
    cardTotal.value = 0;
  } finally {
    if (requestId === cardRequestId) cardLoading.value = false;
  }
}

function refresh() {
  if (viewMode.value === 'card') void loadCards();
  else gridApi.query();
}

function onFilterChange() {
  cardPage.value = 1;
  refresh();
}

function onSearch() {
  keyword.value = searchKeyword.value.trim();
  onFilterChange();
}

function onReset() {
  centerId.value = 'all';
  status.value = undefined;
  searchKeyword.value = '';
  keyword.value = '';
  onFilterChange();
}

function onCardPageChange(page: number, pageSize: number) {
  cardPage.value = page;
  cardPageSize.value = pageSize;
  void loadCards();
}

onMounted(() => {
  void loadCenters();
  void loadCards();
});
watch(viewMode, (mode) => { if (mode === 'card') void loadCards(); });

/** 定时健康检查状态配色：在线＝绿色、不稳定＝橙黄、离线＝红色 */
/** 在线兜底元数据，规避 Record<string> 索引 possibly undefined */
const HEALTH_META_ONLINE = {
  dot: 'bg-emerald-500',
  label: '在线',
  pill: 'bg-emerald-50 border-emerald-200 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300',
};
const HEALTH_META_UNKNOWN = {
  dot: 'bg-gray-400',
  label: '未采集',
  pill: 'bg-gray-50 border-gray-200 text-gray-500 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-400',
};
const HEALTH_META: Record<
  string,
  { dot: string; label: string; pill: string }
> = {
  online: HEALTH_META_ONLINE,
  degraded: {
    dot: 'bg-amber-500',
    label: '不稳定',
    pill: 'bg-amber-50 border-amber-200 text-amber-700 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-300',
  },
  offline: {
    dot: 'bg-rose-500',
    label: '离线',
    pill: 'bg-rose-50 border-rose-200 text-rose-700 dark:border-rose-800 dark:bg-rose-950 dark:text-rose-300',
  },
  unknown: HEALTH_META_UNKNOWN,
};

/** 响应延迟分级：>500ms 红色、>300ms 橙色 */
function latencyClass(ms: null | number | undefined): string {
  if (ms == null)
    return 'border-gray-200 bg-gray-50 text-gray-400 dark:border-gray-700 dark:bg-gray-800 dark:text-gray-500';
  if (ms > 500)
    return 'border-rose-200 bg-rose-50 text-rose-600 dark:border-rose-800 dark:bg-rose-950 dark:text-rose-300';
  if (ms > 300)
    return 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800 dark:bg-amber-950 dark:text-amber-300';
  return 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800 dark:bg-emerald-950 dark:text-emerald-300';
}

const gridOptions: VxeGridProps<NodeItem> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'name', minWidth: 130, title: '节点名称' },
    { field: 'host', title: '主机地址', width: 120 },
    {
      field: 'status',
      slots: { default: 'status' },
      title: '运行状态',
      width: 128,
    },
    {
      field: 'healthStatus',
      slots: { default: 'health' },
      title: '健康状态',
      titleHelp: {
        content:
          '已接入 Node Agent 的节点依据最近 2 分钟心跳和 OpenResty 进程状态显示；其他节点每 10 秒主动探测，最近 3 次全部成功为在线、部分失败为不稳定、全部失败为离线。',
      },
      width: 100,
    },
    {
      field: 'lastLatencyMs',
      slots: { default: 'latency' },
      title: '响应延迟',
      titleHelp: {
        content:
          '最近一次成功探活的往返延迟（RTT）。高于 300ms 橙色预警，高于 500ms 红色告警；探活超时不可达时展示「-」。',
      },
      width: 100,
    },
    {
      field: 'lastCheckedAt',
      showOverflow: true,
      title: '最近检查',
      width: 150,
    },
    {
      field: 'weight',
      slots: { default: 'weight' },
      title: '下发权重',
      titleHelp: {
        content:
          '权重分批下发时的排序依据：低权重实例优先金丝雀灰度验证（1-100，默认 10）。',
      },
      width: 100,
    },
    {
      field: 'layer',
      slots: { default: 'layer' },
      title: '架构层级',
      titleHelp: {
        content:
          '架构层级体系：蓝真相源＝控制面权威元数据（唯一事实来源）；真动态＝Lua 运行时热生效；半动态＝Reload 平滑重载；静态层＝需重启节点生效。',
      },
      minWidth: 200,
    },
    { field: 'centerName', title: '所属中心', width: 130 },
    { field: 'activeVersion', title: '活动版本', width: 100 },
    { field: 'osInfo', minWidth: 200, showOverflow: true, title: '系统信息' },
    { field: 'controlEndpoint', minWidth: 200, showOverflow: true, title: '通信端点' },
    {
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      title: '操作',
      width: 300,
    },
  ],
  height: 'auto',
  pagerConfig: {},
  proxyConfig: {
    ajax: {
      query: async ({ page }) => {
        return await listNodesApi({
          centerId: centerId.value === 'all' ? undefined : centerId.value,
          keyword: keyword.value || undefined,
          page: page.currentPage,
          pageSize: page.pageSize,
          status: status.value,
        });
      },
    },
  },
  toolbarConfig: { custom: true, refresh: true, zoom: true },
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions });

const [NodeForm, nodeFormApi] = useVbenModal({
  connectedComponent: NodeFormModal,
});
const [StatusModal, statusApi] = useVbenModal({
  connectedComponent: NodeStatusModal,
});
const [AgentBootstrap, agentBootstrapApi] = useVbenModal({ connectedComponent: AgentBootstrapModal });
const [MetricsDrawer, metricsDrawerApi] = useVbenDrawer({
  connectedComponent: NodeMetricsDrawer,
});
const [LatencyDrawer, latencyDrawerApi] = useVbenDrawer({
  connectedComponent: NodeLatencyDrawer,
});

function onCreate() {
  quickBootstrapAfterCreate.value = false;
  nodeFormApi.setData({ record: undefined }).open();
}

function onQuickBootstrap() {
  quickBootstrapAfterCreate.value = true;
  nodeFormApi.setData({ record: undefined }).open();
}

function onNodeFormSuccess(record?: NodeItem) {
  refresh();
  if (quickBootstrapAfterCreate.value && record) {
    agentBootstrapApi.setData({ record }).open();
  }
  quickBootstrapAfterCreate.value = false;
}

function onEdit(row: NodeItem) {
  nodeFormApi.setData({ record: row }).open();
}

function onAgentBootstrap(row: NodeItem) {
  agentBootstrapApi.setData({ record: row }).open();
}

function onMetrics(row: NodeItem) {
  metricsDrawerApi.setData({ record: row }).open();
}

function onLatency(row: NodeItem) {
  latencyDrawerApi.setData({ record: row }).open();
}

function onLogs(row: NodeItem) {
  router.push({ path: '/log', query: { target: `node:${row.id}` } });
}

function onStatusClick(row: NodeItem) {
  statusApi.setData({ record: row }).open();
}

function onDelete(row: NodeItem) {
  AntModal.confirm({
    content: '删除前请确认节点已下线，删除后不可恢复。',
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteNodeApi(row.id);
        refresh();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除节点「${row.name}」？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="按中心登记与管理网关数据面节点实例。"
    title="节点实例管理"
  >
    <div class="flex h-full min-h-0 flex-col gap-4">
      <div class="flex flex-col gap-3 2xl:flex-row 2xl:items-center 2xl:justify-between">
        <div class="flex flex-wrap items-center gap-2">
          <Select v-model:value="centerId" :options="centerOptions" option-filter-prop="label" show-search style="width: 160px" @change="onFilterChange" />
          <Select v-model:value="status" :options="STATUS_OPTIONS" allow-clear placeholder="运行状态" style="width: 144px" @change="onFilterChange" />
          <Input v-model:value="searchKeyword" allow-clear placeholder="节点名称或主机地址" style="width: 220px" @press-enter="onSearch" />
          <Button type="primary" @click="onSearch">搜索</Button>
          <Button @click="onReset">重置</Button>
        </div>
        <div class="flex items-center justify-end gap-3">
          <Button @click="refresh">刷新</Button>
          <Radio.Group v-model:value="viewMode" button-style="solid" aria-label="节点展示方式">
            <Radio.Button value="card">卡片视图</Radio.Button>
            <Radio.Button value="table">列表视图</Radio.Button>
          </Radio.Group>
          <Button v-access:code="'orp:config:write'" @click="onCreate">注册节点</Button>
          <Button v-access:code="'orp:node:operate'" type="primary" @click="onQuickBootstrap">快速接入</Button>
        </div>
      </div>
      <div v-if="viewMode === 'card'" class="flex min-h-0 flex-1 flex-col gap-4">
        <Spin :spinning="cardLoading" class="min-h-0 flex-1 overflow-y-auto">
          <div v-if="cardItems.length" class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            <Card v-for="node in cardItems" :key="node.id" class="h-full" size="small">
              <template #title>
                <span class="block truncate" :title="node.name">{{ node.name }}</span>
              </template>
              <template #extra>
                <span class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-xs" :class="(STATUS_META[node.status] ?? STATUS_META.running).pill">
                  <span class="h-1.5 w-1.5 rounded-full" :class="(STATUS_META[node.status] ?? STATUS_META.running).dot"></span>
                  {{ (STATUS_META[node.status] ?? STATUS_META.running).label }}
                </span>
              </template>
              <div class="space-y-3 text-sm">
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">主机地址</span><span class="break-all text-right font-medium">{{ node.host }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">所属中心</span><span class="text-right">{{ node.centerName || '-' }}</span></div>
                <div class="flex justify-between gap-3">
                  <span class="shrink-0 text-gray-500">健康状态</span>
                  <Tooltip :title="node.healthRule ?? '最近 3 次探测：全部成功为在线，部分失败为不稳定，全部失败为离线'">
                    <span class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-xs" :class="(HEALTH_META[node.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).pill">
                      <span class="h-1.5 w-1.5 rounded-full" :class="(HEALTH_META[node.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).dot"></span>
                      {{ (HEALTH_META[node.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).label }}
                    </span>
                  </Tooltip>
                </div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">响应延迟</span><span class="rounded border px-2 py-0.5 text-xs" :class="latencyClass(node.lastLatencyMs)">{{ node.lastLatencyMs != null ? `${node.lastLatencyMs} ms` : '-' }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">活动版本</span><span class="text-right">{{ node.activeVersion || '-' }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">下发权重</span><span class="text-right">W{{ node.weight ?? 10 }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">最近检查</span><span class="text-right">{{ node.lastCheckedAt || '-' }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">系统信息</span><span class="line-clamp-1 text-right" :title="node.osInfo">{{ node.osInfo || '-' }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">通信端点</span><span class="truncate text-right" :title="node.controlEndpoint">{{ node.controlEndpoint || '-' }}</span></div>
                <div class="flex flex-wrap justify-end gap-1 border-t pt-2">
                  <Button v-access:code="'orp:node:operate'" size="small" type="link" @click="onStatusClick(node)">状态</Button>
                  <Button v-access:code="'orp:read'" size="small" type="link" @click="onLatency(node)">延迟趋势</Button>
                  <Button v-access:code="'orp:read'" size="small" type="link" @click="onMetrics(node)">指标</Button>
                  <Button v-access:code="'orp:read'" size="small" type="link" @click="onLogs(node)">日志</Button>
                  <Button v-access:code="'orp:node:operate'" size="small" type="link" @click="onAgentBootstrap(node)">快速接入</Button>
                  <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(node)">编辑</Button>
                  <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(node)">删除</Button>
                </div>
              </div>
            </Card>
          </div>
          <Empty v-else-if="!cardLoading" description="暂无节点" class="mt-16" />
        </Spin>
        <div v-if="cardTotal > 0" class="flex justify-end">
          <Pagination :current="cardPage" :page-size="cardPageSize" :total="cardTotal" show-size-changer :show-total="(total: number) => `共 ${total} 条`" @change="onCardPageChange" />
        </div>
      </div>
      <Grid v-else table-title="节点列表" class="min-h-0 flex-1">
      <template #weight="{ row }">
        <span class="inline-flex items-center gap-1 rounded border px-2 py-0.5 text-xs" :class="row.weight <= 1 ? 'border-amber-200 bg-amber-50 text-amber-700' : 'border-gray-200 bg-gray-50 text-gray-600 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300'">
          W{{ row.weight ?? 10 }}
        </span>
      </template>
      <template #status="{ row }">
        <Tooltip title="点击修改运行状态">
          <button
            v-access:code="'orp:node:operate'"
            class="inline-flex cursor-pointer items-center gap-1 rounded border px-2 py-0.5 text-xs transition-shadow hover:shadow-md"
            :class="(STATUS_META[row.status] ?? STATUS_META.running).pill"
            :data-status="row.status"
            data-status-pill
            type="button"
            @click="onStatusClick(row)"
          >
            <span
              class="h-1.5 w-1.5 rounded-full"
              :class="(STATUS_META[row.status] ?? STATUS_META.running).dot"
            ></span>
            {{ (STATUS_META[row.status] ?? STATUS_META.running).label }}
          </button>
        </Tooltip>
      </template>
      <template #layer>
        <Tooltip
          title="静态层 (Restart)：节点主机启停、网络绑定等底层变更，需重启节点或守护进程后方可生效。"
        >
          <span
            class="inline-flex cursor-help items-center gap-1 text-xs text-rose-600 bg-rose-50 px-2 py-0.5 rounded border border-rose-200"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-rose-500"></span>
            静态层 (Restart)
          </span>
        </Tooltip>
      </template>
      <template #health="{ row }">
        <Tooltip
          :title="
            row.healthRule ??
            '已接入 Agent 时依据心跳和 OpenResty 进程状态；其他节点依据最近 3 次主动探测结果'
          "
        >
          <span
            class="inline-flex cursor-help items-center gap-1 rounded border px-2 py-0.5 text-xs"
            :class="(HEALTH_META[row.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).pill"
          >
            <span
              class="h-1.5 w-1.5 rounded-full"
              :class="(HEALTH_META[row.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).dot"
            ></span>
            {{ (HEALTH_META[row.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).label }}
          </span>
        </Tooltip>
      </template>
      <template #latency="{ row }">
        <span
          class="inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium"
          :class="latencyClass(row.lastLatencyMs)"
        >
          {{ row.lastLatencyMs != null ? `${row.lastLatencyMs} ms` : '-' }}
        </span>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'orp:read'" size="small" type="link" @click="onLatency(row)">延迟趋势</Button>
        <Button v-access:code="'orp:read'" size="small" type="link" @click="onMetrics(row)">指标</Button>
        <Button v-access:code="'orp:read'" size="small" type="link" @click="onLogs(row)">日志</Button>
        <Button v-access:code="'orp:node:operate'" size="small" type="link" @click="onAgentBootstrap(row)">快速接入</Button>
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">删除</Button>
      </template>
      </Grid>
    </div>
    <NodeForm @success="onNodeFormSuccess" />
    <AgentBootstrap />
    <StatusModal @success="refresh" />
    <MetricsDrawer />
    <LatencyDrawer @refresh="refresh" />
  </Page>
</template>
