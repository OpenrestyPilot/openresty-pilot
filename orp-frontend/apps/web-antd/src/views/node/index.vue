<script lang="ts" setup>
import type { NodeItem } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenDrawer, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tooltip } from 'ant-design-vue';
import { useRouter } from 'vue-router';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteNodeApi, listCentersApi, listNodesApi } from '#/api';

import NodeFormModal from './form-modal.vue';
import NodeLatencyDrawer from './latency-drawer.vue';
import NodeMetricsDrawer from './metrics-drawer.vue';
import { STATUS_META, STATUS_OPTIONS } from './status-meta';
import NodeStatusModal from './status-modal.vue';

defineOptions({ name: 'NodeManagement' });

const router = useRouter();

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

const formOptions: VbenFormProps = {
  collapsed: false,
  schema: [
    {
      component: 'ApiSelect',
      componentProps: {
        allowClear: true,
        showSearch: true,
        optionFilterProp: 'label',
        api: async () => {
          const res = await listCentersApi({ page: 1, pageSize: 200 });
          return [{ label: '全部中心', value: 'all' }, ...res.items.map((item) => ({
            label: item.name,
            value: item.id,
          }))];
        },
        placeholder: '按中心筛选',
      },
      defaultValue: 'all',
      fieldName: 'centerId',
      label: '所属中心',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        showSearch: true,
        optionFilterProp: 'label',
        options: STATUS_OPTIONS,
        placeholder: '按状态筛选',
      },
      fieldName: 'status',
      label: '运行状态',
    },
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '节点名称或主机地址' },
      fieldName: 'keyword',
      label: '关键字',
    },
  ],
  submitOnChange: true,
};

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
          '本地 Docker 节点每 10 秒探测一次：最近 3 次全部成功为在线，部分失败为不稳定，全部失败为离线；未接入探测通道的节点显示未采集。',
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
      query: async ({ page }, formValues) => {
        return await listNodesApi({
          centerId:
            formValues?.centerId && formValues.centerId !== 'all'
              ? Number(formValues.centerId)
              : undefined,
          keyword: formValues?.keyword,
          page: page.currentPage,
          pageSize: page.pageSize,
          status: formValues?.status,
        });
      },
    },
  },
  toolbarConfig: { custom: true, refresh: true, zoom: true },
};

const [Grid, gridApi] = useVbenVxeGrid({ formOptions, gridOptions });

const [NodeForm, nodeFormApi] = useVbenModal({
  connectedComponent: NodeFormModal,
});
const [StatusModal, statusApi] = useVbenModal({
  connectedComponent: NodeStatusModal,
});
const [MetricsDrawer, metricsDrawerApi] = useVbenDrawer({
  connectedComponent: NodeMetricsDrawer,
});
const [LatencyDrawer, latencyDrawerApi] = useVbenDrawer({
  connectedComponent: NodeLatencyDrawer,
});

function onCreate() {
  nodeFormApi.setData({ record: undefined }).open();
}

function onEdit(row: NodeItem) {
  nodeFormApi.setData({ record: row }).open();
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
        gridApi.query();
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
    description="按中心登记与管理 OpenResty 数据面节点实例。"
    title="节点实例管理"
  >
    <Grid table-title="节点列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">注册节点</Button>
      </template>
      <template #weight="{ row }">
        <span :class="`inline-flex items-center gap-1 rounded border px-2 py-0.5 text-xs ${row.weight <= 1 ? 'border-amber-200 bg-amber-50 text-amber-700' : 'border-gray-200 bg-gray-50 text-gray-600 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-300'}`">
          W{{ row.weight ?? 10 }}
        </span>
      </template>
      <template #status="{ row }">
        <Tooltip title="点击修改运行状态">
          <button
            v-access:code="'orp:node:operate'"
            :class="`inline-flex cursor-pointer items-center gap-1 rounded border px-2 py-0.5 text-xs transition-shadow hover:shadow-md ${(STATUS_META[row.status] ?? STATUS_META.running).pill}`"
            :data-status="row.status"
            data-status-pill
            type="button"
            @click="onStatusClick(row)"
          >
            <span
              :class="`h-1.5 w-1.5 rounded-full ${(STATUS_META[row.status] ?? STATUS_META.running).dot}`"
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
            '最近 3 次探测：全部成功为在线，部分失败为不稳定，全部失败为离线'
          "
        >
          <span
            :class="`inline-flex cursor-help items-center gap-1 rounded border px-2 py-0.5 text-xs ${(HEALTH_META[row.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).pill}`"
          >
            <span
              :class="`h-1.5 w-1.5 rounded-full ${(HEALTH_META[row.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).dot}`"
            ></span>
            {{ (HEALTH_META[row.healthStatus ?? 'unknown'] ?? HEALTH_META_UNKNOWN).label }}
          </span>
        </Tooltip>
      </template>
      <template #latency="{ row }">
        <span
          :class="`inline-flex items-center rounded border px-2 py-0.5 text-xs font-medium ${latencyClass(row.lastLatencyMs)}`"
        >
          {{ row.lastLatencyMs != null ? `${row.lastLatencyMs} ms` : '-' }}
        </span>
      </template>
      <template #action="{ row }">
        <Button size="small" type="link" @click="onLatency(row)">延迟趋势</Button>
        <Button size="small" type="link" @click="onMetrics(row)">指标</Button>
        <Button size="small" type="link" @click="onLogs(row)">日志</Button>
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">删除</Button>
      </template>
    </Grid>
    <NodeForm @success="() => gridApi.query()" />
    <StatusModal @success="() => gridApi.query()" />
    <MetricsDrawer />
    <LatencyDrawer @refresh="() => gridApi.query()" />
  </Page>
</template>
