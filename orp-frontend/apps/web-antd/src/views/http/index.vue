<script lang="ts" setup>
import type { HttpListener } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { Page, useVbenDrawer, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tag, Tooltip } from 'ant-design-vue';
import { useRouter } from 'vue-router';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteHttpListenerApi, listCentersApi, listHttpListenersApi } from '#/api';

import HttpDirectivesModal from './directives-modal.vue';
import HttpFormModal from './form-modal.vue';
import HttpRoutesDrawer from './routes-drawer.vue';

defineOptions({ name: 'HttpTrafficManagement' });

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
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '域名关键字' },
      fieldName: 'keyword',
      label: '域名',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<HttpListener> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'domain', minWidth: 180, title: '绑定域名' },
    { field: 'port', title: '监听端口', width: 100 },
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
    { field: 'centerName', title: '所属中心', width: 130 },
    {
      field: 'tlsCertId',
      slots: { default: 'tls' },
      title: 'TLS 证书',
      width: 150,
    },
    { field: 'routeCount', slots: { default: 'routes' }, title: '路由规则', width: 110 },
    { field: 'createdAt', title: '创建时间', width: 170 },
    {
      field: 'action',
      fixed: 'right',
      slots: { default: 'action' },
      title: '操作',
      width: 240,
    },
  ],
  height: 'auto',
  pagerConfig: {},
  proxyConfig: {
    ajax: {
      query: async ({ page }, formValues) => {
        return await listHttpListenersApi({
          centerId:
            formValues?.centerId && formValues.centerId !== 'all'
              ? Number(formValues.centerId)
              : undefined,
          keyword: formValues?.keyword,
          page: page.currentPage,
          pageSize: page.pageSize,
        });
      },
    },
  },
  toolbarConfig: { custom: true, refresh: true, zoom: true },
};

const [Grid, gridApi] = useVbenVxeGrid({ formOptions, gridOptions });

const [HttpForm, httpFormApi] = useVbenModal({
  connectedComponent: HttpFormModal,
});
const [RoutesDrawer, routesDrawerApi] = useVbenDrawer({
  connectedComponent: HttpRoutesDrawer,
});
const [HttpDirectives, httpDirectivesApi] = useVbenModal({
  connectedComponent: HttpDirectivesModal,
});

function onCreate() {
  httpFormApi.setData({ record: undefined }).open();
}

function onDirectives() {
  httpDirectivesApi.open();
}

function onEdit(row: HttpListener) {
  httpFormApi.setData({ record: row }).open();
}

function onRoutes(row: HttpListener) {
  routesDrawerApi.setData({ record: row }).open();
}

function onLogs(row: HttpListener) {
  router.push({ path: '/log', query: { target: `http:${row.id}` } });
}

function onDelete(row: HttpListener) {
  AntModal.confirm({
    content: '删除监听配置会同时移除其下所有 Location 路由规则。',
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteHttpListenerApi(row.id);
        gridApi.query();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除「${row.domain}:${row.port}」？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="按中心管理 HTTP 监听端口、域名绑定与 Location 路由规则。"
    title="HTTP 流量管理"
  >
    <Grid table-title="监听与域名列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" class="mr-2" @click="onDirectives">HTTP 块指令</Button>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新建监听</Button>
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
      <template #tls="{ row }">
        <Tag v-if="row.tlsCertId" color="green">{{ row.certName }}</Tag>
        <Tag v-else>明文</Tag>
      </template>
      <template #routes="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onRoutes(row)">
          {{ row.routeCount }} 条规则
        </Button>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onRoutes(row)">路由</Button>
        <Button size="small" type="link" @click="onLogs(row)">日志</Button>
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">删除</Button>
      </template>
    </Grid>
    <HttpForm @success="() => gridApi.query()" />
    <RoutesDrawer @success="() => gridApi.query()" />
    <HttpDirectives />
  </Page>
</template>
