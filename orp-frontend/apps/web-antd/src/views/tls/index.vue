<script lang="ts" setup>
import type { Certificate, CertificateStats } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { onMounted, ref } from 'vue';

import { Page, useVbenModal } from '@vben/common-ui';

import { Button, Modal as AntModal, Tag, Tooltip } from 'ant-design-vue';
import dayjs from 'dayjs';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import {
  deleteCertificateApi,
  getCertificateStatsApi,
  listCertificatesApi,
} from '#/api';

import TlsFormModal from './form-modal.vue';

defineOptions({ name: 'TlsCertificateManagement' });

/** 证书剩余天数与四级紧急度状态（≤7 天紧急 / 8-30 天临期 / >30 天正常 / 已过期） */
function certState(notAfter: string) {
  const days = dayjs(notAfter).diff(dayjs(), 'day');
  if (days < 0) {
    return { color: 'red', days, text: `已过期 ${Math.abs(days)} 天` };
  }
  if (days <= 7) {
    return { color: 'volcano', days, text: `仅剩 ${days} 天` };
  }
  if (days <= 30) {
    return { color: 'gold', days, text: `剩 ${days} 天` };
  }
  return { color: 'green', days, text: `剩 ${days} 天` };
}

/** 顶部状态预警统计 */
const certStats = ref<null | CertificateStats>(null);

async function loadStats() {
  try {
    certStats.value = await getCertificateStatsApi();
  } catch {
    // 统计加载失败不阻塞证书列表
  }
}

onMounted(loadStats);

/** 观察条目配色（与列表四级标准一致） */
function watchTone(days: number) {
  if (days < 0) return 'red';
  if (days <= 7) return 'volcano';
  if (days <= 30) return 'gold';
  return 'green';
}

function watchText(days: number) {
  return days < 0 ? `已过期 ${Math.abs(days)} 天` : `剩 ${days} 天`;
}

const formOptions: VbenFormProps = {
  collapsed: false,
  schema: [
    {
      component: 'Input',
      componentProps: { allowClear: true, placeholder: '证书名称或域名' },
      fieldName: 'keyword',
      label: '关键字',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<Certificate> = {
  columns: [
    { title: '序号', type: 'seq', width: 60 },
    { field: 'name', minWidth: 160, title: '证书名称' },
    { field: 'domains', minWidth: 180, title: '关联域名' },
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
    {
      field: 'validity',
      slots: { default: 'validity' },
      title: '有效期范围',
      width: 260,
    },
    {
      field: 'centerScope',
      slots: { default: 'scope' },
      title: '适用中心范围',
      width: 160,
    },
    { field: 'createdAt', title: '录入时间', width: 170 },
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
        return await listCertificatesApi({
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

const [TlsForm, tlsFormApi] = useVbenModal({
  connectedComponent: TlsFormModal,
});

function onCreate() {
  tlsFormApi.setData({ record: undefined }).open();
}

function onEdit(row: Certificate) {
  tlsFormApi.setData({ record: row }).open();
}

function onDelete(row: Certificate) {
  AntModal.confirm({
    content: '若证书仍被 HTTP 监听域名引用，将无法删除。',
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => formApiDelete(row),
    title: `确认删除证书「${row.name}」？`,
  });
}

async function formApiDelete(row: Certificate) {
  try {
    await deleteCertificateApi(row.id);
    gridApi.query();
    loadStats();
  } catch {
    // 错误提示由请求拦截器统一处理
  }
}

function onFormSuccess() {
  gridApi.query();
  loadStats();
}
</script>

<template>
  <Page
    auto-content-height
    description="集中录入与管理 TLS 证书，绑定至对应域名。"
    title="TLS 证书管理"
  >
    <div v-if="certStats" class="mb-4 grid grid-cols-2 gap-3 lg:grid-cols-4">
      <div
        class="rounded-lg border border-gray-200 bg-white p-4 dark:border-gray-700 dark:bg-gray-800/40"
      >
        <div class="text-xs text-gray-400">证书总数</div>
        <div class="mt-1 text-2xl font-semibold">{{ certStats.total }}</div>
        <div class="mt-1 text-xs text-gray-400">覆盖全中心域名接入</div>
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50/60 p-4 dark:border-amber-700/60 dark:bg-amber-900/10"
      >
        <div class="text-xs text-amber-600 dark:text-amber-400">临期预警（8-30 天）</div>
        <div class="mt-1 text-2xl font-semibold text-amber-600 dark:text-amber-400">
          {{ certStats.expiringCount }}
        </div>
        <div class="mt-1 text-xs text-gray-400">建议纳入轮换计划</div>
      </div>
      <div
        class="rounded-lg border border-orange-200 bg-orange-50/60 p-4 dark:border-orange-700/60 dark:bg-orange-900/10"
      >
        <div class="text-xs text-orange-600 dark:text-orange-400">紧急临期（≤7 天）</div>
        <div class="mt-1 text-2xl font-semibold text-orange-600 dark:text-orange-400">
          {{ certStats.criticalCount }}
        </div>
        <div class="mt-1 text-xs text-gray-400">需立即安排续期</div>
      </div>
      <div
        class="rounded-lg border border-rose-200 bg-rose-50/60 p-4 dark:border-rose-700/60 dark:bg-rose-900/10"
      >
        <div class="text-xs text-rose-600 dark:text-rose-400">已过期</div>
        <div class="mt-1 text-2xl font-semibold text-rose-600 dark:text-rose-400">
          {{ certStats.expiredCount }}
        </div>
        <div class="mt-1 text-xs text-gray-400">已生效域名握手失败风险</div>
      </div>
    </div>
    <div
      v-if="certStats?.watchlist?.length"
      class="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-gray-200 bg-white px-4 py-2.5 text-xs dark:border-gray-700 dark:bg-gray-800/40"
    >
      <span class="font-medium text-gray-500 dark:text-gray-400">临期观察：</span>
      <span
        v-for="w in certStats.watchlist.slice(0, 6)"
        :key="w.id"
        class="inline-flex items-center gap-1.5"
      >
        <Tag :color="watchTone(w.days)" class="!m-0">{{ watchText(w.days) }}</Tag>
        <span class="text-gray-600 dark:text-gray-300">{{ w.domains.split(',')[0]?.trim() }}</span>
      </span>
      <span
        v-if="certStats.watchlist.length > 6"
        class="text-gray-400"
      >
        等 {{ certStats.watchlist.length }} 张证书待轮换
      </span>
    </div>
    <Grid table-title="证书列表">
      <template #toolbar-tools>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">录入证书</Button>
      </template>
      <template #layer>
        <Tooltip
          title="真动态 (Lua SSL)：证书由 Lua 运行时内存热更新，零 Reload、零闪断，变更即时生效。"
        >
          <span
            class="inline-flex cursor-help items-center gap-1 text-xs text-emerald-600 bg-emerald-50 px-2 py-0.5 rounded border border-emerald-200"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>
            真动态 (Lua SSL)
          </span>
        </Tooltip>
      </template>
      <template #validity="{ row }">
        <div class="flex items-center gap-2">
          <span>{{ row.notBefore.slice(0, 10) }} ~ {{ row.notAfter.slice(0, 10) }}</span>
          <Tag :color="certState(row.notAfter).color">
            {{ certState(row.notAfter).text }}
          </Tag>
        </div>
      </template>
      <template #scope="{ row }">
        <Tag v-if="row.centerScope === 'all'" color="blue">全部中心</Tag>
        <Tooltip v-else :title="`共 ${row.centerScope.length} 个中心`">
          <Tag :color="row.centerScope.length > 0 ? 'cyan' : 'red'">
            {{ row.centerScope.length > 0 ? `${row.centerScope.length} 个中心` : '未配置' }}
          </Tag>
        </Tooltip>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">删除</Button>
      </template>
    </Grid>
    <TlsForm @success="onFormSuccess" />
  </Page>
</template>
