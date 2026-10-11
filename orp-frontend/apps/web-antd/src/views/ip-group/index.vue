<script lang="ts" setup>
import type { OrpIpGroup } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tag, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteIpGroupApi, listIpGroupsApi } from '#/api';

import IpGroupFormModal from './form-modal.vue';

defineOptions({ name: 'IpGroupManagement' });

const formOptions: VbenFormProps = {
  collapsed: false,
  schema: [
    {
      component: 'Input',
      componentProps: {
        allowClear: true,
        placeholder: '按组名称关键字检索',
      },
      fieldName: 'keyword',
      label: '组名称',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<OrpIpGroup> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'name', minWidth: 180, title: '组名称' },
    { field: 'description', minWidth: 220, title: '描述' },
    {
      field: 'members',
      slots: { default: 'count' },
      title: '成员数量',
      width: 100,
    },
    {
      field: 'members',
      minWidth: 340,
      slots: { default: 'members' },
      title: '成员 IP / CIDR',
    },
    { field: 'updatedAt', width: 170, title: '更新时间' },
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
        return await listIpGroupsApi({
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

const [IpGroupForm, ipGroupFormApi] = useVbenModal({
  connectedComponent: IpGroupFormModal,
});

function onCreate() {
  ipGroupFormApi.setData({ record: undefined }).open();
}

function onEdit(row: OrpIpGroup) {
  ipGroupFormApi.setData({ record: row }).open();
}

function onDelete(row: OrpIpGroup) {
  AntModal.confirm({
    content:
      '删除后已有策略中导入过的 IP 项不受影响，仅无法再从该组继续导入。',
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteIpGroupApi(row.id);
        gridApi.query();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除 IP 组「${row.name}」？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="统一维护常用 IP / CIDR 网段分组，供 HTTP Server、Stream Server 与 Location 路由的 IP 访问控制策略快捷导入复用。"
    title="IP 组管理"
  >
    <Grid table-title="IP 组列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新增 IP 组</Button>
      </template>
      <template #count="{ row }">{{ row.members.length }} 项</template>
      <template #members="{ row }">
        <Tooltip
          v-if="row.members.length > 4"
          :title="row.members.join('、')"
        >
          <div class="flex flex-wrap items-center gap-1">
            <Tag v-for="item in row.members.slice(0, 4)" :key="item" class="text-xs">
              {{ item }}
            </Tag>
            <Tag class="text-xs">+{{ row.members.length - 4 }}</Tag>
          </div>
        </Tooltip>
        <div v-else class="flex flex-wrap items-center gap-1">
          <Tag v-for="item in row.members" :key="item" class="text-xs">
            {{ item }}
          </Tag>
        </div>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">
          删除
        </Button>
      </template>
    </Grid>
    <IpGroupForm @success="() => gridApi.query()" />
  </Page>
</template>
