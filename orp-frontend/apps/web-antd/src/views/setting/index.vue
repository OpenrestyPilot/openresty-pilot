<script lang="ts" setup>
import type { OrpSetting, OrpSettingGroup } from '#/api';
import type { VbenFormProps } from '#/adapter/form';
import type { VxeGridProps } from '#/adapter/vxe-table';

import { computed, onMounted, ref } from 'vue';

import { Page, useVbenModal } from '@vben/common-ui';
import { useAccessStore } from '@vben/stores';

import { SettingOutlined, UploadOutlined } from '@ant-design/icons-vue';
import {
  Button,
  Modal as AntModal,
  Popconfirm,
  Select,
  Switch,
  Tag,
  Tooltip,
  message,
} from 'ant-design-vue';

import { useVbenVxeGrid } from '#/adapter/vxe-table';
import {
  batchMoveSettingsApi,
  deleteSettingApi,
  getGeoIPDatabaseStatusApi,
  importGeoIPDatabaseApi,
  listSettingGroupEntitiesApi,
  listSettingsApi,
  toggleSettingStatusApi,
} from '#/api';

import SettingFormModal from './form-modal.vue';
import SettingGroupModal from './group-modal.vue';

defineOptions({ name: 'SystemSettingManagement' });

/** 参数类型徽标 */
const TYPE_META: Record<string, { color: string; label: string }> = {
  boolean: { color: 'purple', label: '布尔' },
  json: { color: 'gold', label: 'JSON' },
  number: { color: 'cyan', label: '数字' },
  string: { color: 'blue', label: '字符串' },
};

/** 分组徽标颜色 */
const GROUP_COLOR: Record<string, string> = {
  业务参数: 'cyan',
  安全策略: 'volcano',
  接口密钥: 'gold',
  系统基础: 'blue',
};

const groupOptions = ref<{ label: string; value: string }[]>([
  { label: '全部分组', value: 'all' },
  { label: '业务参数', value: '业务参数' },
  { label: '安全策略', value: '安全策略' },
  { label: '接口密钥', value: '接口密钥' },
  { label: '系统基础', value: '系统基础' },
]);

/** 分组实体列表（导航条 + 筛选下拉数据源，按 sortOrder 升序） */
const groups = ref<OrpSettingGroup[]>([]);
const navigationGroups = computed(() => [
  { name: '全部配置', value: 'all', custom: false },
  ...groups.value.map((group) => ({
    name: group.name,
    value: group.name,
    custom: !group.isSystem,
  })),
]);
/** 当前导航选中的分组（与筛选表单双向联动） */
const activeGroup = ref('all');
/** 勾选的配置项（批量移动分组用） */
const selectedRows = ref<OrpSetting[]>([]);

/** 分组管理与批量移动要求配置写入权限。 */
const accessStore = useAccessStore();
const canManageGroups = computed(() => {
  const permissions = accessStore.accessCodes;
  return permissions.includes('*') || permissions.includes('orp:config:write');
});

const geoIPStatus = ref<null | Awaited<ReturnType<typeof getGeoIPDatabaseStatusApi>>>(null);
const geoIPLoading = ref(false);
const geoIPImporting = ref(false);
const geoIPFileInput = ref<HTMLInputElement>();

async function reloadGeoIPStatus() {
  geoIPLoading.value = true;
  try {
    geoIPStatus.value = await getGeoIPDatabaseStatusApi();
  } catch {
    geoIPStatus.value = null;
  } finally {
    geoIPLoading.value = false;
  }
}

async function onGeoIPFileChange(event: Event) {
  const input = event.target as HTMLInputElement;
  const file = input.files?.[0];
  input.value = '';
  if (!file) return;
  if (!file.name.toLowerCase().endsWith('.mmdb')) {
    message.error('请选择 .mmdb 格式的 GeoIP City 数据库');
    return;
  }
  if (file.size === 0 || file.size > 256 * 1024 * 1024) {
    message.error('数据库文件必须大于 0 且不超过 256 MB');
    return;
  }
  geoIPImporting.value = true;
  try {
    geoIPStatus.value = await importGeoIPDatabaseApi(file);
    message.success('GeoIP City 数据库已校验并启用，大屏将在下一次推送中使用');
  } catch (error) {
    message.error(error instanceof Error ? error.message : 'GeoIP 数据库导入失败');
  } finally {
    geoIPImporting.value = false;
  }
}

function formatBytes(value = 0) {
  if (value >= 1024 * 1024) return `${(value / (1024 * 1024)).toFixed(1)} MB`;
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${value} B`;
}

const geoIPStateLabel = computed(() => {
  if (geoIPStatus.value?.state === 'ready') return '已启用';
  if (geoIPStatus.value?.state === 'error') return '文件异常';
  return '未配置';
});

async function reloadGroups() {
  try {
    groups.value = await listSettingGroupEntitiesApi();
    groupOptions.value = [
      { label: '全部分组', value: 'all' },
      ...groups.value.map((g) => ({ label: g.name, value: g.name })),
    ];
  } catch {
    // 失败时保留静态分组选项，由请求拦截器统一提示
  }
}

onMounted(() => {
  void reloadGroups();
  void reloadGeoIPStatus();
});

/** 分组徽标颜色：系统预置组固定配色，自定义组统一紫色 */
function groupColor(group: string): string {
  if (GROUP_COLOR[group]) return GROUP_COLOR[group];
  const g = groups.value.find((x) => x.name === group);
  return g && !g.isSystem ? 'purple' : 'default';
}

/** 导航切换分组：同步筛选表单值并触发查询 */
async function onGroupNav(group: string) {
  activeGroup.value = group;
  try {
    gridApi.formApi?.setValues({ group });
  } catch {
    // 表单未挂载时忽略，直接按当前值查询
  }
  await gridApi.query();
}

/** 勾选行变化（含表头全选） */
function onCheckChange({ records }: any) {
  selectedRows.value = records ?? [];
}

const formOptions: VbenFormProps = {
  collapsed: false,
  handleValuesChange: (values) => {
    // 筛选下拉与导航按钮高亮双向联动
    activeGroup.value = (values?.group as string) || 'all';
  },
  schema: [
    {
      component: 'Select',
      componentProps: {
        allowClear: true,
        options: groupOptions,
        placeholder: '按分组筛选',
      },
      defaultValue: 'all',
      fieldName: 'group',
      label: '所属分组',
    },
    {
      component: 'Input',
      componentProps: {
        allowClear: true,
        placeholder: '按配置名称 / 键名检索',
      },
      fieldName: 'keyword',
      label: '关键字',
    },
  ],
  submitOnChange: true,
};

const gridOptions: VxeGridProps<OrpSetting> = {
  columns: [
    { title: '', type: 'checkbox', width: 44 },
    { title: '序号', type: 'seq', width: 60 },    { field: 'name', minWidth: 150, title: '配置名称' },
    {
      field: 'key',
      minWidth: 200,
      slots: { default: 'key' },
      title: '配置键名',
    },
    {
      field: 'group',
      width: 110,
      slots: { default: 'group' },
      title: '所属分组',
    },
    {
      field: 'type',
      width: 90,
      slots: { default: 'type' },
      title: '参数类型',
    },
    {
      field: 'value',
      minWidth: 200,
      slots: { default: 'value' },
      title: '配置键值',
    },
    {
      field: 'status',
      width: 90,
      slots: { default: 'status' },
      title: '状态',
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
        return await listSettingsApi({
          group:
            formValues?.group && formValues.group !== 'all'
              ? formValues.group
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

const [Grid, gridApi] = useVbenVxeGrid({
  formOptions,
  gridOptions,
  // vben 不透传 gridOptions.events，须用顶层 gridEvents（v-on 到 vxe-grid）
  gridEvents: {
    checkboxChange: onCheckChange,
    checkboxAll: onCheckChange,
  },
});

const [SettingForm, settingFormApi] = useVbenModal({
  connectedComponent: SettingFormModal,
});

const [GroupModal, groupModalApi] = useVbenModal({
  connectedComponent: SettingGroupModal,
});

/** 配置项保存成功：刷新列表与分组计数 */
function onSettingSaved() {
  gridApi.query();
  reloadGroups();
}

/** 打开分组管理弹窗（CRUD / 排序 / 删除处理） */
function onManageGroups() {
  groupModalApi.setData({}).open();
}

/** 分组实体变更（新增/重命名/删除/排序）：同步导航与列表 */
function onGroupsChanged() {
  reloadGroups();
  gridApi.query();
}

/* ---------- 批量移动配置项至指定分组 ---------- */
const batchMoveOpen = ref(false);
const batchMoveTarget = ref<undefined | string>(undefined);
const batchMoving = ref(false);
const moveTargetOptions = computed(() =>
  groups.value.map((g) => ({ label: g.name, value: g.name })),
);

function onBatchMove() {
  if (selectedRows.value.length === 0) return;
  batchMoveTarget.value = undefined;
  batchMoveOpen.value = true;
}

async function onBatchMoveOk() {
  if (!batchMoveTarget.value) {
    message.warning('请选择目标分组');
    return;
  }
  batchMoving.value = true;
  try {
    const res = await batchMoveSettingsApi(
      selectedRows.value.map((r) => r.id),
      batchMoveTarget.value,
    );
    message.success(
      `已移动 ${res.moved} 个配置项至分组「${res.targetGroup}」，列表已同步刷新`,
    );
    batchMoveOpen.value = false;
    selectedRows.value = [];
    await reloadGroups();
    gridApi.query();
  } catch {
    // 错误提示由请求拦截器统一处理，弹窗保持打开便于修正目标分组
  } finally {
    batchMoving.value = false;
  }
}

function onCreate() {
  settingFormApi.setData({ record: undefined }).open();
}

function onEdit(row: OrpSetting) {
  settingFormApi.setData({ record: row }).open();
}

/** 启用/禁用切换（Popconfirm 二次确认） */
async function onToggleStatus(row: OrpSetting, checked: boolean) {
  try {
    await toggleSettingStatusApi(row.id, checked ? 'enabled' : 'disabled');
    message.success(
      `配置「${row.name}」已${checked ? '启用' : '禁用'}，列表已同步刷新`,
    );
    gridApi.query();
  } catch {
    // 错误提示由请求拦截器统一处理；刷新以还原开关状态
    gridApi.query();
  }
}

function onDelete(row: OrpSetting) {
  AntModal.confirm({
    content: `删除后引用该配置的功能将回退到默认行为，且不可恢复。`,
    okButtonProps: { danger: true },
    okText: '确认删除',
    onOk: async () => {
      try {
        await deleteSettingApi(row.id);
        gridApi.query();
      } catch {
        // 错误提示由请求拦截器统一处理
      }
    },
    title: `确认删除配置「${row.name}」？`,
  });
}
</script>

<template>
  <Page
    auto-content-height
    description="统一管理平台运行参数：支持分组筛选、参数类型格式校验、敏感信息脱敏与启停状态控制；支持自定义分组的新增 / 排序 / 删除及配置项归属调整，安全策略（密码复杂度 / 过期时间 / OTP）以预置配置项形式维护。"
    title="系统配置"
  >
    <section class="bg-card mb-3 rounded-lg border border-border p-4" aria-labelledby="geoip-database-title">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="min-w-0 flex-1">
          <div class="flex items-center gap-2">
            <h2 id="geoip-database-title" class="text-base font-semibold">GeoIP City 数据库</h2>
            <Tag :color="geoIPStatus?.state === 'ready' ? 'success' : geoIPStatus?.state === 'error' ? 'error' : 'default'">
              {{ geoIPStateLabel }}
            </Tag>
          </div>
          <p class="text-muted-foreground mt-1 text-sm">
            导入 MaxMind GeoLite2 City 或 GeoIP2 City 的 .mmdb 文件。校验通过后，大屏会在下一次 SSE 更新时显示来源点和流线。
          </p>
          <div v-if="geoIPLoading" class="text-muted-foreground mt-2 text-xs">正在读取数据库状态…</div>
          <div v-else-if="geoIPStatus?.state === 'ready'" class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
            <span>文件：{{ geoIPStatus.fileName }}</span>
            <span>类型：{{ geoIPStatus.databaseType }}</span>
            <span>大小：{{ formatBytes(geoIPStatus.sizeBytes) }}</span>
            <span>来源：{{ geoIPStatus.source === 'managed' ? '控制台导入' : '环境变量配置' }}</span>
            <span v-if="geoIPStatus.importedAt">导入时间：{{ geoIPStatus.importedAt }}</span>
          </div>
          <div v-else class="text-muted-foreground mt-2 text-xs">
            {{ geoIPStatus?.message || '尚未导入数据库；地图轮廓和大盘指标仍可使用。' }}
          </div>
          <div v-if="geoIPStatus?.state === 'ready' && geoIPStatus.sha256" class="text-muted-foreground mt-1 break-all font-mono text-[11px]">
            SHA-256：{{ geoIPStatus.sha256 }}
          </div>
          <p class="text-muted-foreground mt-2 text-xs">
            请仅导入已获授权使用的数据库文件；上传文件限制为 256 MB，原文件名不会用于服务器存储路径。
          </p>
        </div>
        <div v-if="canManageGroups" class="shrink-0">
          <input
            ref="geoIPFileInput"
            accept=".mmdb,application/octet-stream"
            class="hidden"
            type="file"
            @change="onGeoIPFileChange"
          />
          <Button v-access:code="'orp:system:manage'" :loading="geoIPImporting" @click="geoIPFileInput?.click()">
            <UploadOutlined />
            {{ geoIPStatus?.state === 'ready' ? '替换数据库' : '导入数据库' }}
          </Button>
        </div>
      </div>
    </section>
    <div class="bg-card mb-3 rounded-lg border border-border p-3">
      <div class="flex items-center justify-between gap-3">
        <span class="shrink-0 text-sm font-medium">配置分组</span>
        <Button v-if="canManageGroups" size="small" @click="onManageGroups">
          <SettingOutlined />
          管理分组
        </Button>
      </div>
      <nav
        aria-label="配置分组导航"
        class="-mx-1 mt-3 flex gap-1 overflow-x-auto px-1 pb-1"
      >
        <button
          v-for="item in navigationGroups"
          :key="item.value"
          :aria-pressed="activeGroup === item.value"
          class="inline-flex h-9 shrink-0 items-center gap-2 rounded-md border px-3 text-sm transition-colors"
          :class="
            activeGroup === item.value
              ? 'border-primary bg-primary text-primary-foreground shadow-sm'
              : 'border-transparent text-muted-foreground hover:bg-accent hover:text-foreground'
          "
          type="button"
          @click="onGroupNav(item.value)"
        >
          <span>{{ item.name }}</span>
          <span
            v-if="item.custom"
            class="rounded bg-purple-100 px-1.5 py-0.5 text-[10px] leading-none text-purple-700 dark:bg-purple-950 dark:text-purple-300"
          >
            自定义
          </span>
        </button>
      </nav>
    </div>
    <Grid table-title="配置项列表">
      <template #toolbar-tools>
        <Button
          v-if="canManageGroups"
          :disabled="selectedRows.length === 0"
          class="mr-2"
          size="small"
          @click="onBatchMove"
        >
          批量移动分组{{ selectedRows.length > 0 ? `（${selectedRows.length}）` : '' }}
        </Button>
        <Button v-access:code="'orp:config:write'" type="primary" @click="onCreate">新增配置</Button>
      </template>
      <template #key="{ row }">
        <span class="font-mono text-xs">{{ row.key }}</span>
      </template>
      <template #group="{ row }">
        <Tag :color="groupColor(row.group)" class="text-xs">
          {{ row.group }}
        </Tag>
      </template>
      <template #type="{ row }">
        <Tag :color="TYPE_META[row.type]?.color ?? 'default'" class="text-xs">
          {{ TYPE_META[row.type]?.label ?? row.type }}
        </Tag>
      </template>
      <template #value="{ row }">
        <Tooltip
          v-if="row.isSensitive"
          title="敏感信息已脱敏展示，编辑时可查看明文"
        >
          <span class="inline-flex items-center gap-1">
            <span class="font-mono text-xs">{{ row.value }}</span>
            <Tag class="text-xs" color="red">敏感</Tag>
          </span>
        </Tooltip>
        <span
          v-else
          class="block max-w-[280px] truncate font-mono text-xs"
          :title="row.value"
        >
          {{ row.value }}
        </span>
      </template>
      <template #status="{ row }">
        <Popconfirm
          v-access:code="'orp:config:write'"
          :title="
            row.status === 'enabled'
              ? `确认禁用「${row.name}」？禁用后引用方回退默认值`
              : `确认启用「${row.name}」？启用后配置立即生效`
          "
          ok-text="确认"
          cancel-text="取消"
          @confirm="onToggleStatus(row, row.status !== 'enabled')"
        >
          <Switch
            :checked="row.status === 'enabled'"
            checked-children="启用"
            size="small"
            un-checked-children="禁用"
          />
        </Popconfirm>
      </template>
      <template #action="{ row }">
        <Button v-access:code="'orp:config:write'" size="small" type="link" @click="onEdit(row)">编辑</Button>
        <Button v-access:code="'orp:config:write'" danger size="small" type="link" @click="onDelete(row)">
          删除
        </Button>
      </template>
    </Grid>
    <SettingForm @success="onSettingSaved" />
    <GroupModal @change="onGroupsChanged" />
    <AntModal
      v-model:open="batchMoveOpen"
      :confirm-loading="batchMoving"
      ok-text="确认移动"
      title="批量移动至分组"
      width="480px"
      @ok="onBatchMoveOk"
    >
      <div class="space-y-3 py-2">
        <div class="text-sm">
          已勾选 <b>{{ selectedRows.length }}</b> 个配置项，确认后其所属分组将立即更新：
        </div>
        <div
          v-if="selectedRows.length"
          class="text-muted-foreground max-h-28 overflow-y-auto text-xs leading-5"
        >
          {{ selectedRows.map((r) => r.name).join('、') }}
        </div>
        <Select
          v-model:value="batchMoveTarget"
          :options="moveTargetOptions"
          placeholder="选择目标分组（含系统预置与自定义分组）"
          show-search
        />
      </div>
    </AntModal>
  </Page>
</template>
