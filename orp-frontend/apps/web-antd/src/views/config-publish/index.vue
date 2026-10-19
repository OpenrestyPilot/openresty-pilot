<script lang="ts" setup>
import type {
  PublishGroup,
  PublishPendingNode,
  PublishStrategy,
  PublishSummary,
} from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';

import { Page, useVbenDrawer, useVbenModal } from '@vben/common-ui';

import {
  Button,
  Card,
  Checkbox,
  Empty,
  message,
  Spin,
  Tag,
} from 'ant-design-vue';

import { createPublishApi, getPublishSummaryApi } from '#/api';
import { usePublishStore } from '#/store';

import DiffDrawer from './diff-drawer.vue';
import HistoryDrawer from './history-drawer.vue';
import PrecheckModal from './precheck-modal.vue';
import StrategyModal from './strategy-modal.vue';
import ProgressDrawer from './progress-drawer.vue';

defineOptions({ name: 'ConfigPublishManagement' });

const publishStore = usePublishStore();
const route = useRoute();
const router = useRouter();

const loading = ref(false);
const summary = ref<null | PublishSummary>(null);
const checked = reactive<{ ids: number[] }>({ ids: [] });
const publishing = ref(false);

const groups = computed<PublishGroup[]>(
  () => summary.value?.groups ?? [],
);
const pendingCount = computed(() => summary.value?.pendingCount ?? 0);

const STATUS_TEXT: Record<string, string> = {
  maintenance: '维护中',
  offline: '离线',
  online: '在线',
};
const STATUS_COLOR: Record<string, string> = {
  maintenance: 'orange',
  offline: 'red',
  online: 'green',
};

const [DiffView, diffDrawerApi] = useVbenDrawer({
  connectedComponent: DiffDrawer,
});
const [ProgressView, progressDrawerApi] = useVbenDrawer({
  connectedComponent: ProgressDrawer,
});
const [HistoryView, historyDrawerApi] = useVbenDrawer({
  connectedComponent: HistoryDrawer,
});
const [PrecheckView, precheckModalApi] = useVbenModal({
  connectedComponent: PrecheckModal,
});

const [StrategyView, strategyModalApi] = useVbenModal({
  connectedComponent: StrategyModal,
});

async function loadSummary() {
  loading.value = true;
  try {
    summary.value = await getPublishSummaryApi();
    publishStore.pendingCount = summary.value.pendingCount;
    const valid = new Set(
      groups.value.flatMap((g) => g.nodes.map((n) => n.id)),
    );
    checked.ids = checked.ids.filter((id) => valid.has(id));
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  const rawBatchId = route.query.batchId;
  const batchId = typeof rawBatchId === 'string' ? Number(rawBatchId) : Number.NaN;
  if (Number.isSafeInteger(batchId) && batchId > 0) {
    progressDrawerApi.setData({ batchId }).open();
    const query = { ...route.query };
    delete query.batchId;
    void router.replace({ query });
  }
  void loadSummary();
});

function toggleCheck(id: number) {
  const ix = checked.ids.indexOf(id);
  if (ix >= 0) {
    checked.ids.splice(ix, 1);
  } else {
    checked.ids.push(id);
  }
}

function toggleGroup(group: PublishGroup) {
  const ids = group.nodes.map((n) => n.id);
  const all = ids.every((id) => checked.ids.includes(id));
  if (all) {
    checked.ids = checked.ids.filter((id) => !ids.includes(id));
  } else {
    checked.ids = [...new Set([...checked.ids, ...ids])];
  }
}

function viewDiff(node: PublishPendingNode) {
  diffDrawerApi.setData({ nodeId: node.id }).open();
}

function viewHistory(node?: PublishPendingNode) {
  historyDrawerApi.setData({ nodeId: node?.id }).open();
}

/** 批量下发前先执行 Nginx 语法预检，全部通过后方可创建批次 */
function batchPublish() {
  if (!checked.ids.length || publishing.value) {
    return;
  }
  precheckModalApi.setData({ nodeIds: [...checked.ids] }).open();
}

/** 批量预检通过后进入下发策略配置（全量 / 权重分批 + 金丝雀观察） */
async function onBatchPrecheckConfirmed(ids: number[]) {
  // 预检通过 -> 打开策略配置（全量 / 权重分批 + 金丝雀等待）
  strategyModalApi.setData({ nodeIds: ids }).open();
}

/** 策略确认后真正创建下发批次 */
async function onStrategyConfirmed(payload: {
  nodeIds: number[];
  strategy?: PublishStrategy;
}) {
  publishing.value = true;
  try {
    const res = await createPublishApi(payload.nodeIds, payload.strategy);
    message.success(
      payload.strategy?.mode === 'weighted'
        ? `已创建权重分批下发任务（${res.total} 个实例，低权重金丝雀优先）`
        : `已创建下发任务，${res.total} 个实例开始下发`,
    );
    precheckModalApi.close();
    strategyModalApi.close();
    progressDrawerApi.setData({ batchId: res.batchId }).open();
  } catch {
    // 错误提示由请求拦截器统一处理；策略弹窗保持打开便于调整后重试
  } finally {
    publishing.value = false;
  }
}

function onPublished(batchId: number) {
  diffDrawerApi.close();
  progressDrawerApi.setData({ batchId }).open();
}

/** 历史快照已重新预检，查看真实节点的回滚发布进度。 */
function onHistoryRollback(batchId: number) {
  progressDrawerApi.setData({ batchId }).open();
}

/** 下发批次结束后刷新待下发列表与角标 */
function onSettled() {
  loadSummary();
}

function onProgressClosed() {
  loadSummary();
}
</script>

<template>
  <Page
    description="人工审计各中心实例的期望配置与运行配置差异，确认无误后单实例或跨中心批量下发生效。"
    title="配置比对与下发"
  >
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div class="text-sm">
        <template v-if="pendingCount > 0">
          共 <b class="text-md">{{ pendingCount }}</b> 个实例配置待下发，涉及
          <b>{{ groups.length }}</b> 个中心。请先查看差异，再选择实例执行预检与下发
        </template>
        <span v-else class="text-gray-400 dark:text-gray-500">
          当前所有实例配置均已同步，无待下发变更
        </span>
      </div>
      <Button @click="viewHistory()">查看下发历史</Button>
    </div>

    <Spin :spinning="loading">
      <Empty
        v-if="!loading && !groups.length"
        class="py-16"
        description="暂无待下发变更"
      >
        <Button type="primary" @click="viewHistory()">查看下发历史</Button>
      </Empty>
      <div v-else class="flex flex-col gap-4 pb-24">
        <Card v-for="g in groups" :key="g.centerId" size="small">
          <template #title>
            <div class="flex items-center gap-2">
              <Checkbox
                :aria-label="`选择${g.centerName}下全部待下发实例`"
                :checked="g.nodes.every((n) => checked.ids.includes(n.id))"
                :indeterminate="
                  g.nodes.some((n) => checked.ids.includes(n.id)) &&
                  !g.nodes.every((n) => checked.ids.includes(n.id))
                "
                @change="toggleGroup(g)"
              />
              <span>{{ g.centerName }}</span>
              <Tag class="!m-0" color="orange">
                {{ g.nodes.length }} 台实例待下发
              </Tag>
            </div>
          </template>
          <div class="flex flex-col divide-y divide-gray-100 dark:divide-gray-700">
            <div
              v-for="n in g.nodes"
              :key="n.id"
              class="flex items-center gap-3 py-3"
            >
              <Checkbox
                :aria-label="`选择节点 ${n.name}`"
                :checked="checked.ids.includes(n.id)"
                @change="toggleCheck(n.id)"
              />
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <span class="font-medium">{{ n.name }}</span>
                  <Tag class="!m-0" :color="STATUS_COLOR[n.status]">
                    {{ STATUS_TEXT[n.status] ?? n.status }}
                  </Tag>
                  <Tag class="!m-0" color="geekblue">
                    {{ n.eventCount }} 项变更事件
                  </Tag>
                </div>
                <div class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                  {{ n.host }} · 最近变更
                  {{ n.lastChangeAt ?? '—' }}
                </div>
              </div>
              <div class="shrink-0">
                <Button size="small" type="link" @click="viewDiff(n)">
                  查看差异
                </Button>
                <Button size="small" type="link" @click="viewHistory(n)">
                  变更历史
                </Button>
              </div>
            </div>
          </div>
        </Card>
      </div>
    </Spin>

    <div
      v-if="checked.ids.length"
      class="fixed right-8 bottom-6 z-50 flex items-center gap-3 rounded-lg border border-gray-200 bg-white px-4 py-2.5 shadow-lg dark:border-gray-700 dark:bg-gray-800"
    >
      <span class="text-sm">
        已选 <b>{{ checked.ids.length }}</b> 个实例
      </span>
      <Button
        v-access:code="'orp:publish'"
        :loading="publishing"
        danger
        type="primary"
        @click="batchPublish"
      >
        预检并下发
      </Button>
      <Button type="text" @click="checked.ids = []">取消</Button>
    </div>

    <DiffView @published="onPublished" />
    <ProgressView @settled="onSettled" @closed="onProgressClosed" />
    <HistoryView @rollback="onHistoryRollback" />
    <PrecheckView @confirmed="onBatchPrecheckConfirmed" />
    <StrategyView @confirmed="onStrategyConfirmed" />
  </Page>
</template>
