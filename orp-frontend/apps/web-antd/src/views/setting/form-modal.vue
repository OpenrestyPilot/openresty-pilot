<script lang="ts" setup>
import type { OrpSetting, SettingPayload, SettingType } from '#/api';

import { computed, ref } from 'vue';

import { useVbenModal } from '@vben/common-ui';

import {
  EyeInvisibleOutlined,
  EyeOutlined,
} from '@ant-design/icons-vue';
import { Alert, Button, Space, message } from 'ant-design-vue';
import { useAccessStore } from '@vben/stores';

import { useVbenForm, z } from '#/adapter/form';
import {
  createSettingApi,
  getSettingPlainApi,
  listSettingGroupEntitiesApi,
  updateSettingApi,
} from '#/api';
import { useDirtyGuard } from '#/composables/use-dirty-guard';

defineOptions({ name: 'SettingFormModal' });

const emit = defineEmits<{ success: [] }>();

const canReadSecrets = computed(() => {
  const codes = useAccessStore().accessCodes;
  return codes.includes('*') || codes.includes('orp:secrets:read');
});

const isEdit = ref(false);
const editId = ref(0);
const editSensitive = ref(false);
/** 敏感值明文查看状态 */
const showPlain = ref(false);
const plainLoading = ref(false);
const saving = ref(false);

const { markDirty, onBeforeClose, resetDirty } = useDirtyGuard();

/** 键名：小写字母开头的多级路径（如 system.platform_name） */
const KEY_PATTERN = /^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$/;

const TYPE_OPTIONS: { label: string; value: SettingType }[] = [
  { label: '字符串（string）', value: 'string' },
  { label: '布尔值（boolean）', value: 'boolean' },
  { label: '数字（number）', value: 'number' },
  { label: 'JSON（json）', value: 'json' },
];

/** 所属分组选项（打开弹窗时动态聚合系统预置 + 自定义分组） */
const groupOptions = ref<{ label: string; value: string }[]>([]);

/** 当前参数类型（驱动键值格式校验与占位提示） */
const currentType = ref<SettingType>('string');

/** 打开时的初始快照（重置=恢复打开时内容） */
let initialSnapshot: Record<string, any> = {};

/** 按参数类型校验键值格式（zod refine 谓词） */
function valueFormatOk(value: unknown): boolean {
  const str = String(value ?? '').trim();
  if (!str) return false;
  const t = currentType.value;
  if (t === 'boolean') return str === 'true' || str === 'false';
  if (t === 'number') {
    return /^-?\d+(\.\d+)?$/.test(str) && Number.isFinite(Number(str));
  }
  if (t === 'json') {
    try {
      JSON.parse(str);
      return true;
    } catch {
      return false;
    }
  }
  return true;
}

const [Form, formApi] = useVbenForm({
  handleValuesChange: (values) => {
    if (values?.type !== undefined) {
      currentType.value = values.type as SettingType;
    }
    markDirty();
  },
  handleSubmit: onSubmit,
  schema: [
    {
      component: 'Input',
      componentProps: { placeholder: '例如：system.platform_name' },
      fieldName: 'key',
      help: '全局唯一；小写字母开头的多级路径，段间以点分隔',
      label: '配置键名',
      rules: z
        .string()
        .min(1, { message: '配置键名为必填项' })
        .regex(KEY_PATTERN, {
          message:
            '键名需为小写字母开头的多级路径（如 system.platform_name）',
        }),
    },
    {
      component: 'Input',
      componentProps: { placeholder: '例如：平台名称' },
      fieldName: 'name',
      label: '配置名称',
      rules: z.string().min(1, { message: '配置名称为必填项' }),
    },
    {
      component: 'Select',
      componentProps: {
        options: groupOptions,
        placeholder: '选择所属分组',
        showSearch: true,
      },
      defaultValue: '系统基础',
      fieldName: 'group',
      help: '可归属系统预置分组或任意自定义分组（自定义分组在「分组管理」中维护）',
      label: '所属分组',
      rules: z.string().min(1, { message: '所属分组为必填项' }),
    },
    {
      component: 'Select',
      componentProps: {
        options: TYPE_OPTIONS,
        placeholder: '选择参数类型',
      },
      defaultValue: 'string',
      fieldName: 'type',
      help: '切换类型后请同步调整键值格式，保存时按类型校验',
      label: '参数类型',
      rules: z.string().min(1, { message: '参数类型为必填项' }),
    },
    {
      component: 'Input',
      componentProps: {
        placeholder: '按所选类型输入，例如：ORP / true / 30',
      },
      fieldName: 'value',
      help: '按所选参数类型进行格式校验（JSON 非法将阻断保存）',
      label: '配置键值',
      rules: z
        .string()
        .min(1, { message: '配置键值为必填项' })
        .refine((value) => valueFormatOk(value), {
          message: '键值格式与所选参数类型不匹配（布尔/数字/JSON 需合法）',
        }),
    },
    {
      component: 'RadioGroup',
      componentProps: {
        options: [
          { label: '启用', value: 'enabled' },
          { label: '禁用', value: 'disabled' },
        ],
      },
      defaultValue: 'enabled',
      fieldName: 'status',
      label: '状态',
    },
    {
      component: 'RadioGroup',
      componentProps: {
        options: [
          { label: '普通', value: 'normal' },
          { label: '敏感', value: 'sensitive' },
        ],
      },
      defaultValue: 'normal',
      fieldName: 'sensitivity',
      help: '敏感信息在列表中脱敏展示，编辑时可查看明文',
      label: '敏感标记',
    },
    {
      component: 'Textarea',
      componentProps: {
        placeholder: '配置用途说明，例如：控制面平台展示名称',
        rows: 2,
      },
      fieldName: 'description',
      label: '配置描述',
    },
  ],
  showDefaultActions: false,
});

const [Modal, modalApi] = useVbenModal<{ record?: OrpSetting }>({
  fullscreenButton: false,
  onBeforeClose,
  async onOpenChange(isOpen: boolean) {
    if (isOpen) {
      await formApi.resetForm();
      // 动态聚合系统预置 + 自定义分组作为归属选项（按展示排序）
      try {
        const groups = await listSettingGroupEntitiesApi();
        groupOptions.value = groups.map((g) => ({
          label: g.isSystem ? g.name : `${g.name}（自定义）`,
          value: g.name,
        }));
      } catch {
        // 拉取失败时保留既有选项，由请求拦截器统一提示
      }
      const record = modalApi.getData()?.record;
      isEdit.value = Boolean(record);
      editId.value = record?.id ?? 0;
      editSensitive.value = record?.isSensitive ?? false;
      showPlain.value = false;
      plainLoading.value = false;
      currentType.value = record?.type ?? 'string';
      modalApi.setState({ title: record ? '编辑系统配置' : '新增系统配置' });
      if (record) {
        formApi.setValues({
          description: record.description,
          group: record.group,
          key: record.key,
          name: record.name,
          sensitivity: record.isSensitive ? 'sensitive' : 'normal',
          status: record.status,
          type: record.type,
          value: record.value,
        });
        // 敏感项：打开时拉取明文回填，避免脱敏值被误存
        if (record.isSensitive && canReadSecrets.value) {
          plainLoading.value = true;
          try {
            const plain = await getSettingPlainApi(record.id);
            formApi.setValues({ value: plain.value });
          } catch {
            message.warning('敏感值明文获取失败，保存时将沿用原值');
          } finally {
            plainLoading.value = false;
          }
        }
      }
      // 记录初始快照 + 清脏
      const values = await formApi.getValues();
      initialSnapshot = { ...values };
      resetDirty();
    }
  },
});

/** 重置：恢复打开时的内容 */
async function onReset() {
  await formApi.setValues(initialSnapshot);
  currentType.value = (initialSnapshot.type as SettingType) ?? 'string';
  showPlain.value = false;
  resetDirty();
}

/** 敏感值明文/脱敏切换 */
async function togglePlain() {
  if (!canReadSecrets.value || !isEdit.value || !editSensitive.value) return;
  if (showPlain.value) {
    // 切回脱敏展示：恢复初始值
    await formApi.setValues({ value: initialSnapshot.value ?? '' });
    showPlain.value = false;
    return;
  }
  plainLoading.value = true;
  try {
    const plain = await getSettingPlainApi(editId.value);
    formApi.setValues({ value: plain.value });
    showPlain.value = true;
  } catch {
    // 错误提示由请求拦截器统一处理
  } finally {
    plainLoading.value = false;
  }
}

/** 保存：触发表单校验，通过后由 handleSubmit 走 onSubmit */
async function onSave() {
  await formApi.validateAndSubmitForm();
}

async function onSubmit(values: Record<string, any>) {
  saving.value = true;
  try {
    const payload: SettingPayload = {
      description: String(values.description ?? '').trim(),
      group: String(values.group ?? '').trim(),
      isSensitive: values.sensitivity === 'sensitive',
      key: String(values.key).trim(),
      name: String(values.name).trim(),
      status: values.status === 'disabled' ? 'disabled' : 'enabled',
      type: values.type as SettingType,
      value: String(values.value ?? '').trim(),
    };
    if (isEdit.value) {
      await updateSettingApi(editId.value, payload);
      message.success('系统配置已更新，列表已同步刷新');
    } else {
      await createSettingApi(payload);
      message.success('系统配置创建成功，列表已同步刷新');
    }
    resetDirty();
    modalApi.close();
    emit('success');
  } catch {
    // 错误提示由请求拦截器统一处理
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <Modal class="w-[680px]">
    <Alert
      v-if="isEdit && editSensitive"
      class="mb-4"
      message="该配置项为敏感信息：值默认脱敏回填，查看明文后方可编辑新值"
      show-icon
      type="warning"
    />
    <Form />
    <template #footer>
      <Space>
        <Button
          v-access:code="'orp:secrets:read'"
          v-if="isEdit && editSensitive"
          :loading="plainLoading"
          size="small"
          @click="togglePlain"
        >
          <EyeInvisibleOutlined v-if="showPlain" />
          <EyeOutlined v-else />
          {{ showPlain ? '恢复脱敏' : '查看明文' }}
        </Button>
        <Button :disabled="saving" @click="onReset">重置</Button>
        <Button :disabled="saving" @click="modalApi.close()">取消</Button>
        <Button :loading="saving" type="primary" @click="onSave">保存</Button>
      </Space>
    </template>
  </Modal>
</template>
