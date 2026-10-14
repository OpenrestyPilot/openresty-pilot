<script lang="ts" setup>
import type { NodeItem, OrpDirective } from '#/api';

import { ref, watch } from 'vue';

import { useVbenModal } from '@vben/common-ui';

import { AutoComplete, Button, Input, message, Tag, Tooltip } from 'ant-design-vue';

import { useVbenForm, z } from '#/adapter/form';
import { createNodeApi, listCentersApi, updateNodeApi } from '#/api';
import { useDirtyGuard } from '#/composables/use-dirty-guard';

defineOptions({ name: 'NodeFormModal' });

const emit = defineEmits<{ success: [record?: NodeItem] }>();

const isEdit = ref(false);
const editId = ref(0);

const { markDirty, onBeforeClose, resetDirty } = useDirtyGuard();

const [Form, formApi] = useVbenForm({
  handleValuesChange: () => markDirty(),
  handleSubmit: onSubmit,
  schema: [
    {
      component: 'ApiSelect',
      componentProps: {
        allowClear: false,
        api: async () => {
          const res = await listCentersApi({ page: 1, pageSize: 200 });
          return res.items.map((item) => ({
            label: item.name,
            value: item.id,
          }));
        },
        placeholder: '请选择所属中心',
      },
      fieldName: 'centerId',
      label: '所属中心',
      rules: z.number({ message: '请选择所属中心' }).min(1, {
        message: '请选择所属中心',
      }),
    },
    {
      component: 'Input',
      componentProps: { maxLength: 64, placeholder: '例如：or-sh-ngx-05' },
      fieldName: 'name',
      label: '节点名称',
      rules: z.string().min(1, { message: '请输入节点名称' }),
    },
    {
      component: 'Input',
      componentProps: { placeholder: '例如：10.60.1.15' },
      fieldName: 'host',
      help: '节点数据面主机的 IPv4 或主机名',
      label: '主机地址',
      rules: z
        .string()
        .min(1, { message: '请输入主机地址' })
        .regex(/^[a-zA-Z0-9][a-zA-Z0-9.-]*$/, {
          message: '支持 IPv4 或主机名格式',
        }),
    },
    {
      component: 'Input',
      componentProps: { placeholder: '例如：http://10.60.1.15:8081/control' },
      fieldName: 'controlEndpoint',
      help: '节点本机 Control API 的 HTTP 转发地址（可选）',
      label: '通信端点',
    },
    {
      component: 'Input',
      componentProps: { placeholder: '例如：Ubuntu 22.04 / OpenResty 1.25.3' },
      fieldName: 'osInfo',
      label: '系统信息',
    },
    {
      component: 'Input',
      componentProps: { disabled: true, placeholder: '未发布' },
      dependencies: {
        if: (values) => Boolean(values.activeVersion),
        triggerFields: ['activeVersion'],
      },
      fieldName: 'activeVersion',
      label: '活动版本',
    },
    {
      component: 'InputNumber',
      componentProps: { max: 100, min: 1, placeholder: '默认 10' },
      fieldName: 'weight',
      help: '权重分批下发排序依据：低权重实例优先金丝雀灰度（1-100）',
      label: '下发权重',
      rules: z.number().int().min(1, { message: '权重最小为 1' }).max(100, { message: '权重最大为 100' }),
    },
    {
      component: 'Select',
      componentProps: {
        showSearch: true,

        optionFilterProp: 'label',
        options: [
          { label: '运行中（running）', value: 'running' },
          { label: '已暂停（paused）', value: 'paused' },
          { label: '已停止（stopped）', value: 'stopped' },
          { label: '维护中（maintenance）', value: 'maintenance' },
        ],
        placeholder: '请选择运行状态',
      },
      fieldName: 'status',
      label: '运行状态',
      rules: z.enum(['maintenance', 'paused', 'running', 'stopped'], {
        message: '请选择运行状态',
      }),
    },
  ],
  showDefaultActions: false,
});

/* ---------------- 根指令（main 上下文）动态键值编辑 ---------------- */

interface EditableDirective {
  name: string;
  value: string;
}

const editDirectives = ref<EditableDirective[]>([]);

/** 常用根指令预设（名称 · 说明） */
const MAIN_DIRECTIVE_OPTIONS = [
  {
    label: 'worker_processes · 工作进程数（auto 或正整数）',
    value: 'worker_processes',
  },
  {
    label: 'worker_rlimit_nofile · 进程文件句柄上限（如 65535）',
    value: 'worker_rlimit_nofile',
  },
  {
    label: 'error_log · 全局错误日志（如 logs/error.log warn）',
    value: 'error_log',
  },
  { label: 'pid · 主进程 PID 文件（如 logs/nginx.pid）', value: 'pid' },
  { label: 'user · 运行用户（如 nginx）', value: 'user' },
  { label: 'daemon · 守护进程模式（on / off）', value: 'daemon' },
  {
    label: 'worker_priority · 工作进程优先级（-20 ~ 19）',
    value: 'worker_priority',
  },
  { label: 'worker_cpu_affinity · CPU 亲和性（auto）', value: 'worker_cpu_affinity' },
  {
    label: 'timer_resolution · 计时器分辨率（如 100ms）',
    value: 'timer_resolution',
  },
];

/** 指令名格式（nginx 命名规范） */
const NAME_PATTERN = /^[a-z_][a-z0-9_]*$/;

/** 特定根指令取值校验规则 */
const MAIN_VALUE_RULES: Record<string, { pattern: RegExp; tip: string }> = {
  worker_processes: {
    pattern: /^(auto|[1-9]\d{0,3})$/,
    tip: '取值应为 auto 或 1-9999 的正整数',
  },
  worker_rlimit_nofile: {
    pattern: /^\d{4,7}$/,
    tip: '取值应为 1024 以上正整数（如 65535）',
  },
  daemon: { pattern: /^(on|off)$/, tip: '取值应为 on 或 off' },
  user: {
    pattern: /^[a-z_][a-z0-9_-]*$/,
    tip: '取值应为 Linux 用户名（如 nginx）',
  },
  worker_priority: {
    pattern: /^-?(1?\d|20)$/,
    tip: '取值应为 -20 ~ 20 的整数',
  },
};

function addDirective() {
  editDirectives.value.push({ name: '', value: '' });
}

function removeDirective(index: number) {
  editDirectives.value.splice(index, 1);
}

function filterOption(input: string, option: { label: string }) {
  return option.label.toLowerCase().includes(input.toLowerCase());
}

/** 校验根指令列表，返回错误信息（null 表示通过） */
function validateDirectives(): null | string {
  const seen = new Set<string>();
  for (const [index, item] of editDirectives.value.entries()) {
    const name = item.name.trim();
    const value = item.value.trim();
    if (!name || !value) {
      return `第 ${index + 1} 条根指令的指令名与取值均为必填项`;
    }
    if (!NAME_PATTERN.test(name)) {
      return `第 ${index + 1} 条指令名「${name}」不合法：仅支持小写字母、数字与下划线`;
    }
    if (seen.has(name)) {
      return `指令 ${name} 重复定义，请合并或删除重复项`;
    }
    seen.add(name);
    const rule = MAIN_VALUE_RULES[name];
    if (rule && !rule.pattern.test(value)) {
      return `指令 ${name} ${rule.tip}`;
    }
  }
  return null;
}

watch(editDirectives, () => markDirty(), { deep: true });

const [Modal, modalApi] = useVbenModal<{ record?: NodeItem }>({
  fullscreenButton: false,
  onBeforeClose,
  async onConfirm() {
    await formApi.validateAndSubmitForm();
  },
  async onOpenChange(isOpen: boolean) {
    if (isOpen) {
      await formApi.resetForm();
      const record = modalApi.getData()?.record;
      isEdit.value = Boolean(record);
      editId.value = record?.id ?? 0;
      modalApi.setState({ title: record ? '编辑节点' : '注册节点' });
      if (record) {
        formApi.setValues({
          activeVersion: record.activeVersion,
          centerId: record.centerId,
          controlEndpoint: record.controlEndpoint,
          host: record.host,
          name: record.name,
          osInfo: record.osInfo,
          status: record.status,
          weight: record.weight ?? 10,
        });
        editDirectives.value = (record.directives ?? []).map((item) => ({
          ...item,
        }));
      } else {
        // resetForm 会把未设置的 number 字段初始化为 0；中心 ID 从 1 开始，
        // 该值既不是有效中心也会绕过占位提示，显式清空让用户选择。
        formApi.setValues({ centerId: undefined });
        editDirectives.value = [];
      }
      resetDirty();
    }
  },
});

async function onSubmit(values: Record<string, any>) {
  const directiveError = validateDirectives();
  if (directiveError) {
    message.error(directiveError);
    return;
  }
  modalApi.lock();
  try {
    const payload = {
      activeVersion: values.activeVersion,
      centerId: Number(values.centerId),
      controlEndpoint: values.controlEndpoint ?? '',
      directives: editDirectives.value.map(
        (item) => ({ name: item.name.trim(), value: item.value.trim() }),
      ) as OrpDirective[],
      host: String(values.host),
      name: String(values.name),
      osInfo: values.osInfo ?? '',
      status: values.status,
      weight: Number(values.weight ?? 10),
    };
    let savedNode: NodeItem | undefined;
    if (isEdit.value) {
      await updateNodeApi(editId.value, payload);
      message.success('节点信息已更新');
    } else {
      savedNode = await createNodeApi(payload);
      message.success('节点注册成功');
    }
    resetDirty();
    modalApi.close();
    emit('success', savedNode);
  } catch {
    // 错误提示由请求拦截器统一处理
  } finally {
    modalApi.unlock();
  }
}
</script>

<template>
  <Modal class="w-[680px]">
    <div class="pr-2">
      <Form />
    </div>

    <!-- 根指令（main 上下文） -->
    <div class="mt-4 px-1">
      <div class="mb-2 flex items-center justify-between">
        <Tooltip
          title="nginx.conf 最外层 main 上下文指令（如 worker_processes、error_log）。配置将随版本发布渲染进节点主配置，留空则不生成。"
        >
          <span class="text-sm font-medium">根指令（main 上下文）</span>
        </Tooltip>
        <Button size="small" type="dashed" @click="addDirective">+ 添加指令</Button>
      </div>
      <div class="max-h-60 space-y-2 overflow-y-auto pr-1">
        <div
          v-for="(directive, index) in editDirectives"
          :key="index"
          class="flex flex-wrap items-center gap-2 rounded border border-gray-200 bg-gray-50 p-2 dark:border-gray-700 dark:bg-gray-800"
        >
          <Tag class="shrink-0" color="blue">#{{ index + 1 }}</Tag>
          <AutoComplete
            v-model:value="directive.name"
            :filter-option="filterOption"
            :options="MAIN_DIRECTIVE_OPTIONS"
            class="!w-60"
            placeholder="指令名，如 worker_processes"
            size="small"
          />
          <Input
            v-model:value="directive.value"
            class="!w-56"
            placeholder="取值，如 auto / 65535"
            size="small"
          />
          <Button danger size="small" type="link" @click="removeDirective(index)">
            删除
          </Button>
        </div>
        <div
          v-if="editDirectives.length === 0"
          class="py-4 text-center text-xs text-gray-400"
        >
          暂无根指令，点击「+ 添加指令」开始配置
        </div>
      </div>
    </div>
  </Modal>
</template>
