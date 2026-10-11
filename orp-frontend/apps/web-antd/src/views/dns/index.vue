<script lang="ts" setup>
import type { DnsResolver } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteDnsResolverApi, listCentersApi, listDnsResolversApi } from '#/api';

import DnsFormModal from './form-modal.vue';

defineOptions({ name: 'DnsResolverManagement' });

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
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<DnsResolver> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'centerName', minWidth: 160, title: '所属中心' },
    {
      field: 'address',
      minWidth: 220,
      slots: { default: 'address' },
      title: '解析服务器地址',
    },
    { field: 'port', title: '端口', width: 90 },
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
      field: 'timeoutSec',
      slots: { default: 'timeout' },
      title: '超时时间',
      width: 110,
    },
    {
      field: 'cacheTtlSec',
      slots: { default: 'cache' },
      title: '有效缓存时间',
      width: 130,
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
        return await listDnsResolversApi({
          centerId:
            formValues?.centerId && formValues.centerId !== 'all'
              ? Number(formValues.centerId)
              : undefined,
          page: page.currentPage,
          pageSize: page.pageSize,
        });
      },
    },
  },
  toolbarConfig: { custom: true, refresh: true, zoom: true },
};

const [Grid, gridApi] = useVbenVxeGrid({ formOptions, gridOptions });

const [DnsForm, dnsFormApi] = useVbenModal({
  connectedComponent: DnsFormModal,
});

function onCreate() {
  dnsFormApi.setData({ record: undefined }).open();
}

function onEdit(row: DnsResolver) {
  dnsFormApi.setData({ record: row }).open();
}

function onDelete(row: DnsResolver) {
  AntModal.confirm({
    content: '删除后该解析服务器不再参与域名解析。',
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteDnsResolverApi(row.id);
        gridApi.query();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除解析器 ${row.address}:${row.port}？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="按中心配置 DNS 域名解析服务器、超时与缓存策略。"
    title="DNS 解析器管理"
  >
    <Grid table-title="DNS Resolver 列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新增解析器</Button>
      </template>
      <template #layer>
        <Tooltip
          title="真动态 (Lua Resolver)：解析器配置由 Lua 运行时动态加载，零 Reload、零闪断，变更即时生效。"
        >
          <span
            class="inline-flex cursor-help items-center gap-1 text-xs text-emerald-600 bg-emerald-50 px-2 py-0.5 rounded border border-emerald-200"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>
            真动态 (Lua Resolver)
          </span>
        </Tooltip>
      </template>
      <template #address="{ row }">
        {{ row.address }}:{{ row.port }}
      </template>
      <template #timeout="{ row }">{{ row.timeoutSec }} 秒</template>
      <template #cache="{ row }">{{ row.cacheTtlSec }} 秒</template>
      <template #action="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">删除</Button>
      </template>
    </Grid>
    <DnsForm @success="() => gridApi.query()" />
  </Page>
</template>
