<script lang="ts" setup>
import type { StreamService } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tag, Tooltip } from 'ant-design-vue';
import { useRouter } from 'vue-router';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteStreamServiceApi, listCentersApi, listStreamServicesApi } from '#/api';

import StreamDirectivesModal from './directives-modal.vue';
import StreamFormModal from './form-modal.vue';

defineOptions({ name: 'StreamTrafficManagement' });

const router = useRouter();

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
        options: [
          { label: 'TCP', value: 'tcp' },
          { label: 'UDP', value: 'udp' },
        ],
        placeholder: '按协议筛选',
      },
      fieldName: 'protocol',
      label: '监听协议',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<StreamService> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    {
      field: 'protocol',
      slots: { default: 'protocol' },
      title: '协议',
      width: 80,
    },
    {
      field: 'listen',
      slots: { default: 'listen' },
      title: '监听地址',
      width: 150,
    },
    {
      field: 'layer',
      slots: { default: 'layer' },
      title: '架构层级',
      titleHelp: {
        content:
          '架构层级体系：蓝真相源＝控制面权威元数据（唯一事实来源）；真动态＝Lua 运行时热生效；半动态＝Reload 平滑重载；静态层＝需重启节点生效。',
      },
      width: 140,
    },
    { field: 'description', minWidth: 180, showOverflow: true, title: '服务描述' },
    {
      field: 'backends',
      slots: { default: 'backends' },
      title: '后端地址',
      minWidth: 240,
    },
    { field: 'centerName', title: '所属中心', width: 130 },
    { field: 'createdAt', title: '创建时间', width: 170 },
    {
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      title: '操作',
      width: 200,
    },
  ],
  height: 'auto',
  pagerConfig: {},
  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        return await listStreamServicesApi({
          centerId:
            formValues?.centerId && formValues.centerId !== 'all'
              ? Number(formValues.centerId)
            : undefined,
          page: page.currentPage,
          pageSize: page.pageSize,
          protocol: formValues?.protocol,
        });
      },
    },
  },
  toolbarConfig: { custom: true, refresh: true, zoom: true },
};

const [Grid, gridApi] = useVbenVxeGrid({ formOptions, gridOptions });

const [StreamForm, streamFormApi] = useVbenModal({
  connectedComponent: StreamFormModal,
});
const [StreamDirectives, streamDirectivesApi] = useVbenModal({
  connectedComponent: StreamDirectivesModal,
});

function onCreate() {
  streamFormApi.setData({ record: undefined }).open();
}

function onEdit(row: StreamService) {
  streamFormApi.setData({ record: row }).open();
}

function onLogs(row: StreamService) {
  router.push({ path: '/log', query: { target: `stream:${row.id}` } });
}

function onDirectives() {
  streamDirectivesApi.open();
}

function onDelete(row: StreamService) {
  AntModal.confirm({
    content: '删除后该四层代理服务立即停止转发。',
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteStreamServiceApi(row.id);
        gridApi.query();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除 ${row.protocol.toUpperCase()}/${row.listenPort} 服务？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="按中心管理 TCP/UDP 四层代理服务与后端转发地址。"
    title="Stream 流量管理"
  >
    <Grid table-title="Stream 服务列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" class="mr-2" @click="onDirectives">Stream 块配置</Button>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新建服务</Button>
      </template>
      <template #protocol="{ row }">
        <Tag :color="row.protocol === 'tcp' ? 'blue' : 'purple'">
          {{ row.protocol.toUpperCase() }}
        </Tag>
      </template>
      <template #listen="{ row }">
        {{ row.listenAddress }}:{{ row.listenPort }}
      </template>
      <template #layer>
        <Tooltip
          title="半动态 (Reload)：配置变更经 nginx -s reload 平滑重载生效，不中断现有连接。"
        >
          <span
            class="inline-flex cursor-help items-center gap-1 text-xs text-amber-600 bg-amber-50 px-2 py-0.5 rounded border border-amber-200"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>
            半动态 (Reload)
          </span>
        </Tooltip>
      </template>
      <template #backends="{ row }">
        <Tooltip :title="row.backends.join('\n')">
          <span class="cursor-help text-sm">{{ row.backends.join('、') }}</span>
        </Tooltip>
      </template>
      <template #action="{ row }">
        <Button size="small" type="link" @click="onLogs(row)">日志</Button>
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">删除</Button>
      </template>
    </Grid>
    <StreamForm @success="() => gridApi.query()" />
    <StreamDirectives />
  </Page>
</template>
