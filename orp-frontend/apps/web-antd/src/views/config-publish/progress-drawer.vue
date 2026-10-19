<script lang="ts" setup>
import type { PublishBatch } from '#/api';

import { computed, onUnmounted, ref } from 'vue';

import { useVbenDrawer } from '@vben/common-ui';

import { Alert, Button, Input, message, Popconfirm, Progress, Tag } from 'ant-design-vue';

import {
  abortPublishBatchApi,
  advancePublishBatchApi,
  getPublishBatchApi,
  reconcileContinuePublishBatchApi,
} from '#/api';

defineOptions({ name: 'PublishProgressDrawer' });

const emit = defineEmits<{ closed: []; settled: [] }>();


const batch = ref<null | PublishBatch>(null);
const observedReleaseByItem = ref<Record<number, string>>({});
let timer: null | ReturnType<typeof setInterval> = null;
let batchId: null | number = null;
const acting = ref(false);

const STATUS_MAP: Record<string, { color: string; text: string }> = {
  aborted: { color: 'default', text: '已中止' },
  deploying: { color: 'processing', text: '下发中' },
  failed: { color: 'error', text: '失败' },
  queued: { color: 'default', text: '排队中' },
  success: { color: 'success', text: '成功' },
  validating: { color: 'warning', text: '校验中' },
};

const GROUP_STATUS_MAP: Record<string, { color: string; text: string }> = {
  aborted: { color: 'default', text: '已中止' },
  failed: { color: 'error', text: '失败' },
  observing: { color: 'warning', text: '金丝雀观察中' },
  pending: { color: 'default', text: '待下发' },
  running: { color: 'processing', text: '下发中' },
  success: { color: 'success', text: '已完成' },
};

const PHASE_MAP: Record<string, { color: string; text: string }> = {
  aborted: { color: 'error', text: '已中止' },
  canary_observing: { color: 'warning', text: '金丝雀观察期' },
  done: { color: 'success', text: '已完成' },
  recovery_required: { color: 'error', text: '需要人工核对恢复' },
  running: { color: 'processing', text: '执行中' },
};

const [Drawer, drawerApi] = useVbenDrawer<{ batchId?: number }>({
  class: 'w-[640px]',
  confirmText: '关闭',
  showCancelButton: false,
  onOpenChange(isOpen) {
    if (isOpen) {
      batch.value = null;
      observedReleaseByItem.value = {};
      batchId = drawerApi.getData()?.batchId ?? null;
      if (batchId != null) {
        start(batchId);
      }
    } else {
      stop();
      emit('closed');
    }
  },
  onConfirm() {
    drawerApi.close();
  },
  title: '下发进度',
});

const percent = computed(() => {
  if (!batch.value || !batch.value.items.length) {
    return 0;
  }
  const settled =
    batch.value.successCount + batch.value.failCount + batch.value.abortedCount;
  return Math.round((settled / batch.value.items.length) * 100);
});

const createdAtText = computed(() =>
  batch.value
    ? new Date(batch.value.createdAt).toLocaleString('zh-CN', {
        hour12: false,
      })
    : '—',
);

/** 金丝雀观察期倒计时描述 */
const canaryText = computed(() => {
  const c = batch.value?.canary;
  if (!c?.enabled || batch.value?.phase !== 'canary_observing') return '';
  const r = c.remaining ?? 0;
  if (r >= 60) {
    const m = Math.floor(r / 60);
    const s2 = r % 60;
    return `金丝雀批已下发完成，观察等待剩余 ${m} 分 ${s2} 秒，到期自动推进下一批`;
  }
  return `金丝雀批已下发完成，观察等待剩余 ${r} 秒，到期自动推进下一批`;
});

/** 金丝雀观察期内可操作 */
const canaryOperable = computed(
  () => batch.value?.phase === 'canary_observing' && !batch.value.done,
);

/** 权重分批模式 */
const isWeighted = computed(() => batch.value?.mode === 'weighted');
const recoveryItems = computed(() => batch.value?.items.filter((item) => item.requiresReconciliation) ?? []);
const recoveryReady = computed(() => recoveryItems.value.length > 0 && recoveryItems.value.every((item) => {
  const value = observedReleaseByItem.value[item.itemId]?.trim() ?? '';
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
}));

const nodeNameOf = (id: number) =>
  batch.value?.items.find((x) => x.nodeId === id)?.nodeName ?? `节点 ${id}`;

async function poll(id: number) {
  try {
    const data = await getPublishBatchApi(id);
    batch.value = data;
    if (data.done) {
      stop();
      emit('settled');
    }
  } catch {
    stop();
  }
}

function start(id: number) {
  stop();
  poll(id);
  timer = setInterval(() => poll(id), 800);
}

function stop() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

/** 中止下发（观察期内） */
async function onAbort() {
  if (batchId == null || acting.value) return;
  acting.value = true;
  try {
    const res = await abortPublishBatchApi(batchId);
    message.warning(`已中止下发，${res.abortedCount} 个未下发实例已取消`);
    await poll(batchId);
  } catch {
    // 错误提示由请求拦截器统一处理
  } finally {
    acting.value = false;
  }
}

/** 立即推进（跳过观察等待） */
async function onAdvance() {
  if (batchId == null || acting.value) return;
  acting.value = true;
  try {
    await advancePublishBatchApi(batchId);
    message.success('已跳过观察等待，立即推进下一批下发');
    if (timer == null) start(batchId);
    else await poll(batchId);
  } catch {
    // 错误提示由请求拦截器统一处理
  } finally {
    acting.value = false;
  }
}

/** Confirm field observations, then let the control plane probe and reconcile each recovery item. */
async function onRecoveryContinue() {
  if (batchId == null || acting.value || !recoveryReady.value) return;
  acting.value = true;
  try {
    const confirmations = recoveryItems.value.map((item) => ({
      itemId: item.itemId,
      observedReleaseUuid: observedReleaseByItem.value[item.itemId]!.trim(),
    }));
    const result = await reconcileContinuePublishBatchApi(batchId, confirmations);
    message.success(`已提交人工核对：${result.alreadyAppliedCount} 个已确认生效，${result.resumedCount} 个未开始目标恢复执行`);
    await poll(batchId);
  } catch {
    // 权限和探针核对失败由统一请求拦截器说明；保留人工输入便于修正。
  } finally {
    acting.value = false;
  }
}

onUnmounted(stop);
</script>

<template>
  <Drawer>
    <template v-if="batch">
      <div class="mb-4">
        <div class="mb-2 flex flex-wrap items-center justify-between gap-2 text-sm">
          <span class="flex items-center gap-2">
            批次 #{{ batch.id }} · 发起人 {{ batch.operator }} ·
            {{ createdAtText }}
            <Tag :color="PHASE_MAP[batch.phase]?.color ?? 'default'">
              {{ PHASE_MAP[batch.phase]?.text ?? batch.phase }}
            </Tag>
            <Tag v-if="isWeighted" color="orange">权重分批 · {{ batch.totalBatches }} 批</Tag>
            <Tag v-else>全量下发</Tag>
          </span>
          <span class="text-xs text-gray-400">
            共 {{ batch.items.length }} 个实例 · 成功 {{ batch.successCount }} ·
            失败 {{ batch.failCount }}<template v-if="batch.abortedCount"> · 已中止 {{ batch.abortedCount }}</template>
          </span>
        </div>
        <Progress
          :percent="percent"
          :status="
            batch.done
              ? batch.failCount
                ? 'exception'
                : batch.abortedCount
                  ? 'normal'
                  : 'success'
              : 'active'
          "
          :stroke-color="{ '0%': '#108ee9', '100%': '#87d068' }"
        />
      </div>

      <Alert
        v-if="batch.phase === 'recovery_required'"
        class="mb-4"
        message="控制面中断后需要逐节点人工核对"
        show-icon
        type="error"
      >
        <template #description>
          <p class="mb-3">请在每个节点现场查看当前活动 release UUID 并逐项填写。提交后服务端会运行固定版本探针：已确认候选版本的节点记为成功，仍处于冻结基线的未开始任务才会继续；已开始但结果不明的任务不会重放。</p>
          <div class="space-y-3">
            <div v-for="item in recoveryItems" :key="item.itemId" class="rounded border border-red-200 p-3 dark:border-red-900">
              <div class="mb-2 flex flex-wrap items-center gap-2"><strong>{{ item.nodeName }}（{{ item.centerName }}）</strong><Tag color="red">{{ STATUS_MAP[item.status]?.text ?? item.status }}</Tag><span class="text-xs text-gray-500">批次项 #{{ item.itemId }}</span></div>
              <div v-if="item.error" class="mb-2 text-xs text-red-600">恢复原因：{{ item.error }}</div>
              <Input v-model:value="observedReleaseByItem[item.itemId]" placeholder="现场读取当前活动 release UUID" autocomplete="off" />
            </div>
            <div v-if="recoveryItems.length === 0" class="text-sm text-red-600">服务端未返回待核对的批次项。请刷新批次状态；不会推断或补造恢复目标。</div>
          </div>
          <div class="mt-3 flex justify-end">
            <Popconfirm
              title="确认提交现场观察的版本并继续批次？"
              description="服务端会逐节点探测并按核对结果标记已生效项或继续未开始项。"
              ok-text="提交核对"
              cancel-text="再检查一下"
              @confirm="onRecoveryContinue"
            >
              <Button type="primary" danger :loading="acting" :disabled="!recoveryReady" v-access:code="'orp:publish'">提交核对并继续</Button>
            </Popconfirm>
          </div>
        </template>
      </Alert>

      <!-- 金丝雀观察等待期卡片 -->
      <Alert
        v-if="canaryOperable && canaryText"
        class="mb-4"
        show-icon
        type="warning"
      >
        <template #message>
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="font-mono">{{ canaryText }}</span>
            <span class="flex gap-2">
              <Popconfirm
                title="确认中止本次下发？未下发实例将全部取消"
                @confirm="onAbort"
              >
                <Button danger :loading="acting" size="small" type="primary">
                  中止下发
                </Button>
              </Popconfirm>
              <Button :loading="acting" size="small" @click="onAdvance">
                立即推进
              </Button>
            </span>
          </div>
        </template>
        <template #description>
          观察期内可通过监控确认金丝雀实例（低权重）运行无异常后再推进；中止后未下发实例的待下发变更将保留。
        </template>
      </Alert>

      <Alert
        v-if="batch.done"
        :message="
          batch.failCount
            ? `下发结束：${batch.successCount} 个成功，${batch.failCount} 个失败，失败实例待下发变更已保留，可恢复后重新下发`
            : batch.abortedCount
              ? `下发已中止：${batch.successCount} 个实例已成功下发，${batch.abortedCount} 个实例未下发，其待下发变更已保留`
              : `下发完成：${batch.successCount} 个实例全部成功，运行配置已与期望配置一致`
        "
        :type="batch.failCount ? 'warning' : batch.abortedCount ? 'warning' : 'success'"
        class="mb-4"
        show-icon
      />

      <!-- 分批看板（权重分批模式） -->
      <div v-if="isWeighted && batch.batchGroups" class="mb-4 rounded-md border border-gray-200 p-3 dark:border-gray-700">
        <div class="mb-2 text-sm font-medium">分批看板</div>
        <div class="space-y-1.5">
          <div
            v-for="g in batch.batchGroups"
            :key="g.index"
            class="flex flex-wrap items-center gap-2 rounded bg-gray-50 px-2.5 py-1.5 dark:bg-gray-800/60"
          >
            <Tag class="!m-0" :color="g.index === 0 ? 'orange' : 'geekblue'">
              {{ g.index === 0 ? '金丝雀批' : `第 ${g.index + 1} 批` }}
            </Tag>
            <Tag class="!m-0" :color="GROUP_STATUS_MAP[g.status]?.color ?? 'default'">
              {{ GROUP_STATUS_MAP[g.status]?.text ?? g.status }}
            </Tag>
            <span
              v-for="id in g.nodeIds"
              :key="id"
              class="rounded border border-gray-200 bg-white px-1.5 py-0.5 font-mono text-xs dark:border-gray-600 dark:bg-gray-900"
            >
              {{ nodeNameOf(id) }}
            </span>
            <span class="ml-auto text-xs text-gray-400">Σ权重 {{ g.weightSum }}</span>
          </div>
        </div>
      </div>

      <div class="flex flex-col divide-y divide-gray-100 dark:divide-gray-700">
        <div
          v-for="it in batch.items"
          :key="it.nodeId"
          class="flex items-start gap-3 py-2.5"
        >
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-2">
              <span class="font-medium">{{ it.nodeName }}</span>
              <Tag v-if="isWeighted" class="!m-0" color="orange">W{{ it.weight }}</Tag>
              <span class="text-xs text-gray-400">{{ it.centerName }}</span>
            </div>
            <!-- 失败原因（离线不可达 / 实例被删等，帮助运维快速定位） -->
            <div
              v-if="it.error"
              class="mt-1 text-xs leading-5 text-rose-600 dark:text-rose-400"
            >
              失败原因：{{ it.error }}
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <Tag :color="STATUS_MAP[it.status]?.color ?? 'default'">
              {{ STATUS_MAP[it.status]?.text ?? it.status }}
            </Tag>
          </div>
        </div>
      </div>
    </template>
  </Drawer>
</template>
