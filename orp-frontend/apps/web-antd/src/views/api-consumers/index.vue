<script lang="ts" setup>
import type {
  APIConsumer,
  APIKey,
  APIKeyIssue,
  APIKeyIssuePayload,
  APIConsumerPayload,
} from '#/api/orp/consumers';

import { computed, onMounted, onUnmounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import dayjs, { type Dayjs } from 'dayjs';
import {
  Alert,
  Button,
  DatePicker,
  Drawer,
  Input,
  message,
  Modal,
  Space,
  Table,
  Tag,
} from 'ant-design-vue';

import {
  createConsumerApi,
  disableConsumerApi,
  issueConsumerKeyApi,
  listConsumerKeysApi,
  listConsumersApi,
  revokeConsumerKeyApi,
  rotateConsumerKeyApi,
  updateConsumerApi,
} from '#/api/orp/consumers';

defineOptions({ name: 'SystemConsumers' });

const loading = ref(false);
const savingConsumer = ref(false);
const consumers = ref<APIConsumer[]>([]);
const page = ref(1);
const pageSize = ref(10);
const total = ref(0);

const consumerModalOpen = ref(false);
const editingConsumer = ref<APIConsumer>();
const consumerForm = reactive({ name: '', description: '' });

const keysDrawerOpen = ref(false);
const keyLoading = ref(false);
const selectedConsumer = ref<APIConsumer>();
const keys = ref<APIKey[]>([]);
const keyPage = ref(1);
const keyPageSize = ref(10);
const keyTotal = ref(0);

const issueModalOpen = ref(false);
const issuing = ref(false);
const expiration = ref<Dayjs>();
const rotatingKey = ref<APIKey>();

const secretModalOpen = ref(false);
const issuedKey = ref<APIKeyIssue>();
const issuedSecret = ref('');
let secretClearTimer: number | undefined;

const consumerColumns = [
  { title: '调用方', dataIndex: 'name', key: 'name', width: 190 },
  { title: '描述', dataIndex: 'description', key: 'description', ellipsis: true },
  { title: '状态', dataIndex: 'status', key: 'status', width: 100 },
  { title: '有效密钥', dataIndex: 'activeKeyCount', key: 'activeKeyCount', width: 110 },
  { title: '创建人', dataIndex: 'createdBy', key: 'createdBy', width: 130 },
  { title: '创建时间', dataIndex: 'createdAt', key: 'createdAt', width: 180 },
  { title: '操作', key: 'actions', fixed: 'right' as const, width: 260 },
];

const keyColumns = [
  { title: '密钥前缀', dataIndex: 'keyPrefix', key: 'keyPrefix', width: 190 },
  { title: '状态', dataIndex: 'status', key: 'status', width: 100 },
  { title: '创建时间', dataIndex: 'createdAt', key: 'createdAt', width: 180 },
  { title: '最近使用', dataIndex: 'lastUsedAt', key: 'lastUsedAt', width: 180 },
  { title: '过期时间', dataIndex: 'expiresAt', key: 'expiresAt', width: 180 },
  { title: '轮换自', dataIndex: 'rotatedFromId', key: 'rotatedFromId', width: 100 },
  { title: '操作', key: 'actions', fixed: 'right' as const, width: 190 },
];

const consumerModalTitle = computed(() =>
  editingConsumer.value ? '编辑 API 调用方' : '创建 API 调用方',
);

async function loadConsumers() {
  loading.value = true;
  try {
    const result = await listConsumersApi({ page: page.value, pageSize: pageSize.value });
    consumers.value = result.items;
    total.value = result.total;
  } catch {
    // API errors are shown by the shared request interceptor.
  } finally {
    loading.value = false;
  }
}

async function loadKeys() {
  const consumer = selectedConsumer.value;
  if (!consumer) return;
  keyLoading.value = true;
  try {
    const result = await listConsumerKeysApi(consumer.id, {
      page: keyPage.value,
      pageSize: keyPageSize.value,
    });
    keys.value = result.items;
    keyTotal.value = result.total;
  } catch {
    // API errors are shown by the shared request interceptor.
  } finally {
    keyLoading.value = false;
  }
}

function openCreateConsumer() {
  editingConsumer.value = undefined;
  consumerForm.name = '';
  consumerForm.description = '';
  consumerModalOpen.value = true;
}

function openEditConsumer(consumer: APIConsumer) {
  editingConsumer.value = consumer;
  consumerForm.name = consumer.name;
  consumerForm.description = consumer.description;
  consumerModalOpen.value = true;
}

async function saveConsumer() {
  const name = consumerForm.name.trim();
  const description = consumerForm.description.trim();
  if (!name) {
    message.warning('请填写调用方名称');
    return;
  }
  if (name.length > 100 || description.length > 500) {
    message.warning('名称最多 100 个字符，描述最多 500 个字符');
    return;
  }
  const payload: APIConsumerPayload = {
    name,
    description,
    ...(editingConsumer.value ? { status: editingConsumer.value.status } : {}),
  };
  savingConsumer.value = true;
  try {
    if (editingConsumer.value) {
      await updateConsumerApi(editingConsumer.value.id, payload);
      message.success('调用方已更新');
    } else {
      await createConsumerApi(payload);
      message.success('调用方已创建');
      page.value = 1;
    }
    consumerModalOpen.value = false;
    await loadConsumers();
    if (selectedConsumer.value?.id === editingConsumer.value?.id) {
      selectedConsumer.value = consumers.value.find((item) => item.id === selectedConsumer.value?.id) ?? selectedConsumer.value;
    }
  } catch {
    // API errors are shown by the shared request interceptor.
  } finally {
    savingConsumer.value = false;
  }
}

function openKeys(consumer: APIConsumer) {
  selectedConsumer.value = consumer;
  keyPage.value = 1;
  keysDrawerOpen.value = true;
  void loadKeys();
}

function openIssueKey(key?: APIKey) {
  if (!selectedConsumer.value || selectedConsumer.value.status !== 'enabled') return;
  rotatingKey.value = key;
  expiration.value = undefined;
  issueModalOpen.value = true;
}

function issuePayload(): APIKeyIssuePayload {
  return expiration.value ? { expiresAt: expiration.value.toISOString() } : {};
}

async function confirmIssueKey() {
  const consumer = selectedConsumer.value;
  if (!consumer) return;
  issuing.value = true;
  try {
    const result = rotatingKey.value
      ? await rotateConsumerKeyApi(consumer.id, rotatingKey.value.id, issuePayload())
      : await issueConsumerKeyApi(consumer.id, issuePayload());
    issuedKey.value = { ...result, secret: '' };
    issuedSecret.value = result.secret;
    issueModalOpen.value = false;
    secretModalOpen.value = true;
    rotatingKey.value = undefined;
    message.success(result.rotatedFromId ? '新密钥已签发' : '密钥已签发');
    await Promise.all([loadKeys(), loadConsumers()]);
  } catch {
    // API errors are shown by the shared request interceptor.
  } finally {
    issuing.value = false;
  }
}

function confirmDisableConsumer(consumer: APIConsumer) {
  Modal.confirm({
    title: `停用调用方「${consumer.name}」？`,
    content: '停用会立即撤销该调用方的全部有效 API Key，且此操作会写入审计日志。',
    okText: '停用并撤销密钥',
    okButtonProps: { danger: true },
    cancelText: '取消',
    async onOk() {
      await disableConsumerApi(consumer.id);
      message.success('调用方已停用，相关有效密钥已撤销');
      await loadConsumers();
      if (selectedConsumer.value?.id === consumer.id) {
        selectedConsumer.value = { ...selectedConsumer.value, status: 'disabled', activeKeyCount: 0 };
        await loadKeys();
      }
    },
  });
}

async function enableConsumer(consumer: APIConsumer) {
  try {
    await updateConsumerApi(consumer.id, {
      name: consumer.name,
      description: consumer.description,
      status: 'enabled',
    });
    message.success('调用方已启用');
    await loadConsumers();
  } catch {
    // API errors are shown by the shared request interceptor.
  }
}

function confirmRevokeKey(key: APIKey) {
  const consumer = selectedConsumer.value;
  if (!consumer) return;
  Modal.confirm({
    title: `撤销密钥 ${key.keyPrefix}…？`,
    content: '撤销后该密钥将不能再使用，且无法恢复。',
    okText: '确认撤销',
    okButtonProps: { danger: true },
    cancelText: '取消',
    async onOk() {
      await revokeConsumerKeyApi(consumer.id, key.id);
      message.success('密钥已撤销');
      await Promise.all([loadKeys(), loadConsumers()]);
    },
  });
}

function consumerStatus(status: APIConsumer['status']) {
  return status === 'enabled' ? '启用' : '已停用';
}

function keyStatus(key: APIKey) {
  if (key.status === 'revoked') return { color: 'default', text: '已撤销' };
  if (key.expiresAt && dayjs(key.expiresAt).isBefore(dayjs())) return { color: 'warning', text: '已过期' };
  return { color: 'success', text: '有效' };
}

function formatTime(value?: null | string) {
  return value ? dayjs(value).format('YYYY-MM-DD HH:mm:ss') : '—';
}

function asConsumer(value: unknown) {
  return value as APIConsumer;
}

function asAPIKey(value: unknown) {
  return value as APIKey;
}

function onConsumersTableChange(pagination: { current?: number; pageSize?: number }) {
  page.value = pagination.current ?? 1;
  pageSize.value = pagination.pageSize ?? 10;
  void loadConsumers();
}

function onKeysTableChange(pagination: { current?: number; pageSize?: number }) {
  keyPage.value = pagination.current ?? 1;
  keyPageSize.value = pagination.pageSize ?? 10;
  void loadKeys();
}

async function copySecret() {
  if (!issuedSecret.value) return;
  try {
    await navigator.clipboard.writeText(issuedSecret.value);
    message.success('密钥已复制，请立即保存到安全位置');
  } catch {
    message.error('复制失败，请手动选择密钥复制');
  }
}

function closeSecretModal() {
  secretModalOpen.value = false;
  if (secretClearTimer) window.clearTimeout(secretClearTimer);
  secretClearTimer = window.setTimeout(clearSecret, 300);
}

function clearSecret() {
  issuedSecret.value = '';
  issuedKey.value = undefined;
}

function handleSecretOpenChange(open: boolean) {
  secretModalOpen.value = open;
  if (!open) closeSecretModal();
}

onMounted(() => void loadConsumers());
onUnmounted(() => {
  if (secretClearTimer) window.clearTimeout(secretClearTimer);
  clearSecret();
});
</script>

<template>
  <Page
    auto-content-height
    title="API 调用方与密钥"
    description="维护 API Consumer 和 API Key 生命周期。密钥以哈希保存，完整 Secret 仅在签发或轮换时显示一次。"
  >
    <div class="flex h-full min-h-0 flex-col gap-3">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="text-sm text-gray-500">
          调用方停用会立即撤销其全部有效密钥；轮换会创建新密钥，旧密钥需在确认调用方切换后单独撤销。
        </div>
        <Button v-access:code="'orp:system:manage'" type="primary" @click="openCreateConsumer">
          新建调用方
        </Button>
      </div>

      <Table
        :columns="consumerColumns"
        :data-source="consumers"
        :loading="loading"
        :pagination="{ current: page, pageSize, total, showSizeChanger: true, showTotal: (count: number) => `共 ${count} 个调用方` }"
        :row-key="(record: APIConsumer) => record.id"
        :scroll="{ x: 1100 }"
        @change="onConsumersTableChange"
      >
        <template #bodyCell="{ column, record }">
          <template v-if="column.key === 'status'">
            <Tag :color="record.status === 'enabled' ? 'success' : 'default'">{{ consumerStatus(record.status) }}</Tag>
          </template>
          <template v-else-if="column.key === 'createdAt'">{{ formatTime(record.createdAt) }}</template>
          <template v-else-if="column.key === 'actions'">
            <Space :size="2">
              <Button size="small" type="link" @click="openKeys(asConsumer(record))">密钥</Button>
              <Button v-access:code="'orp:system:manage'" size="small" type="link" @click="openEditConsumer(asConsumer(record))">编辑</Button>
              <Button
                v-if="record.status === 'enabled'"
                v-access:code="'orp:system:manage'"
                danger
                size="small"
                type="link"
                @click="confirmDisableConsumer(asConsumer(record))"
              >停用</Button>
              <Button
                v-else
                v-access:code="'orp:system:manage'"
                size="small"
                type="link"
                @click="enableConsumer(asConsumer(record))"
              >启用</Button>
            </Space>
          </template>
        </template>
      </Table>
    </div>

    <Modal
      v-model:open="consumerModalOpen"
      :confirm-loading="savingConsumer"
      :title="consumerModalTitle"
      ok-text="保存"
      cancel-text="取消"
      @ok="saveConsumer"
    >
      <div class="space-y-4 py-2">
        <label class="block space-y-1">
          <span class="text-sm font-medium">名称</span>
          <Input v-model:value="consumerForm.name" :maxlength="100" show-count placeholder="例如：移动客户端" />
        </label>
        <label class="block space-y-1">
          <span class="text-sm font-medium">描述</span>
          <Input.TextArea v-model:value="consumerForm.description" :maxlength="500" :rows="3" show-count placeholder="说明该调用方的用途" />
        </label>
      </div>
    </Modal>

    <Drawer
      v-model:open="keysDrawerOpen"
      :title="selectedConsumer ? `API Key · ${selectedConsumer.name}` : 'API Key'"
      :width="980"
      destroy-on-close
    >
      <div class="space-y-4">
        <Alert
          message="Secret 只在签发和轮换成功后显示一次。轮换不会自动撤销旧密钥；请在调用方切换完成后手动撤销旧密钥。"
          type="info"
          show-icon
        />
        <div class="flex justify-end">
          <Button
            v-access:code="'orp:system:manage'"
            type="primary"
            :disabled="selectedConsumer?.status !== 'enabled'"
            @click="openIssueKey()"
          >签发 API Key</Button>
        </div>
        <Table
          :columns="keyColumns"
          :data-source="keys"
          :loading="keyLoading"
          :pagination="{ current: keyPage, pageSize: keyPageSize, total: keyTotal, showSizeChanger: true, showTotal: (count: number) => `共 ${count} 个密钥` }"
          :row-key="(record: APIKey) => record.id"
          :scroll="{ x: 1150 }"
          @change="onKeysTableChange"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'status'">
                  <Tag :color="keyStatus(asAPIKey(record)).color">{{ keyStatus(asAPIKey(record)).text }}</Tag>
            </template>
            <template v-else-if="column.key === 'createdAt'">{{ formatTime(record.createdAt) }}</template>
            <template v-else-if="column.key === 'lastUsedAt'">{{ formatTime(record.lastUsedAt) }}</template>
            <template v-else-if="column.key === 'expiresAt'">{{ formatTime(record.expiresAt) }}</template>
            <template v-else-if="column.key === 'rotatedFromId'">{{ record.rotatedFromId ?? '—' }}</template>
            <template v-else-if="column.key === 'actions'">
              <Space :size="2">
                <Button
                  v-if="record.status === 'active' && selectedConsumer?.status === 'enabled'"
                  v-access:code="'orp:system:manage'"
                  size="small"
                  type="link"
                  @click="openIssueKey(asAPIKey(record))"
                >轮换</Button>
                <Button
                  v-if="record.status === 'active'"
                  v-access:code="'orp:system:manage'"
                  danger
                  size="small"
                  type="link"
                  @click="confirmRevokeKey(asAPIKey(record))"
                >撤销</Button>
              </Space>
            </template>
          </template>
        </Table>
      </div>
    </Drawer>

    <Modal
      v-model:open="issueModalOpen"
      :confirm-loading="issuing"
      :title="rotatingKey ? '轮换 API Key' : '签发 API Key'"
      ok-text="确认签发"
      cancel-text="取消"
      @ok="confirmIssueKey"
    >
      <div class="space-y-4 py-2">
        <Alert
          v-if="rotatingKey"
          message="此操作创建一把关联旧密钥的新密钥，旧密钥仍有效。确认新密钥已部署至调用方后，再单独撤销旧密钥。"
          type="warning"
          show-icon
        />
        <label class="block space-y-1">
          <span class="text-sm font-medium">过期时间</span>
          <DatePicker
            v-model:value="expiration"
            class="w-full"
            show-time
            :disabled-date="(date: Dayjs) => date.isBefore(dayjs().startOf('day'))"
            placeholder="不设置表示永不过期"
          />
        </label>
      </div>
    </Modal>

    <Modal
      :open="secretModalOpen"
      :title="issuedKey ? `密钥已签发 · ${issuedKey.keyPrefix}…` : '密钥已签发'"
      :mask-closable="false"
      @cancel="closeSecretModal"
      @after-close="clearSecret"
      @update:open="handleSecretOpenChange"
    >
      <template #footer>
        <Button type="primary" @click="closeSecretModal">我已安全保存，关闭</Button>
      </template>
      <div class="space-y-3 py-2">
        <Alert
          message="完整 Secret 只显示这一次。请立即复制并存入安全凭据系统；关闭此窗口后页面内存中的 Secret 会清除，之后无法再次查看。"
          type="warning"
          show-icon
        />
        <Input.TextArea :value="issuedSecret" readonly :rows="3" class="font-mono" />
        <Button type="primary" @click="copySecret">复制完整密钥</Button>
      </div>
    </Modal>
  </Page>
</template>
