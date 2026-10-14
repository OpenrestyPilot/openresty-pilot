<script lang="ts" setup>
import type { VxeGridProps } from '#/adapter/vxe-table';
import type { Center } from '#/api';

import { onMounted, ref, watch } from 'vue';

import { Page, useVbenModal } from '@vben/common-ui';

import { Modal as AntModal, Button, Card, Empty, Input, Pagination, Radio, Spin, Tooltip } from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import { deleteCenterApi, listCentersApi } from '#/api';

import CenterFormModal from './form-modal.vue';

defineOptions({ name: 'CenterManagement' });

const viewMode = ref<'card' | 'table'>('card');
const searchKeyword = ref('');
const keyword = ref('');
const cardItems = ref<Center[]>([]);
const cardTotal = ref(0);
const cardPage = ref(1);
const cardPageSize = ref(20);
const cardLoading = ref(false);
let cardRequestId = 0;

async function loadCards() {
  const requestId = ++cardRequestId;
  cardLoading.value = true;
  try {
    const result = await listCentersApi({
      keyword: keyword.value || undefined,
      page: cardPage.value,
      pageSize: cardPageSize.value,
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

function onSearch() {
  keyword.value = searchKeyword.value.trim();
  cardPage.value = 1;
  refresh();
}

function onReset() {
  searchKeyword.value = '';
  keyword.value = '';
  cardPage.value = 1;
  refresh();
}

function onCardPageChange(page: number, pageSize: number) {
  cardPage.value = page;
  cardPageSize.value = pageSize;
  void loadCards();
}

onMounted(() => { void loadCards(); });
watch(viewMode, (mode) => { if (mode === 'card') void loadCards(); });

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
      query: async ({ page }) => {
        return await listCentersApi({
          keyword: keyword.value || undefined,
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
        refresh();
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
    <div class="flex h-full min-h-0 flex-col gap-4">
      <div class="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
        <div class="flex flex-wrap items-center gap-2">
          <Input v-model:value="searchKeyword" allow-clear placeholder="搜索中心名称或标识码" style="width: 240px" @press-enter="onSearch" />
          <Button type="primary" @click="onSearch">搜索</Button>
          <Button @click="onReset">重置</Button>
        </div>
        <div class="flex items-center justify-end gap-3">
          <Button @click="refresh">刷新</Button>
          <Radio.Group v-model:value="viewMode" button-style="solid" aria-label="中心展示方式">
            <Radio.Button value="card">卡片视图</Radio.Button>
            <Radio.Button value="table">列表视图</Radio.Button>
          </Radio.Group>
          <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新建中心</Button>
        </div>
      </div>
      <div v-if="viewMode === 'card'" class="flex min-h-0 flex-1 flex-col gap-4">
        <Spin :spinning="cardLoading" class="min-h-0 flex-1 overflow-y-auto">
          <div v-if="cardItems.length" class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            <Card v-for="center in cardItems" :key="center.id" class="h-full" size="small">
              <template #title>
                <div class="flex min-w-0 items-center gap-2">
                  <span class="truncate" :title="center.name">{{ center.name }}</span>
                  <span class="shrink-0 rounded border border-blue-200 bg-blue-50 px-2 py-0.5 text-xs font-normal text-blue-600 dark:border-blue-800 dark:bg-blue-950 dark:text-blue-300">配置真相源</span>
                </div>
              </template>
              <div class="space-y-3 text-sm">
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">标识码</span><span class="break-all text-right font-medium">{{ center.code }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">备注描述</span><span class="line-clamp-2 text-right" :title="center.description">{{ center.description || '暂无备注' }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">创建时间</span><span class="text-right">{{ center.createdAt || '-' }}</span></div>
                <div class="flex justify-between gap-3"><span class="shrink-0 text-gray-500">更新时间</span><span class="text-right">{{ center.updatedAt || '-' }}</span></div>
                <div v-access:code="'orp:config:write'" class="flex justify-end gap-1 border-t pt-2">
                  <Button type="link" @click="onEdit(center)">编辑</Button>
                  <Button danger type="link" @click="onDelete(center)">删除</Button>
                </div>
              </div>
            </Card>
          </div>
          <Empty v-else-if="!cardLoading" description="暂无中心" class="mt-16" />
        </Spin>
        <div v-if="cardTotal > 0" class="flex justify-end">
          <Pagination :current="cardPage" :page-size="cardPageSize" :total="cardTotal" show-size-changer :show-total="(total: number) => `共 ${total} 条`" @change="onCardPageChange" />
        </div>
      </div>
      <Grid v-else table-title="中心列表" class="min-h-0 flex-1">
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
    </div>
    <CenterForm @success="refresh" />
  </Page>
</template>
