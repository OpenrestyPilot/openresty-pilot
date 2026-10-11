<script lang="ts" setup>
import type {
  AlertChannel,
  AlertChannelPayload,
  AlertChannelType,
  AlertMessageFormat,
  AlertDelivery,
  TLSAlertRule,
  TLSAlertRulePayload,
} from '#/api';

import { computed, onMounted, reactive, ref } from 'vue';

import { Page } from '@vben/common-ui';

import {
  Button,
  Form,
  FormItem,
  Input,
  InputNumber,
  InputPassword,
  message,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
} from 'ant-design-vue';
import dayjs from 'dayjs';

import {
  checkTLSAlertsApi,
  createAlertChannelApi,
  deleteAlertChannelApi,
  listAlertChannelsApi,
  listAlertDeliveriesApi,
  listTLSAlertRulesApi,
  saveTLSAlertRuleApi,
  testAlertChannelApi,
  updateAlertChannelApi,
} from '#/api';

defineOptions({ name: 'AlertNotifications' });

const loading = ref(false);
const savingChannel = ref(false);
const checkingAlerts = ref(false);
const testingChannelId = ref<number>();
const channels = ref<AlertChannel[]>([]);
const rules = ref<TLSAlertRule[]>([]);
const deliveries = ref<AlertDelivery[]>([]);
const ruleDrafts = reactive<Record<number, TLSAlertRulePayload>>({});
const channelModalOpen = ref(false);
const channelFormRef = ref();
const editingChannelId = ref<number>();
const channelForm = reactive({
  enabled: true,
  messageFormat: 'text' as AlertMessageFormat,
  name: '',
  secret: '',
  type: 'webhook' as AlertChannelType,
  webhookUrl: '',
});

const channelOptions = computed(() =>
  channels.value.map((channel) => ({
    label: `${channel.name}${channel.enabled ? '' : '（已停用）'}`,
    value: channel.id,
  })),
);

const enabledRuleCount = computed(
  () => rules.value.filter((item) => item.enabled).length,
);

const channelColumns = [
  { title: '通道名称', dataIndex: 'name', key: 'name', width: 180 },
  { title: '类型', dataIndex: 'type', key: 'type', width: 130 },
  { title: '推送地址', dataIndex: 'targetHint', key: 'targetHint' },
  { title: '状态', dataIndex: 'enabled', key: 'enabled', width: 110 },
  { title: '操作', key: 'actions', width: 230 },
];

const ruleColumns = [
  { title: '证书 / 域名', key: 'certificate', width: 260 },
  { title: '到期时间', key: 'expires', width: 180 },
  { title: '启用提醒', key: 'enabled', width: 110 },
  { title: '提前天数', key: 'daysBefore', width: 130 },
  { title: '关联推送通道', key: 'channels', width: 340 },
  { title: '操作', key: 'actions', width: 90 },
];

const deliveryColumns = [
  { title: '发送时间', dataIndex: 'createdAt', key: 'createdAt', width: 180 },
  { title: '事件', dataIndex: 'eventType', key: 'eventType', width: 140 },
  { title: '通知通道', dataIndex: 'channelName', key: 'channelName', width: 180 },
  { title: '证书', dataIndex: 'certificateName', key: 'certificateName', width: 180 },
  { title: '结果', dataIndex: 'status', key: 'status', width: 110 },
  { title: '尝试次数', dataIndex: 'attemptCount', key: 'attemptCount', width: 100 },
  { title: '消息内容', dataIndex: 'content', key: 'content', ellipsis: true },
];

function getRuleDraft(rule: TLSAlertRule): TLSAlertRulePayload {
  if (!ruleDrafts[rule.certificateId]) {
    ruleDrafts[rule.certificateId] = {
      channelIds: [...rule.channelIds],
      daysBefore: rule.daysBefore || 30,
      enabled: rule.enabled,
    };
  }
  return ruleDrafts[rule.certificateId]!;
}

function asAlertChannel(record: unknown): AlertChannel {
  return record as AlertChannel;
}

function asTLSAlertRule(record: unknown): TLSAlertRule {
  return record as TLSAlertRule;
}

async function loadData() {
  loading.value = true;
  try {
    const [channelData, ruleData, deliveryData] = await Promise.all([
      listAlertChannelsApi(),
      listTLSAlertRulesApi(),
      listAlertDeliveriesApi(30),
    ]);
    channels.value = channelData;
    rules.value = ruleData;
    deliveries.value = deliveryData.items;
    for (const key of Object.keys(ruleDrafts)) {
      delete ruleDrafts[Number(key)];
    }
    for (const rule of ruleData) getRuleDraft(rule);
  } catch {
    // 请求拦截器统一提示错误。
  } finally {
    loading.value = false;
  }
}

function openCreateChannel() {
  editingChannelId.value = undefined;
  Object.assign(channelForm, {
    enabled: true,
    messageFormat: 'text',
    name: '',
    secret: '',
    type: 'webhook',
    webhookUrl: '',
  });
  channelModalOpen.value = true;
}

function openEditChannel(channel: AlertChannel) {
  editingChannelId.value = channel.id;
  Object.assign(channelForm, {
    enabled: channel.enabled,
    messageFormat: channel.messageFormat || 'text',
    name: channel.name,
    secret: '',
    type: channel.type,
    webhookUrl: '',
  });
  channelModalOpen.value = true;
}

function changeChannelType(value: unknown) {
  if (value !== 'webhook' && value !== 'feishu') return;
  const type: AlertChannelType = value;
  if (channelForm.type !== type) {
    channelForm.messageFormat = type === 'feishu' ? 'card' : 'text';
  }
  channelForm.type = type;
}

async function saveChannel() {
  try {
    await channelFormRef.value?.validate();
  } catch {
    return;
  }
  if (!editingChannelId.value && !channelForm.webhookUrl.trim()) {
    message.warning('请填写 Webhook 地址');
    return;
  }
  savingChannel.value = true;
  const payload: AlertChannelPayload = {
    enabled: channelForm.enabled,
    name: channelForm.name.trim(),
    messageFormat: channelForm.type === 'feishu' ? channelForm.messageFormat : 'text',
    secret: channelForm.secret.trim() || undefined,
    type: channelForm.type,
    webhookUrl: channelForm.webhookUrl.trim() || undefined,
  };
  try {
    if (editingChannelId.value) {
      await updateAlertChannelApi(editingChannelId.value, payload);
      message.success('告警通道已更新');
    } else {
      await createAlertChannelApi(payload);
      message.success('告警通道已创建');
    }
    channelModalOpen.value = false;
    await loadData();
  } catch {
    // 请求拦截器统一提示错误。
  } finally {
    savingChannel.value = false;
  }
}

async function toggleChannel(channel: AlertChannel, enabled: boolean) {
  try {
    await updateAlertChannelApi(channel.id, {
      enabled,
      name: channel.name,
      type: channel.type,
    });
    message.success(enabled ? '告警通道已启用' : '告警通道已停用');
    await loadData();
  } catch {
    // 请求拦截器统一提示错误。
  }
}

async function testChannel(channel: AlertChannel) {
  testingChannelId.value = channel.id;
  try {
    const result = await testAlertChannelApi(channel.id);
    message.success(`测试消息已送达（HTTP ${result.responseStatus ?? '-'}）`);
    await loadData();
  } catch {
    await loadData();
  } finally {
    testingChannelId.value = undefined;
  }
}

function confirmDeleteChannel(channel: AlertChannel) {
  Modal.confirm({
    content: '删除后会解除证书规则中的关联；历史发送记录仍会保留。',
    okButtonProps: { danger: true },
    okText: '删除',
    onOk: async () => {
      await deleteAlertChannelApi(channel.id);
      message.success('告警通道已删除');
      await loadData();
    },
    title: `确认删除「${channel.name}」？`,
  });
}

async function saveRule(rule: TLSAlertRule) {
  const draft = getRuleDraft(rule);
  try {
    await saveTLSAlertRuleApi(rule.certificateId, {
      channelIds: draft.channelIds,
      daysBefore: Number(draft.daysBefore),
      enabled: draft.enabled,
    });
    message.success(`「${rule.name}」的到期提醒已保存`);
    await loadData();
  } catch {
    // 请求拦截器统一提示错误。
  }
}

async function runCheck() {
  checkingAlerts.value = true;
  try {
    const result = await checkTLSAlertsApi();
    if (result.notificationsFailed > 0) {
      message.warning(
        `检查完成：已发送 ${result.notificationsSent} 条，失败 ${result.notificationsFailed} 条。请查看发送记录。`,
      );
    } else {
      message.success(`检查完成：已发送 ${result.notificationsSent} 条到期提醒`);
    }
    await loadData();
  } catch {
    // 请求拦截器统一提示错误。
  } finally {
    checkingAlerts.value = false;
  }
}

function formatExpiry(value: string) {
  return dayjs(value).format('YYYY-MM-DD');
}

function daysLabel(days: number) {
  if (days < 0) return `已过期 ${Math.abs(days)} 天`;
  if (days === 0) return '今天到期';
  return `剩余 ${days} 天`;
}

function statusColor(status: AlertDelivery['status']) {
  if (status === 'sent') return 'green';
  if (status === 'failed') return 'red';
  if (status === 'sending') return 'blue';
  return 'gold';
}

onMounted(loadData);
</script>

<template>
  <Page
    auto-content-height
    description="管理 Webhook / 飞书推送通道，配置 TLS 证书到期提醒，并查看发送结果。"
    title="告警通知"
  >
    <div class="flex h-full min-h-0 flex-col gap-4">
      <section class="rounded-lg border border-border bg-card">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
          <div>
            <h2 class="m-0 text-base font-semibold">推送通道</h2>
            <p class="mb-0 mt-1 text-xs text-muted-foreground">
              Webhook 使用 JSON POST，可配置 HMAC 签名；飞书机器人支持文本和交互式消息卡片。
            </p>
          </div>
          <Button v-access:code="'orp:system:manage'" type="primary" @click="openCreateChannel">新增通道</Button>
        </div>
        <Table
          :columns="channelColumns"
          :data-source="channels"
          :loading="loading"
          :pagination="false"
          :row-key="(record: AlertChannel) => record.id"
          size="small"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'type'">
              <Tag :color="record.type === 'feishu' ? 'blue' : 'purple'">
                {{ record.type === 'feishu' ? '飞书机器人' : 'Webhook' }}
              </Tag>
              <Tag v-if="record.type === 'feishu' && record.messageFormat === 'card'" color="purple">
                卡片
              </Tag>
            </template>
            <template v-else-if="column.key === 'enabled'">
              <Switch
                v-access:code="'orp:system:manage'"
                :checked="record.enabled"
                checked-children="启用"
                un-checked-children="停用"
                @change="(value: unknown) => toggleChannel(asAlertChannel(record), value === true)"
              />
            </template>
            <template v-else-if="column.key === 'actions'">
              <Space size="small">
                <Button
                  :loading="testingChannelId === record.id"
                  size="small"
                  v-access:code="'orp:system:manage'"
                  @click="testChannel(asAlertChannel(record))"
                >
                  测试
                </Button>
                <Button v-access:code="'orp:system:manage'" size="small" @click="openEditChannel(asAlertChannel(record))">编辑</Button>
                <Button v-access:code="'orp:system:manage'" danger size="small" @click="confirmDeleteChannel(asAlertChannel(record))">
                  删除
                </Button>
              </Space>
            </template>
          </template>
        </Table>
        <div
          v-if="channels.length === 0 && !loading"
          class="px-4 py-6 text-center text-sm text-muted-foreground"
        >
          暂无推送通道。新增 Webhook 或飞书机器人后，可关联到 TLS 证书。
        </div>
      </section>

      <section class="min-h-0 rounded-lg border border-border bg-card">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
          <div>
            <h2 class="m-0 text-base font-semibold">TLS 到期提醒</h2>
            <p class="mb-0 mt-1 text-xs text-muted-foreground">
              到期日进入设置的提醒天数后，每天最多推送一次；失败消息会在 5 分钟后重试。
            </p>
          </div>
          <Button v-access:code="'orp:system:manage'" :loading="checkingAlerts" @click="runCheck">立即检查</Button>
        </div>
        <div class="px-4 pt-3 text-xs text-muted-foreground">
          每张 TLS 证书可独立启用提醒、设置提前天数，并关联一个或多个推送通道。
        </div>
        <Table
          :columns="ruleColumns"
          :data-source="rules"
          :loading="loading"
          :pagination="false"
          :row-key="(record: TLSAlertRule) => record.certificateId"
          :scroll="{ x: 1110 }"
          size="small"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'certificate'">
              <div class="font-medium">{{ record.name }}</div>
              <div class="max-w-60 truncate text-xs text-muted-foreground" :title="record.domains">
                {{ record.domains || '未配置域名' }}
              </div>
            </template>
            <template v-else-if="column.key === 'expires'">
              <div>{{ formatExpiry(record.notAfter) }}</div>
              <Tag
                :color="record.daysRemaining <= 7 ? 'red' : record.daysRemaining <= 30 ? 'gold' : 'green'"
                class="!m-0"
              >
                {{ daysLabel(record.daysRemaining) }}
              </Tag>
            </template>
            <template v-else-if="column.key === 'enabled'">
              <Switch v-model:checked="getRuleDraft(asTLSAlertRule(record)).enabled" />
            </template>
            <template v-else-if="column.key === 'daysBefore'">
              <InputNumber
                v-model:value="getRuleDraft(asTLSAlertRule(record)).daysBefore"
                :max="365"
                :min="1"
                class="!w-24"
              />
              <span class="ml-1 text-xs text-muted-foreground">天</span>
            </template>
            <template v-else-if="column.key === 'channels'">
              <Select
                v-model:value="getRuleDraft(asTLSAlertRule(record)).channelIds"
                :options="channelOptions"
                :max-tag-count="2"
                class="!min-w-64 !w-full"
                mode="multiple"
                placeholder="选择推送通道"
              />
            </template>
            <template v-else-if="column.key === 'actions'">
              <Button v-access:code="'orp:system:manage'" size="small" type="primary" @click="saveRule(asTLSAlertRule(record))">
                保存
              </Button>
            </template>
          </template>
          <template #emptyText>
            暂无 TLS 证书，请先录入证书后配置到期提醒。
          </template>
        </Table>
        <div v-if="rules.length > 0" class="border-t border-border px-4 py-2 text-xs text-muted-foreground">
          {{ rules.length }} 张证书 · {{ enabledRuleCount }} 条提醒规则已启用
        </div>
      </section>

      <section class="min-h-0 flex-1 rounded-lg border border-border bg-card">
        <div class="border-b border-border px-4 py-3">
          <h2 class="m-0 text-base font-semibold">最近发送记录</h2>
        </div>
        <Table
          :columns="deliveryColumns"
          :data-source="deliveries"
          :loading="loading"
          :pagination="false"
          :row-key="(record: AlertDelivery) => record.id"
          :scroll="{ x: 1050, y: 220 }"
          size="small"
        >
          <template #bodyCell="{ column, record }">
            <template v-if="column.key === 'createdAt'">
              {{ dayjs(record.createdAt).format('YYYY-MM-DD HH:mm:ss') }}
            </template>
            <template v-else-if="column.key === 'eventType'">
              {{ record.eventType === 'tls_expiry' ? 'TLS 到期' : '通道测试' }}
            </template>
            <template v-else-if="column.key === 'status'">
              <Tag :color="statusColor(record.status)">
                {{ record.status === 'sent' ? '已送达' : record.status === 'failed' ? '失败' : record.status === 'sending' ? '发送中' : '待重试' }}
              </Tag>
              <div v-if="record.errorText" class="max-w-52 truncate text-xs text-red-500" :title="record.errorText">
                {{ record.errorText }}
              </div>
            </template>
          </template>
          <template #emptyText>暂无发送记录。可以先使用通道「测试」按钮验证配置。</template>
        </Table>
      </section>
    </div>

    <Modal
      v-model:open="channelModalOpen"
      :confirm-loading="savingChannel"
      :title="editingChannelId ? '编辑告警通道' : '新增告警通道'"
      ok-text="保存"
      cancel-text="取消"
      @ok="saveChannel"
    >
      <Form ref="channelFormRef" :model="channelForm" layout="vertical">
        <FormItem
          label="通道名称"
          name="name"
          :rules="[{ required: true, message: '请输入通道名称' }]"
        >
          <Input v-model:value="channelForm.name" :maxlength="128" placeholder="例如：运维告警群" />
        </FormItem>
        <FormItem label="推送方式" name="type" :rules="[{ required: true, message: '请选择推送方式' }]">
          <Select
            :value="channelForm.type"
            @change="changeChannelType"
            :options="[
              { label: '通用 Webhook', value: 'webhook' },
              { label: '飞书自定义机器人', value: 'feishu' },
            ]"
          />
        </FormItem>
        <FormItem v-if="channelForm.type === 'feishu'" label="飞书消息格式" name="messageFormat">
          <Select
            v-model:value="channelForm.messageFormat"
            :options="[
              { label: '交互式消息卡片（推荐）', value: 'card' },
              { label: '普通文本', value: 'text' },
            ]"
          />
          <div class="mt-1 text-xs text-muted-foreground">
            卡片会突出显示告警标题、证书域名、到期时间和剩余天数。
          </div>
        </FormItem>
        <FormItem label="Webhook 地址">
          <InputPassword
            v-model:value="channelForm.webhookUrl"
            :placeholder="editingChannelId ? `当前地址：${channels.find((item) => item.id === editingChannelId)?.targetHint || '已配置'}；留空保持不变` : channelForm.type === 'feishu' ? '粘贴飞书自定义机器人的 Webhook 地址' : 'https://example.com/your-webhook'"
          />
          <div class="mt-1 text-xs text-muted-foreground">
            地址加密保存，列表只展示脱敏域名。修改时留空会保留当前地址。
          </div>
        </FormItem>
        <FormItem :label="channelForm.type === 'feishu' ? '签名密钥（可选）' : 'Webhook HMAC 密钥（可选）'">
          <InputPassword
            v-model:value="channelForm.secret"
            :placeholder="editingChannelId ? '留空保持现有密钥' : '未配置时不签名'"
          />
          <div class="mt-1 text-xs text-muted-foreground">
            密钥加密保存。飞书按机器人签名规则生成 sign；通用 Webhook 发送 X-ORP-Signature 请求头。
          </div>
        </FormItem>
        <FormItem label="保存后启用">
          <Switch v-model:checked="channelForm.enabled" checked-children="启用" un-checked-children="停用" />
        </FormItem>
      </Form>
    </Modal>
  </Page>
</template>
