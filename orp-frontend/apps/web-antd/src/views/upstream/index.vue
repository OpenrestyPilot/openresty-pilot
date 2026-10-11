<script lang="ts" setup>
import type { UpstreamGroup } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tag, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteUpstreamGroupApi, listCentersApi, listUpstreamGroupsApi } from '#/api';

import UpstreamFormModal from './form-modal.vue';

defineOptions({ name: 'UpstreamGroupManagement' });

const LB_TEXT: Record<string, string> = {
  consistent_hash: '一致性哈希',
  hash: '通用哈希',
  ip_hash: 'IP 哈希',
  least_conn: '最少连接',
  round_robin: '轮询',
};

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
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '名称 / 描述 / 标签' },
      fieldName: 'keyword',
      label: '关键词',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<UpstreamGroup> = {
  // 单元格内容自适应行高：关闭全局单行省略截断，后端节点等密集列自动换行完整展示
  showOverflow: false,
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'centerName', title: '所属中心', width: 120 },
    {
      field: 'name',
      slots: { default: 'name' },
      title: '上游组名称',
      width: 160,
    },
    {
      field: 'lbPolicy',
      slots: { default: 'lb' },
      title: '负载均衡策略',
      width: 120,
    },
    {
      field: 'nodes',
      slots: { default: 'nodes' },
      title: '后端节点',
      minWidth: 320,
    },
    {
      field: 'healthCheck',
      slots: { default: 'health' },
      title: '健康检查',
      width: 160,
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
    {
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      title: '操作',
      width: 150,
    },
  ],
  height: 'auto',
  minHeight: 480,
  pagerConfig: {},
  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        return await listUpstreamGroupsApi({
          centerId:
            formValues?.centerId && formValues.centerId !== 'all'
              ? Number(formValues.centerId)
              : undefined,
          keyword: formValues?.keyword || undefined,
          page: page.currentPage,
          pageSize: page.pageSize,
        });
      },
    },
  },
  toolbarConfig: { custom: true, refresh: true, zoom: true },
};

const [Grid, gridApi] = useVbenVxeGrid({ formOptions, gridOptions });

const [UpstreamForm, upstreamFormApi] = useVbenModal({
  connectedComponent: UpstreamFormModal,
});

function onCreate() {
  upstreamFormApi.setData({ record: undefined }).open();
}

function onEdit(row: UpstreamGroup) {
  upstreamFormApi.setData({ record: row }).open();
}

function onDelete(row: UpstreamGroup) {
  const referenced = row.referenced ?? [];
  AntModal.confirm({
    content: referenced.length > 0
      ? `该上游组正被引用：${referenced.join('、')}。删除前需先在对应监听/服务中解除引用。`
      : '删除后该上游服务器组不再参与流量转发。',
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteUpstreamGroupApi(row.id);
        gridApi.query();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除上游组 ${row.name}？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="按中心维护上游服务器组：负载均衡策略、后端节点与健康检查。"
    title="上游服务器组管理"
  >
    <Grid table-title="Upstream 列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新增上游组</Button>
      </template>
      <template #layer>
        <Tooltip
          title="蓝真相源 (ORP CMDB)：ORP 控制面 CMDB 为上游服务拓扑的唯一权威数据源（Single Source of Truth），集中登记后端节点、权重与健康检查，数据面网关据此同步，禁止配置漂移。"
        >
          <span
            class="inline-flex cursor-help items-center gap-1 text-xs text-blue-600 bg-blue-50 px-2 py-0.5 rounded border border-blue-200"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-blue-500"></span>
            蓝真相源 (ORP CMDB)
          </span>
        </Tooltip>
      </template>
      <template #name="{ row }">
        <div class="flex flex-col">
          <span class="font-medium">{{ row.name }}</span>
          <span v-if="row.description" class="text-xs text-gray-400 break-words">{{ row.description }}</span>
          <div v-if="row.directives && row.directives.length > 0" class="mt-1 flex flex-wrap gap-1">
            <Tooltip :title="row.directives.map((d: any) => `${d.name} ${d.value}`).join('; ')">
              <Tag color="purple" class="!m-0 text-[11px] cursor-help">
                块指令 ({{ row.directives.length }})
              </Tag>
            </Tooltip>
          </div>
        </div>
      </template>
      <template #lb="{ row }">
        <Tag color="blue">{{ LB_TEXT[row.lbPolicy] ?? row.lbPolicy }}</Tag>
      </template>
      <template #nodes="{ row }">
        <div class="flex flex-wrap gap-1">
          <Tooltip
            v-for="node in row.nodes"
            :key="`${node.host}:${node.port}`"
            :title="`权重 w${node.weight} · max_fails ${node.maxFails} · fail_timeout ${node.failTimeoutSec}s${node.slowStartSec ? ` · 慢启动 ${node.slowStartSec}s` : ''}`"
          >
            <Tag :color="node.backup ? 'orange' : 'green'">
              {{ node.host }}:{{ node.port }}
              <span class="text-xs opacity-70">w{{ node.weight }}</span>
            </Tag>
          </Tooltip>
        </div>
      </template>
      <template #health="{ row }">
        <div class="flex flex-col text-xs">
          <Tag v-if="row.healthCheck.type === 'none'" color="default">被动检查</Tag>
          <template v-else>
            <Tag :color="row.healthCheck.type === 'http' ? 'green' : 'blue'">
              主动 {{ row.healthCheck.type === 'http' ? 'HTTP' : 'TCP' }}
            </Tag>
            <span class="text-gray-400 mt-0.5">
              间隔 {{ row.healthCheck.intervalSec }}s
              <template v-if="row.healthCheck.type === 'http'">
                · {{ row.healthCheck.path }} · {{ row.healthCheck.expectedStatus.join('/') }}
              </template>
            </span>
          </template>
        </div>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">删除</Button>
      </template>
    </Grid>
    <UpstreamForm @success="() => gridApi.query()" />
  </Page>
</template>
