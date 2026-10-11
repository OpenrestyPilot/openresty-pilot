<script lang="ts" setup>
import type { RbacRoleItem } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenDrawer, useVbenModal } from '@vben/common-ui';

import { Button, message, Popconfirm, Tag, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteRbacRoleApi, listRbacRolesApi } from '#/api';

import RoleFormModal from './role-form-modal.vue';
import RolePermDrawer from './role-perm-drawer.vue';

defineOptions({ name: 'SystemRoleManagement' });

const formOptions: VbenFormProps = {
  collapsed: false,
  schema: [
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '角色名称或编码' },
      fieldName: 'keyword',
      label: '关键字',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: '启用', value: 'enabled' },
          { label: '停用', value: 'disabled' },
        ],
        placeholder: '按状态筛选',
      },
      fieldName: 'status',
      label: '状态',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<RbacRoleItem> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'name', minWidth: 130, title: '角色名称' },
    { field: 'code', minWidth: 130, title: '角色编码', },
    {
      field: 'builtin',
      slots: { default: 'builtin' },
      title: '内置',
      width: 80,
    },
    {
      field: 'status',
      slots: { default: 'status' },
      title: '状态',
      width: 90,
    },
    { field: 'userCount', slots: { default: 'userCount' }, title: '绑定用户', width: 100 },
    { field: 'permissionKeys', slots: { default: 'perms' }, title: '权限数', width: 90 },
    { field: 'centerIds', slots: { default: 'centerScope' }, title: '中心范围', width: 130 },
    { field: 'description', minWidth: 220, showOverflow: true, title: '角色描述' },
    { field: 'createdAt', title: '创建时间', width: 170 },
    {
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      title: '操作',
      width: 250,
    },
  ],
  height: 'auto',
  pagerConfig: {},
  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        const res = await listRbacRolesApi({
          ...formValues,
          page: page.currentPage,
          pageSize: page.pageSize,
        });
        return { items: res.items, total: res.total };
      },
    },
  },
};

const [BasicGrid, gridApi] = useVbenVxeGrid({ formOptions, gridOptions });

const [RoleFormView, roleFormApi] = useVbenModal({
  connectedComponent: RoleFormModal,
});

const [RolePermView, rolePermApi] = useVbenDrawer({
  connectedComponent: RolePermDrawer,
});

function onAdd() {
  roleFormApi.setData({ record: null }).open();
}

function onEdit(record: RbacRoleItem) {
  roleFormApi.setData({ record }).open();
}

function onAssign(record: RbacRoleItem) {
  rolePermApi.setData({ record }).open();
}

async function onDelete(record: RbacRoleItem) {
  await deleteRbacRoleApi(record.id);
  message.success(`角色 ${record.name} 已删除`);
  gridApi.query();
}

function onFormSuccess() {
  gridApi.query();
}
</script>

<template>
  <Page
    title="角色管理"
    description="角色职责通过权限项明确区分。平台管理员、配置编辑员、发布操作员和审计只读为固定内置角色；需要不同组合时请创建自定义角色。"
  >
    <BasicGrid table-title="角色列表">
      <template #toolbar-actions>
        <Button v-access:code="'*'" type="primary" @click="onAdd">新增角色</Button>
      </template>
      <template #builtin="{ row }">
        <Tag v-if="row.builtin" class="!m-0" color="purple">内置</Tag>
        <span v-else class="text-gray-400">—</span>
      </template>
      <template #status="{ row }">
        <span :class="row.status === 'enabled' ? 'inline-flex items-center gap-1.5 rounded border border-emerald-200 bg-emerald-50 px-2 py-0.5 text-xs text-emerald-700' : 'inline-flex items-center gap-1.5 rounded border border-rose-200 bg-rose-50 px-2 py-0.5 text-xs text-rose-700'">
          <span :class="row.status === 'enabled' ? 'h-1.5 w-1.5 rounded-full bg-emerald-500' : 'h-1.5 w-1.5 rounded-full bg-rose-500'" />
          {{ row.status === 'enabled' ? '启用' : '停用' }}
        </span>
      </template>
      <template #userCount="{ row }">
        <Tag class="!m-0" :color="row.userCount ? 'blue' : 'default'">{{ row.userCount ?? 0 }} 人</Tag>
      </template>
      <template #perms="{ row }">
        <Tag class="!m-0" color="cyan">{{ row.permissionKeys.length }} 项</Tag>
      </template>
      <template #centerScope="{ row }">
        <Tag v-if="row.centerIds.includes('*')" class="!m-0" color="purple">全部中心</Tag>
        <Tag v-else-if="row.centerIds.length" class="!m-0" color="blue">{{ row.centerIds.length }} 个中心</Tag>
        <span v-else class="text-gray-400">全局配置</span>
      </template>
      <template #action="{ row }">
        <Tooltip v-if="row.builtin" title="内置角色职责固定，请创建自定义角色后分配权限">
          <Button disabled size="small" type="link">分配权限</Button>
        </Tooltip>
        <Button v-else v-access:code="'*'" size="small" type="link" @click="onAssign(row)">分配权限</Button>
        <Tooltip v-if="row.builtin" title="内置角色信息固定">
          <Button disabled size="small" type="link">编辑</Button>
        </Tooltip>
        <Button v-else v-access:code="'*'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Popconfirm v-if="!row.builtin" v-access:code="'*'" :title="`确认删除角色 ${row.name}？`" @confirm="onDelete(row)">
          <Button v-access:code="'*'" danger size="small" type="link">删除</Button>
        </Popconfirm>
        <Tooltip v-else title="内置角色不可删除">
          <Button disabled size="small" type="link">删除</Button>
        </Tooltip>
      </template>
    </BasicGrid>
    <RoleFormView @success="onFormSuccess" />
    <RolePermView @success="onFormSuccess" />
  </Page>
</template>
