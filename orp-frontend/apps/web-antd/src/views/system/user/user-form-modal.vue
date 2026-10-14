<script lang="ts" setup>
import type { RbacUserItem } from '#/api';

import { ref } from 'vue';

import { useVbenModal } from '@vben/common-ui';

import { message } from 'ant-design-vue';

import { useVbenForm, z } from '#/adapter/form';
import { createRbacUserApi, listRbacRolesApi, updateRbacUserApi } from '#/api';
import { useDirtyGuard } from '#/composables/use-dirty-guard';

defineOptions({ name: 'SystemUserFormModal' });

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
      fieldName: 'username',
      label: '用户名',
      help: '2-32 位，小写字母开头，可含数字、点、下划线或中划线',
    },
    {
      component: 'Input',
      componentProps: { maxLength: 64, placeholder: '例如：张三' },
      fieldName: 'realName',
      label: '姓名',
      rules: z.string().min(1, { message: '请输入姓名' }),
    },
    {
      component: 'ApiSelect',
      componentProps: {
        api: async () => {
          const res = await listRbacRolesApi({ page: 1, pageSize: 100, status: 'enabled' });
          return res.items.map((r) => ({ label: r.name, value: r.id }));
        },
        mode: 'multiple',
        placeholder: '请选择角色（可多选）',
      },
      fieldName: 'roleIds',
      label: '所属角色',
      rules: z.array(z.number()).min(1, { message: '至少绑定一个角色' }),
    },
    {
      component: 'Input',
      componentProps: { placeholder: '例如：name@orp.daoke.net' },
      fieldName: 'email',
      label: '邮箱',
      rules: z.string().email('邮箱格式不正确').optional().or(z.literal('')),
    },
    {
      component: 'Input',
      componentProps: { maxLength: 20, placeholder: '例如：13800000000' },
      fieldName: 'phone',
      label: '手机号',
      rules: z
        .string()
        .regex(/^\d{5,20}$/, '手机号格式不正确（5-20 位数字）')
        .optional()
        .or(z.literal('')),
    },
    {
      component: 'InputPassword',
      componentProps: { maxLength: 72, placeholder: '至少 12 位，仅新增用户时必填' },
      dependencies: { if: (values) => !values._edit, triggerFields: ['_edit'] },
      fieldName: 'password',
      label: '初始密码',
      rules: z.string().min(12, { message: '密码至少 12 位' }).optional().or(z.literal('')),
    },
    {
      component: 'RadioGroup',
      componentProps: {
        options: [
          { label: '启用', value: 'enabled' },
          { label: '禁用', value: 'disabled' },
        ],
      },
      fieldName: 'status',
      label: '账号状态',
    },
  ],
  showDefaultActions: false,
});

const [Modal, modalApi] = useVbenModal<{ record?: RbacUserItem }>({
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
      modalApi.setState({ title: record ? '编辑用户' : '新增用户' });
      if (record) {
        formApi.setValues({
          _edit: true,
          email: record.email ?? '',
          phone: record.phone ?? '',
          realName: record.realName,
          roleIds: record.roleIds,
          status: record.status,
          username: record.username,
        });
      } else {
        formApi.setValues({ _edit: false, status: 'enabled' });
      }
      resetDirty();
    }
  },
});

async function onSubmit(values: Record<string, any>) {
  modalApi.lock();
  try {
    if (isEdit.value) {
      await updateRbacUserApi(editId.value, {
        email: values.email ?? '',
        phone: values.phone ?? '',
        realName: String(values.realName),
        roleIds: values.roleIds as number[],
        status: values.status,
      });
      message.success('用户信息已更新');
    } else {
      await createRbacUserApi({
        email: values.email ?? '',
        password: values.password || undefined,
        phone: values.phone ?? '',
        realName: String(values.realName),
        roleIds: values.roleIds as number[],
        status: values.status,
        username: String(values.username),
      });
      message.success('用户创建成功');
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
