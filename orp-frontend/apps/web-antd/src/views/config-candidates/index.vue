<script lang="ts" setup>
import type { CandidateDetail, CandidateJob, ConfigurationCandidate } from '#/api/orp/candidates';
import type { Center, NodeItem } from '#/api/orp/types';
import type { PublishStrategy } from '#/api/orp/publish';

import { computed, onMounted, ref, watch } from 'vue';
import { Page } from '@vben/common-ui';
import { message } from 'ant-design-vue';
import { Button, Card, Checkbox, Descriptions, DescriptionsItem, Empty, InputNumber, Modal, Select, Spin, Tag } from 'ant-design-vue';
import { listCentersApi, listNodesApi } from '#/api';
import { useRouter } from 'vue-router';
import { useAccessStore } from '@vben/stores';
import {
  actOnConfigurationCandidateApi, createConfigurationCandidateApi, getConfigurationCandidateApi,
  getValidationCenterPolicyApi, listConfigurationCandidatesApi, publishConfigurationCandidateApi, updateValidationCenterPolicyApi,
} from '#/api/orp/candidates';

const centers = ref<Center[]>([]);
const nodes = ref<NodeItem[]>([]);
const centerId = ref<number>();
const records = ref<ConfigurationCandidate[]>([]);
const archived = ref(false);
const loading = ref(false);
const policy = ref<number>();
const policyDefault = ref(false);
const policyLoading = ref(false);
const policySaving = ref(false);
const createOpen = ref(false);
const createBusy = ref(false);
const newCenterId = ref<number>();
const nodeIds = ref<number[]>([]);
const concurrency = ref(1);
const maxInflight = ref(1);
const createPolicyDefault = ref(false);
const detailOpen = ref(false);
const detailBusy = ref(false);
const detail = ref<CandidateDetail>();
const retryOpen = ref(false);
const retryNode = ref<number>();
const retryCandidate = ref<Pick<CandidateDetail, 'id' | 'publishOnValidationSuccess'>>();
const actionBusy = ref(false);
const router = useRouter();
const accessStore = useAccessStore();
const canWriteConfig = computed(() => accessStore.accessCodes.includes('*') || accessStore.accessCodes.includes('orp:config:write'));
const canPublish = computed(() => accessStore.accessCodes.includes('*') || accessStore.accessCodes.includes('orp:publish'));
const autoPublish = ref(false);
const autoPublishMode = ref<'all' | 'weighted'>('all');
const autoPublishBatches = ref(2);
const autoPublishCanary = ref(false);
const autoPublishWait = ref(60);
const centerOptions = computed(() => centers.value.map(({ id, name }) => ({ label: name, value: id })));
const nodeOptions = computed(() => nodes.value.map(({ id, name, host }) => ({ label: `${name} · ${host}`, value: id })));
const latestAttempt = computed(() => detail.value?.attempts.at(-1));

const labels: Record<string, string> = {
  adapter_required: '缺少校验适配器', cancelled: '已取消', failed: '失败', paused: '已暂停',
  passed: '通过', queued: '排队中', running: '运行中', stopped: '已停止', unknown: '结果未知',
};
function stateLabel(value?: null | string) { return value ? labels[value] ?? value : '暂无尝试'; }
function stateColor(value?: null | string) {
  if (value === 'passed') return 'green';
  if (['failed', 'stopped', 'cancelled'].includes(value ?? '')) return 'red';
  if (['adapter_required', 'unknown'].includes(value ?? '')) return 'orange';
  if (value === 'running') return 'blue';
  return 'default';
}
function publishStateLabel(value: string) {
  const labels: Record<string, string> = {
    aborted: '发布批次已中止', failed: '发布失败或未启动', not_requested: '未请求自动发布', preparing: '正在创建发布批次', recovery_required: '批次需要人工核对',
    started: '发布批次已创建或执行中', succeeded: '发布成功', waiting_validation: '等待校验全部通过',
  };
  return labels[value] ?? value;
}
function publishStateColor(value: string) {
  if (value === 'succeeded') return 'green';
  if (value === 'failed' || value === 'aborted' || value === 'recovery_required') return 'red';
  if (value === 'preparing' || value === 'started') return 'blue';
  return 'default';
}
function canManuallyPublish(row: Pick<ConfigurationCandidate, 'attemptState' | 'publishState' | 'publishBatchId'>) {
  return row.attemptState === 'completed' && row.publishBatchId === null && row.publishState !== 'preparing' && row.publishState !== 'waiting_validation';
}
function openPublishBatch(batchId: number) {
  void router.push({ name: 'ConfigPublish', query: { batchId: String(batchId) } });
}
async function loadList() {
  loading.value = true;
  try {
    const result = await listConfigurationCandidatesApi({ archived: archived.value || undefined, centerId: centerId.value, page: 1, pageSize: 100 });
    // The published OpenAPI contract defines archived=true as archived-only.
    // Keep the UI truthful if a backend revision returns both states.
    records.value = result.items.filter((item) => item.archived === archived.value);
  } catch { records.value = []; }
  finally { loading.value = false; }
}
async function loadPolicy(id: number, forCreate = false) {
  if (!forCreate) policyLoading.value = true;
  try {
    const result = await getValidationCenterPolicyApi(id);
    if (forCreate) {
      maxInflight.value = result.maxInflight;
      concurrency.value = Math.min(concurrency.value, result.maxInflight);
      createPolicyDefault.value = Boolean(result.default);
    } else {
      policy.value = result.maxInflight;
      policyDefault.value = Boolean(result.default);
    }
  } catch { if (!forCreate) policy.value = undefined; }
  finally { if (!forCreate) policyLoading.value = false; }
}
async function loadNodes(id: number) {
  try { nodes.value = (await listNodesApi({ centerId: id, page: 1, pageSize: 200 })).items; }
  catch { nodes.value = []; }
}
watch(centerId, (id) => { void loadList(); if (id) void loadPolicy(id); else policy.value = undefined; });
watch(newCenterId, (id) => { nodeIds.value = []; nodes.value = []; if (id) void Promise.all([loadNodes(id), loadPolicy(id, true)]); });
watch(nodeIds, (ids) => {
  if (ids.length > 0 && autoPublishBatches.value > ids.length) autoPublishBatches.value = Math.min(5, ids.length);
}, { deep: true });
async function init() {
  try {
    centers.value = (await listCentersApi({ page: 1, pageSize: 200 })).items;
    centerId.value = centers.value[0]?.id;
  } catch { centers.value = []; }
}
async function savePolicy() {
  if (!centerId.value || policy.value === undefined) return;
  policySaving.value = true;
  try {
    const saved = await updateValidationCenterPolicyApi(centerId.value, policy.value);
    policy.value = saved.maxInflight;
    policyDefault.value = Boolean(saved.default);
    message.success('中心校验并发上限已保存');
  } catch { /* 统一请求拦截器负责展示 4xx/RBAC 错误。 */ }
  finally { policySaving.value = false; }
}
async function openCreate() {
  newCenterId.value = centerId.value;
  concurrency.value = 1;
  nodeIds.value = [];
  autoPublish.value = false;
  autoPublishMode.value = 'all';
  autoPublishBatches.value = 2;
  autoPublishCanary.value = false;
  autoPublishWait.value = 60;
  createOpen.value = true;
  if (centerId.value) await Promise.all([loadNodes(centerId.value), loadPolicy(centerId.value, true)]);
}
async function createCandidate() {
  if (!newCenterId.value || nodeIds.value.length === 0) { message.warning('请选择中心和至少一个目标节点'); return; }
  createBusy.value = true;
  try {
    const publishStrategy: PublishStrategy | undefined = autoPublish.value
      ? {
          mode: autoPublishMode.value,
          ...(autoPublishMode.value === 'weighted' ? { batchCount: autoPublishBatches.value } : {}),
          canary: { enabled: autoPublishMode.value === 'weighted' && autoPublishCanary.value, waitSeconds: autoPublishWait.value },
        }
      : undefined;
    const created = await createConfigurationCandidateApi({
      centerId: newCenterId.value,
      nodeIds: nodeIds.value,
      concurrency: concurrency.value,
      publishOnValidationSuccess: autoPublish.value,
      publishStrategy,
    });
    message.success(`候选 #${created.id} 已创建，状态：${stateLabel(created.status)}`);
    createOpen.value = false;
    centerId.value = newCenterId.value;
    await loadList();
    await openDetail(created.id);
  } catch { /* 保留表单；统一请求拦截器展示权限或状态冲突。 */ }
  finally { createBusy.value = false; }
}
function confirmManualPublish(row: Pick<ConfigurationCandidate, 'id' | 'attemptState' | 'publishState' | 'publishBatchId'>) {
  if (!canManuallyPublish(row)) return;
  Modal.confirm({
    title: `确认发布已通过的候选 #${row.id}？`,
    content: '将使用当前冻结快照创建发布批次。此操作会向目标节点发布配置并可能触发 reload；后端会再次核验全部节点校验结果、候选快照、目标节点、发布基线和当前发布权限。',
    okText: '确认创建发布批次',
    cancelText: '取消',
    onOk: () => manualPublish(row.id),
  });
}
async function manualPublish(id: number) {
  actionBusy.value = true;
  try {
    const result = await publishConfigurationCandidateApi(id);
    message.success(`发布批次 #${result.batchId} 已创建，共 ${result.total} 个目标节点`);
    await loadList();
    if (detail.value?.id === id) await refreshDetail();
  } catch { /* 统一请求拦截器显示权限、校验或基线冲突。 */ }
  finally { actionBusy.value = false; }
}
async function openDetail(id: number) {
  detailOpen.value = true;
  detailBusy.value = true;
  detail.value = undefined;
  try { detail.value = await getConfigurationCandidateApi(id); }
  catch { detailOpen.value = false; }
  finally { detailBusy.value = false; }
}
async function refreshDetail() {
  if (!detail.value) return;
  detailBusy.value = true;
  try { detail.value = await getConfigurationCandidateApi(detail.value.id); }
  catch { /* 统一拦截器展示错误。 */ }
  finally { detailBusy.value = false; }
}
async function candidateAction(row: Pick<ConfigurationCandidate, 'id'>, action: 'archive' | 'restore' | 'continue' | 'stop') {
  actionBusy.value = true;
  try {
    await actOnConfigurationCandidateApi(row.id, action);
    await loadList();
    if (detail.value?.id === row.id) await refreshDetail();
  } catch { /* 服务端负责最终状态与 RBAC 校验。 */ }
  finally { actionBusy.value = false; }
}
function startRetry(row: Pick<CandidateDetail, 'id' | 'publishOnValidationSuccess'>, job: CandidateJob) {
  retryCandidate.value = row;
  retryNode.value = job.nodeId;
  retryOpen.value = true;
}
async function retry() {
  if (!retryCandidate.value || !retryNode.value) return;
  actionBusy.value = true;
  try {
    await actOnConfigurationCandidateApi(retryCandidate.value.id, 'retry', { nodeId: retryNode.value });
    retryOpen.value = false;
    await loadList();
    await refreshDetail();
  } catch { /* 仅失败/未知任务可重试，服务端响应为准。 */ }
  finally { actionBusy.value = false; }
}
onMounted(init);
</script>

<template>
  <Page auto-content-height title="配置候选与校验" description="冻结配置快照并查看目标节点校验证据；候选校验本身不会激活配置，发布需显式授权并确认。">
    <div class="flex h-full min-h-0 flex-col gap-4 overflow-auto">
      <Card title="中心校验策略" size="small">
        <div class="flex flex-wrap items-center gap-3">
          <Select v-model:value="centerId" :options="centerOptions" allow-clear placeholder="选择中心" show-search option-filter-prop="label" style="width: 240px" />
          <Spin :spinning="policyLoading" size="small" />
          <template v-if="centerId && policy !== undefined">
            <span>每中心最大并发</span><InputNumber v-model:value="policy" :min="1" :max="32" :disabled="!canWriteConfig" />
            <Tag v-if="policyDefault" color="blue">当前使用默认值</Tag>
            <Button v-access:code="'orp:config:write'" type="primary" :loading="policySaving" @click="savePolicy">保存策略</Button>
          </template>
          <span class="text-xs text-gray-500">范围 1–32；降低上限只影响后续调度，不中断已运行任务。</span>
        </div>
      </Card>
      <Card class="min-h-0 flex-1" size="small">
        <template #title>不可变候选</template>
        <template #extra><div class="flex items-center gap-2"><Checkbox v-model:checked="archived" @change="loadList">仅看已归档</Checkbox><Button @click="loadList">刷新</Button><Button v-access:code="'orp:config:write'" type="primary" :disabled="!centerId" @click="openCreate">创建候选</Button></div></template>
        <Spin :spinning="loading">
          <div v-if="records.length" class="grid grid-cols-1 gap-3 xl:grid-cols-2">
            <Card v-for="row in records" :key="row.id" size="small">
              <div class="flex flex-col gap-3">
                <div class="flex flex-wrap items-center justify-between gap-2"><div class="flex items-center gap-2"><strong>候选 #{{ row.id }}</strong><Tag :color="stateColor(row.attemptState)">{{ stateLabel(row.attemptState) }}</Tag><Tag v-if="row.publishOnValidationSuccess" color="purple">校验通过后自动发布</Tag><Tag v-if="row.archived">已归档</Tag><Tag v-if="row.blocked" color="orange">阻塞</Tag></div><span class="text-xs text-gray-500">{{ row.createdAt }}</span></div>
                <div class="break-all text-xs text-gray-600">{{ row.candidateUuid }} · 中心 {{ row.centerId }} · {{ row.creator }}</div>
                <div class="break-all font-mono text-xs">SHA-256 {{ row.digest }}</div>
                <div class="flex justify-between gap-3 text-sm"><span>目标节点：{{ row.targetIds.join(', ') }}</span><span>尝试 #{{ row.attemptNo ?? '—' }} · 并发 {{ row.concurrency ?? '—' }}</span></div>
                <div class="flex flex-wrap items-center gap-2 text-xs"><span class="text-gray-500">发布状态：</span><Tag :color="publishStateColor(row.publishState)">{{ publishStateLabel(row.publishState) }}</Tag><span v-if="row.publishError" class="text-red-600">{{ row.publishError }}</span><Button v-if="row.publishBatchId" type="link" size="small" @click="openPublishBatch(row.publishBatchId)">打开批次 #{{ row.publishBatchId }}</Button></div>
                <div v-if="row.publishOnValidationSuccess && row.publishState === 'waiting_validation'" class="text-xs text-gray-500">只有全部冻结目标校验通过才会启动自动发布；失败或结果未知不会自动发布。</div>
                <div v-if="row.blocker" class="rounded bg-orange-50 px-3 py-2 text-xs text-orange-700 dark:bg-orange-950 dark:text-orange-300">{{ row.blocker }}</div>
                <div class="flex justify-end gap-2 border-t pt-2"><Button v-if="canManuallyPublish(row)" v-access:code="'orp:publish'" type="primary" ghost :loading="actionBusy" @click="confirmManualPublish(row)">手动发布</Button><Button type="link" @click="openDetail(row.id)">详情 / Diff</Button><Button v-if="row.archived" v-access:code="'orp:config:write'" type="link" :loading="actionBusy" @click="candidateAction(row, 'restore')">恢复</Button><Button v-else v-access:code="'orp:config:write'" type="link" :loading="actionBusy" @click="candidateAction(row, 'archive')">归档</Button></div>
              </div>
            </Card>
          </div>
          <Empty v-else-if="!loading" description="此筛选条件下没有候选" />
        </Spin>
      </Card>
    </div>

    <Modal v-model:open="createOpen" title="创建不可变配置候选" :confirm-loading="createBusy" ok-text="冻结并排队校验" width="640px" @ok="createCandidate">
      <div class="space-y-4 py-2">
        <div class="flex items-center gap-3"><span class="w-24 shrink-0">中心</span><Select v-model:value="newCenterId" :options="centerOptions" placeholder="选择中心" show-search option-filter-prop="label" class="w-full" /></div>
        <div class="flex items-start gap-3"><span class="w-24 shrink-0 pt-1">目标节点</span><Select v-model:value="nodeIds" :options="nodeOptions" :disabled="!newCenterId" mode="multiple" placeholder="选择冻结目标节点" show-search option-filter-prop="label" class="w-full" /></div>
        <div class="flex items-center gap-3"><span class="w-24 shrink-0">尝试并发</span><InputNumber v-model:value="concurrency" :min="1" :max="maxInflight" /><span class="text-xs text-gray-500">中心上限 {{ maxInflight }}<template v-if="createPolicyDefault">（默认值）</template></span></div>
        <div class="rounded border p-3">
          <Checkbox v-model:checked="autoPublish" :disabled="!canPublish">校验全部通过后自动创建发布批次</Checkbox>
          <div class="mt-1 text-xs text-gray-500">默认关闭。失败或结果未知不会触发自动发布；启用后必须最新一次校验完成且所有冻结目标均通过。自动发布仍受当前权限、快照、目标及基线复核约束。<template v-if="!canPublish">当前账号没有发布权限，无法启用此选项。</template></div>
          <div v-if="autoPublish" class="mt-3 flex flex-wrap items-center gap-3">
            <span>发布方式</span><Select v-model:value="autoPublishMode" :options="[{ label: '全量', value: 'all' }, { label: '权重分批', value: 'weighted' }]" style="width: 140px" />
            <template v-if="autoPublishMode === 'weighted'"><span>批次数</span><InputNumber v-model:value="autoPublishBatches" :min="1" :max="Math.max(1, Math.min(5, nodeIds.length))" /><Checkbox v-model:checked="autoPublishCanary">启用金丝雀观察</Checkbox><template v-if="autoPublishCanary"><span>观察秒数</span><InputNumber v-model:value="autoPublishWait" :min="1" :max="300" /></template></template>
            <span v-else class="text-xs text-gray-500">金丝雀观察仅适用于权重分批。</span>
          </div>
        </div>
        <div class="rounded bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:bg-blue-950 dark:text-blue-300">提交会冻结配置并排队校验。自动发布默认关闭；启用后也只有全部目标校验通过才创建发布批次，失败或结果未知不会自动发布。</div>
      </div>
    </Modal>

    <a-drawer v-model:open="detailOpen" title="候选详情与校验证据" width="min(960px, 92vw)" destroy-on-close>
      <Spin :spinning="detailBusy">
        <template v-if="detail">
          <div class="mb-4 flex flex-wrap items-center justify-between gap-2"><div class="flex items-center gap-2"><strong>候选 #{{ detail.id }}</strong><Tag :color="stateColor(latestAttempt?.state)">{{ stateLabel(latestAttempt?.state) }}</Tag><Tag v-if="detail.archived">已归档</Tag></div><div class="flex gap-2"><Button @click="refreshDetail">刷新状态</Button><Button v-if="latestAttempt?.state === 'completed' && canManuallyPublish({ attemptState: latestAttempt.state, publishState: detail.publishState, publishBatchId: detail.publishBatchId })" v-access:code="'orp:publish'" type="primary" ghost :loading="actionBusy" @click="confirmManualPublish({ id: detail.id, attemptState: latestAttempt.state, publishState: detail.publishState, publishBatchId: detail.publishBatchId })">手动发布</Button><Button v-if="latestAttempt?.state === 'paused'" v-access:code="'orp:config:write'" @click="candidateAction(detail, 'continue')">继续校验</Button><Button v-if="latestAttempt?.state === 'paused'" v-access:code="'orp:config:write'" danger @click="candidateAction(detail, 'stop')">停止校验</Button></div></div>
          <Descriptions bordered size="small" :column="2"><DescriptionsItem label="候选 UUID">{{ detail.candidateUuid }}</DescriptionsItem><DescriptionsItem label="中心">{{ detail.centerId }}</DescriptionsItem><DescriptionsItem label="创建人">{{ detail.creator }}</DescriptionsItem><DescriptionsItem label="创建时间">{{ detail.createdAt }}</DescriptionsItem><DescriptionsItem label="摘要" :span="2"><span class="break-all font-mono">{{ detail.digest }}</span></DescriptionsItem><DescriptionsItem label="冻结目标节点" :span="2">{{ detail.targetIds.join(', ') }}</DescriptionsItem></Descriptions>
          <Card class="mt-4" size="small" title="发布意图与批次"><div class="flex flex-wrap items-center gap-2"><Tag :color="detail.publishOnValidationSuccess ? 'purple' : 'default'">{{ detail.publishOnValidationSuccess ? '校验通过后自动发布：已启用' : '校验通过后自动发布：未启用' }}</Tag><Tag :color="publishStateColor(detail.publishState)">{{ publishStateLabel(detail.publishState) }}</Tag><Button v-if="detail.publishBatchId" type="link" @click="openPublishBatch(detail.publishBatchId)">打开发布批次 #{{ detail.publishBatchId }}</Button></div><div v-if="detail.publishError" class="mt-2 text-sm text-red-600">{{ detail.publishError }}</div><div class="mt-2 text-xs text-gray-500">失败或结果未知的校验不会自动触发发布。显式发布前服务端会重新校验权限、快照、目标节点与发布基线。</div></Card>
          <h3 class="mb-2 mt-6 font-semibold">校验尝试与节点任务</h3><Empty v-if="detail.attempts.length === 0" description="服务端未返回校验尝试" />
          <Card v-for="attempt in detail.attempts" :key="attempt.id" class="mb-3" size="small"><template #title>尝试 #{{ attempt.attemptNo }} · <Tag :color="stateColor(attempt.state)">{{ stateLabel(attempt.state) }}</Tag> · 并发 {{ attempt.concurrency }}</template><template #extra>{{ attempt.createdAt }}<span v-if="attempt.finishedAt"> → {{ attempt.finishedAt }}</span></template>
            <div v-if="attempt.blocker" class="mb-3 rounded bg-orange-50 px-3 py-2 text-xs text-orange-700 dark:bg-orange-950 dark:text-orange-300">{{ attempt.blocker }}</div>
            <div v-for="job in attempt.jobs" :key="`${attempt.id}-${job.nodeId}`" class="mb-3 rounded border p-3 last:mb-0"><div class="flex flex-wrap items-center justify-between gap-2"><div class="flex items-center gap-2"><strong>节点 {{ job.nodeId }}</strong><Tag :color="stateColor(job.state)">{{ stateLabel(job.state) }}</Tag></div><Button v-if="attempt.id === latestAttempt?.id && (job.state === 'failed' || job.state === 'unknown')" v-access:code="'orp:config:write'" size="small" type="link" @click="startRetry(detail, job)">重试该节点</Button></div><div v-if="job.blocker" class="mt-2 text-xs text-orange-600">{{ job.blocker }}</div><div v-if="job.command" class="mt-2 break-all font-mono text-xs">命令：{{ job.command }}</div><pre v-if="job.output" class="mt-2 max-h-56 overflow-auto whitespace-pre-wrap rounded bg-gray-50 p-3 text-xs dark:bg-gray-900">{{ job.output }}</pre><div class="mt-2 text-xs text-gray-500">开始：{{ job.startedAt || '—' }} · 完成：{{ job.finishedAt || '—' }}</div></div>
          </Card>
          <h3 class="mb-2 mt-6 font-semibold">冻结基线 Diff</h3><div v-if="detail.diffs === undefined" class="rounded border border-orange-200 bg-orange-50 p-3 text-sm text-orange-700 dark:bg-orange-950 dark:text-orange-300">API 未返回 Diff。需要该中心配置写入权限才能查看冻结文本 Diff。</div><Empty v-else-if="detail.diffs.length === 0" description="服务端未返回基线 Diff 条目" />
          <Card v-for="diff in detail.diffs" :key="diff.nodeId" class="mb-3" size="small"><template #title>节点 {{ diff.nodeId }} · +{{ diff.addedLines }} / −{{ diff.deletedLines }}</template><template #extra>基线 {{ diff.baselineVersion || '无已发布版本' }}</template><div class="mb-2 break-all font-mono text-xs text-gray-500">baseline {{ diff.baselineDigest || '—' }} → candidate {{ diff.candidateDigest }}</div><details v-if="diff.semanticDiff.length" class="mb-3 text-xs"><summary class="cursor-pointer">语义变化 {{ diff.semanticDiff.length }} 项</summary><pre class="mt-2 overflow-auto rounded bg-gray-50 p-3 dark:bg-gray-900">{{ JSON.stringify(diff.semanticDiff, null, 2) }}</pre></details><pre class="max-h-96 overflow-auto rounded bg-gray-50 p-3 text-xs leading-5 dark:bg-gray-900"><span v-for="(line, index) in diff.textDiff" :key="index" :class="line.t === 1 ? 'text-green-700 dark:text-green-300' : line.t === -1 ? 'text-red-700 dark:text-red-300' : 'text-gray-600 dark:text-gray-300'">{{ line.t === 1 ? '+ ' : line.t === -1 ? '− ' : '  ' }}{{ line.s }}
</span></pre></Card>
          <div class="mt-5 rounded bg-blue-50 px-3 py-2 text-xs text-blue-700 dark:bg-blue-950 dark:text-blue-300">校验通过只表示冻结候选通过检查。只有创建时显式启用了自动发布意图，且最新一次校验的所有冻结目标均通过，系统才会尝试创建发布批次；否则需由有发布权限的操作员手动确认。校验本身不会激活配置。</div>
        </template>
      </Spin>
    </a-drawer>
    <Modal v-model:open="retryOpen" title="创建校验重试尝试" :confirm-loading="actionBusy" ok-text="重试" @ok="retry"><p v-if="retryCandidate?.publishOnValidationSuccess">启用了校验通过后自动发布，将创建新的全目标校验尝试并重新验证所有冻结目标，避免复用旧通过结果触发发布。</p><p v-else>将为节点 {{ retryNode }} 追加新的校验证据。服务端会再次确认该节点仍处于可重试状态。</p></Modal>
  </Page>
</template>
