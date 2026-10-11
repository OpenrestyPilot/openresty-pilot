<script lang="ts" setup>
import type { RbacUserItem } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { ref } from 'vue';

import { Page, useVbenModal } from '@vben/common-ui';

import { Button, message, Popconfirm, Tag, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteRbacUserApi, getUserInfoApi, listRbacRolesApi, listRbacUsersApi, resetRbacUserPasswordApi } from '#/api';

import UserFormModal from './user-form-modal.vue';

defineOptions({ name: 'SystemUserManagement' });

const STATUS_META: Record<string, { color: string; label: string; pill: string }> = {
  enabled: { color: 'green', label: '启用', pill: 'bg-emerald-50 border-emerald-200 text-emerald-700' },
  disabled: { color: 'red', label: '禁用', pill: 'bg-rose-50 border-rose-200 text-rose-700' },
};

/** 当前登录账号（不可禁用/删除自身） */
const currentUsername = ref('');

const formOptions: VbenFormProps = {
  collapsed: false,
  schema: [
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '用户名 / 姓名 / 邮箱 / 手机号' },
      fieldName: 'keyword',
      label: '关键字',
    },
    {
      component: 'ApiSelect',
      componentProps: {
        allowClear: true,
        api: async () => {
          const res = await listRbacRolesApi({ page: 1, pageSize: 100 });
          return [{ label: '全部角色', value: 'all' }, ...res.items.map((r) => ({ label: r.name, value: r.id }))];
        },
        placeholder: '按角色筛选',
      },
      defaultValue: 'all',
      fieldName: 'roleId',
      label: '所属角色',
    },
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: [
          { label: '启用', value: 'enabled' },
          { label: '禁用', value: 'disabled' },
        ],
        placeholder: '按状态筛选',
      },
      fieldName: 'status',
      label: '账号状态',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<RbacUserItem> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'username', minWidth: 130, title: '用户名' },
    { field: 'realName', title: '姓名', width: 110 },
    {
      field: 'roleNames',
      minWidth: 180,
      slots: { default: 'roles' },
      title: '所属角色',
    },
    {
      field: 'status',
      slots: { default: 'status' },
      title: '状态',
      width: 90,
    },
    { field: 'email', minWidth: 190, showOverflow: true, title: '邮箱' },
    { field: 'phone', title: '手机号', width: 130 },
    { field: 'lastLoginAt', slots: { default: 'lastLogin' }, title: '最近登录', width: 170 },
    { field: 'createdAt', title: '创建时间', width: 170 },
    {
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      title: '操作',
      width: 260,
    },
  ],
  height: 'auto',
  pagerConfig: {},
  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        const res = await listRbacUsersApi({
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

const [UserFormView, userFormApi] = useVbenModal({
  connectedComponent: UserFormModal,
});

function onAdd() {
  userFormApi.setData({ record: null }).open();
}

function onEdit(record: RbacUserItem) {
  userFormApi.setData({ record }).open();
}

async function onDelete(record: RbacUserItem) {
  await deleteRbacUserApi(record.id);
  message.success(`用户 ${record.username} 已删除`);
  gridApi.query();
}

async function onResetPassword(record: RbacUserItem) {
  const res = await resetRbacUserPasswordApi(record.id);
  message.success(`已重置用户 ${record.realName}（${record.username}）的密码为初始密码 ${res.resetTo}`);
}

function onFormSuccess() {
  gridApi.query();
}

/** 页面进入时记录当前登录账号 */
void getUserInfoApi()
  .then((user) => {
    currentUsername.value = user.username ?? '';
  })
  .catch(() => {});
</script>

<template>
  <Page
    title="用户管理"
    description="维护平台登录账号、角色绑定与账号状态；角色决定账号可访问的菜单与可执行的操作。"
  >
    <BasicGrid table-title="账号列表">
      <template #toolbar-actions>
        <Button v-access:code="'*'" type="primary" @click="onAdd">新增用户</Button>
      </template>
      <template #roles="{ row }">
        <Tag
          v-for="name in row.roleNames"
          :key="name"
          class="!m-0"
          color="geekblue"
        >
          {{ name }}
        </Tag>
      </template>
      <template #status="{ row }">
        <span :class="`inline-flex items-center gap-1.5 rounded border px-2 py-0.5 text-xs ${STATUS_META[row.status]?.pill ?? ''}`">
          <span :class="row.status === 'enabled' ? 'h-1.5 w-1.5 rounded-full bg-emerald-500' : 'h-1.5 w-1.5 rounded-full bg-rose-500'" />
          {{ STATUS_META[row.status]?.label ?? row.status }}
        </span>
      </template>
      <template #lastLogin="{ row }">
        <span v-if="row.lastLoginAt">{{ row.lastLoginAt }}</span>
        <span v-else class="text-gray-400">从未登录</span>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'*'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Tooltip v-if="row.username === currentUsername" title="当前登录账号不可禁用或删除">
          <Button disabled size="small" type="link">重置密码</Button>
          <Button disabled size="small" type="link">删除</Button>
        </Tooltip>
        <template v-else>
          <Popconfirm v-access:code="'*'" title="确认重置该账号密码为初始密码？" @confirm="onResetPassword(row)">
            <Button v-access:code="'*'" size="small" type="link">重置密码</Button>
          </Popconfirm>
          <Popconfirm v-access:code="'*'" :title="`确认删除用户 ${row.realName}（${row.username}）？`" @confirm="onDelete(row)">
            <Button v-access:code="'*'" danger size="small" type="link">删除</Button>
          </Popconfirm>
        </template>
      </template>
    </BasicGrid>
    <UserFormView @success="onFormSuccess" />
  </Page>
</template>
