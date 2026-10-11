<script lang="ts" setup>
import type { Center } from '#/api';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteCenterApi, listCentersApi } from '#/api';

import CenterFormModal from './form-modal.vue';

defineOptions({ name: 'CenterManagement' });

const gridOptions: VxeGridProps<Center> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'name', minWidth: 140, title: '中心名称' },
    { field: 'code', title: '标识码', width: 130 },
    {
      field: 'layer',
      title: '架构层级',
      titleHelp: {
        content:
          '架构层级体系：蓝真相源＝控制面权威元数据（唯一事实来源）；真动态＝Lua 运行时热生效；半动态＝Reload 平滑重载；静态层＝需重启节点生效。',
      },
      width: 140,
      slots: { default: 'layer' },
    },
    { field: 'description', minWidth: 200, showOverflow: true, title: '备注描述' },
    { field: 'createdAt', title: '创建时间', width: 170 },
    { field: 'updatedAt', title: '更新时间', width: 170 },
    {
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      title: '操作',
      width: 150,
    },
  ],
  height: 'auto',
  pagerConfig: {},
  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        return await listCentersApi({
          keyword: formValues?.keyword,
          page: page.currentPage,
          pageSize: page.pageSize,
        });
      },
    },
  },
  toolbarConfig: {
    custom: true,
    refresh: true,
    zoom: true,
  },
};

const [Grid, gridApi] = useVbenVxeGrid({ gridOptions });

const [CenterForm, centerFormApi] = useVbenModal({
  connectedComponent: CenterFormModal,
});

function onCreate() {
  centerFormApi.setData({ record: undefined }).open();
}

function onEdit(row: Center) {
  centerFormApi.setData({ record: row }).open();
}

function onDelete(row: Center) {
  AntModal.confirm({
    content: `删除后不可恢复，且要求中心下无任何关联资源。`,
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteCenterApi(row.id);
        gridApi.query();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除中心「${row.name}」？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="以中心为配置与管理边界，登记节点实例与流量资源。"
    title="中心管理"
  >
    <Grid table-title="中心列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新建中心</Button>
      </template>
      <template #layer>
        <Tooltip
          title="配置真相源：中心与全量资源元数据的唯一权威数据源（Single Source of Truth），所有配置变更以此为准，避免配置漂移。"
        >
          <span
            class="inline-flex cursor-help items-center gap-1 text-xs text-blue-600 bg-blue-50 px-2 py-0.5 rounded border border-blue-200"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-blue-500"></span>
            配置真相源
          </span>
        </Tooltip>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">
          删除
        </Button>
      </template>
    </Grid>
    <CenterForm @success="() => gridApi.query()" />
  </Page>
</template>
