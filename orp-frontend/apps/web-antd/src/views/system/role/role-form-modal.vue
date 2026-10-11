<script lang="ts" setup>
import type { RbacRoleItem } from '#/api';

import { ref } from 'vue';

import { useVbenModal } from '@vben/common-ui';

import { message } from 'ant-design-vue';

import { useVbenForm, z } from '#/adapter/form';
import { createRbacRoleApi, listCentersApi, updateRbacRoleApi } from '#/api';
import { useDirtyGuard } from '#/composables/use-dirty-guard';

defineOptions({ name: 'SystemRoleFormModal' });

const emit = defineEmits<{ success: [] }>();

const isEdit = ref(false);
const editId = ref(0);

const { markDirty, onBeforeClose, resetDirty } = useDirtyGuard();

const [Form, formApi] = useVbenForm({
  handleValuesChange: () => markDirty(),
  handleSubmit: onSubmit,
  schema: [
    {
      component: 'Input',
      componentProps: { placeholder: '2-32 位，小写字母开头' },
      dependencies: {
        disabled: (values) => Boolean(values._edit),
        triggerFields: ['_edit'],
      },
      fieldName: 'code',
      label: '角色编码',
      help: '2-32 位，小写字母开头，仅含小写字母、数字与下划线',
    },
    {
      component: 'Input',
      componentProps: { maxLength: 64, placeholder: '例如：网络运维组' },
      fieldName: 'name',
      label: '角色名称',
      rules: z.string().min(1, { message: '请输入角色名称' }),
    },
    {
      component: 'Textarea',
      componentProps: { rows: 3, maxLength: 200, placeholder: '描述该角色的职责范围' },
      fieldName: 'description',
      label: '角色描述',
    },
    {
      component: 'ApiSelect',
      componentProps: {
        api: async () => {
          const res = await listCentersApi({ page: 1, pageSize: 200 });
          return [
            { label: '全部中心', value: '*' },
            ...res.items.map((center) => ({ label: center.name, value: String(center.id) })),
          ];
        },
        mode: 'multiple',
        placeholder: '选择允许访问的中心；不选择时仅能访问全局配置',
      },
      fieldName: 'centerIds',
      label: '中心范围',
      help: '此范围同时限制中心列表、节点及其代理配置。全部中心不能与单独中心同时选择。',
    },
    {
      component: 'RadioGroup',
      componentProps: {
        options: [
          { label: '启用', value: 'enabled' },
          { label: '停用', value: 'disabled' },
        ],
      },
      dependencies: {
        if: (values) => !values._edit,
        triggerFields: ['_edit'],
      },
      fieldName: 'status',
      label: '状态',
    },
  ],
  showDefaultActions: false,
});

const [Modal, modalApi] = useVbenModal<{ record?: RbacRoleItem }>({
  fullscreenButton: false,
  onBeforeClose,
  async onConfirm() {
    await formApi.submitForm();
  },
  async onOpenChange(isOpen: boolean) {
    if (isOpen) {
      await formApi.resetForm();
      const record = modalApi.getData()?.record;
      isEdit.value = Boolean(record);
      editId.value = record?.id ?? 0;
      modalApi.setState({ title: record ? '编辑角色' : '新增角色' });
      if (record) {
        formApi.setValues({
          _edit: true,
          code: record.code,
          description: record.description ?? '',
          name: record.name,
          centerIds: record.centerIds ?? [],
        });
      } else {
        formApi.setValues({ _edit: false, centerIds: [] });
      }
      resetDirty();
    }
  },
});

async function onSubmit(values: Record<string, any>) {
  modalApi.lock();
  try {
    if (isEdit.value) {
      await updateRbacRoleApi(editId.value, {
        description: values.description ?? '',
        centerIds: values.centerIds ?? [],
        name: String(values.name),
      });
      message.success('角色信息已更新');
    } else {
      await createRbacRoleApi({
        code: String(values.code),
        centerIds: values.centerIds ?? [],
        description: values.description ?? '',
        name: String(values.name),
        status: values.status ?? 'enabled',
      });
      message.success('角色创建成功');
    }
    resetDirty();
    modalApi.close();
    emit('success');
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
  </Modal>
</template>
