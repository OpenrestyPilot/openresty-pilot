<script lang="ts" setup>
import type { Certificate } from '#/api';

import { computed, reactive, ref } from 'vue';

import { useVbenModal } from '@vben/common-ui';

import { Alert, message } from 'ant-design-vue';
import { useAccessStore } from '@vben/stores';

import { useVbenForm, z } from '#/adapter/form';
import { createCertificateApi, updateCertificateApi, listCentersApi } from '#/api';
import { parseCertificate } from '#/utils/x509';
import { useDirtyGuard } from '#/composables/use-dirty-guard';

defineOptions({ name: 'TlsFormModal' });

const canReadSecrets = computed(() => {
  const codes = useAccessStore().accessCodes;
  return codes.includes('*') || codes.includes('orp:secrets:read');
});

const emit = defineEmits<{ success: [] }>();

const isEdit = ref(false);
const editId = ref(0);

/** 自动解析结果（粘贴 PEM 后填充，只读展示） */
const parsed = reactive<{
  domains: string;
  error: string;
  notAfter: string;
  notBefore: string;
  subjectCn: string;
}>({
  domains: '',
  error: '',
  notAfter: '',
  notBefore: '',
  subjectCn: '',
});

const { markDirty, onBeforeClose, resetDirty } = useDirtyGuard();

const [Form, formApi] = useVbenForm({
  commonConfig: { componentProps: { class: 'w-full' } },
  handleValuesChange: async (values, fields) => {
    markDirty();
    // 粘贴公钥证书后自动解析有效期与关联域名
    if (fields.includes('certificate')) {
      await handleParse(String(values.certificate ?? ''));
    }
  },
  handleSubmit: onSubmit,
  schema: [
    {
      component: 'Input',
      componentProps: { maxLength: 64, placeholder: '例如：api-example-cn' },
      fieldName: 'name',
      label: '证书名称',
      rules: z.string().min(1, { message: '请输入证书名称' }),
    },
    {
      component: 'Textarea',
      componentProps: {
        placeholder: '-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----',
        rows: 6,
      },
      fieldName: 'certificate',
      help: '粘贴 PEM 公钥证书后自动解析有效期与关联域名',
      label: '公钥证书',
      rules: z.string().refine((value) => value.includes('BEGIN CERTIFICATE'), {
        message: '证书格式错误：缺少 PEM 格式 BEGIN CERTIFICATE 头',
      }),
    },
    {
      component: 'Input',
      componentProps: { disabled: true, placeholder: '粘贴公钥证书后自动填充' },
      fieldName: 'domains',
      label: '关联域名',
      rules: z.string().min(1, { message: '请先粘贴公钥证书以解析关联域名' }),
    },
    {
      component: 'RangePicker',
      componentProps: {
        showTime: true,
        valueFormat: 'YYYY-MM-DD HH:mm:ss',
      },
      fieldName: 'validRange',
      label: '有效期范围',
      rules: z.array(z.string(), { message: '请选择有效期范围' }).length(2, {
        message: '请选择完整的有效期范围',
      }),
    },
    {
      component: 'RadioGroup',
      componentProps: {
        options: [
          { label: '全部中心', value: 'all' },
          { label: '指定中心', value: 'partial' },
        ],
      },
      defaultValue: 'all',
      fieldName: 'scopeType',
      label: '适用范围',
    },
    {
      component: 'ApiSelect',
      componentProps: {
        api: async () => {
          const res = await listCentersApi({ page: 1, pageSize: 200 });
          return res.items.map((item) => ({ label: item.name, value: item.id }));
        },
        mode: 'multiple',
        placeholder: '选择适用中心',
        showSearch: true,
        optionFilterProp: 'label',
      },
      dependencies: {
        if: (values) => values.scopeType === 'partial',
        triggerFields: ['scopeType'],
      },
      fieldName: 'centerIds',
      label: '适用中心',
    },
    {
      component: 'Textarea',
      componentProps: {
        placeholder: '-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----（可选）',
        rows: 4,
      },
      fieldName: 'certificateChain',
      help: '可选：PEM 格式中间证书链',
      label: '证书链',
    },
    {
      component: 'Textarea',
      componentProps: {
        placeholder: '-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----',
        rows: 4,
      },
      fieldName: 'privateKey',
      help: 'PEM 格式私钥，保存后不再回显明文',
      label: '私钥',
      rules: z.string().optional().refine((value) => !value || value.includes('PRIVATE KEY'), {
        message: '私钥格式错误：缺少 PEM 格式 PRIVATE KEY 头',
      }),
    },
  ],
  showDefaultActions: false,
});

const [Modal, modalApi] = useVbenModal<{ record?: Certificate }>({
  fullscreenButton: false,
  onBeforeClose,
  async onConfirm() {
    await formApi.validateAndSubmitForm();
  },
  async onOpenChange(isOpen: boolean) {
    if (isOpen) {
      await formApi.resetForm();
      resetParsed();
      const record = modalApi.getData()?.record;
      isEdit.value = Boolean(record);
      editId.value = record?.id ?? 0;
      modalApi.setState({ title: record ? '编辑证书' : '录入证书' });
      if (record) {
        const certRes = canReadSecrets.value ? await fetchCertificateDetail(record.id) : null;
        if (certRes) {
          formApi.setValues({
            certificate: certRes.certificate,
            certificateChain: certRes.certificateChain,
            centerIds: Array.isArray(record.centerScope) ? record.centerScope : [],
            domains: record.domains,
            name: record.name,
            privateKey: certRes.privateKey,
            scopeType: record.centerScope === 'all' ? 'all' : 'partial',
            validRange: [record.notBefore, record.notAfter],
          });
          // 编辑时同步解析状态展示（以服务器端存储数据为准）
          parsed.domains = record.domains;
          parsed.notAfter = record.notAfter;
          parsed.notBefore = record.notBefore;
        } else if (!canReadSecrets.value) {
          const publicDetail = await fetchCertificatePublicDetail(record.id);
          formApi.setValues({
            certificate: publicDetail?.certificate ?? '',
            certificateChain: publicDetail?.certificateChain ?? '',
            centerIds: Array.isArray(record.centerScope) ? record.centerScope : [],
            domains: record.domains,
            name: record.name,
            privateKey: '',
            scopeType: record.centerScope === 'all' ? 'all' : 'partial',
            validRange: [record.notBefore, record.notAfter],
          });
        }
      }
      resetDirty();
    }
  },
});

function resetParsed() {
  parsed.domains = '';
  parsed.error = '';
  parsed.notAfter = '';
  parsed.notBefore = '';
  parsed.subjectCn = '';
}

/** 仅具备敏感数据读取权限的角色可读取证书明文（含私钥）。 */
async function fetchCertificateDetail(id: number) {
  try {
    const { requestClient } = await import('#/api/request');
    return await requestClient.get<{
      certificate: string;
      certificateChain: string;
      privateKey: string;
    }>(`/orp/certificates/${id}/detail`);
  } catch {
    message.warning('证书明文加载失败，编辑保存将覆盖原内容');
    return null;
  }
}

async function fetchCertificatePublicDetail(id: number) {
  try {
    const { requestClient } = await import('#/api/request');
    return await requestClient.get<{ certificate: string; certificateChain: string }>(
      `/orp/certificates/${id}/public-detail`,
    );
  } catch {
    message.warning('证书内容加载失败，暂时不能编辑该证书');
    return null;
  }
}

/** 解析当前表单中的公钥证书 PEM，回填 domains/validRange */
async function handleParse(value: string) {
  if (!value || !value.includes('BEGIN CERTIFICATE') || !value.includes('END CERTIFICATE')) {
    if (value) parsed.error = 'PEM 内容不完整（缺少 END CERTIFICATE 尾）';
    return;
  }
  const result = parseCertificate(value);
  if (!result) {
    parsed.error = '证书解析失败：PEM 内容损坏或格式无效';
    parsed.domains = '';
    parsed.notBefore = '';
    parsed.notAfter = '';
    return;
  }
  parsed.error = '';
  parsed.subjectCn = result.subjectCn;
  const domains = [result.subjectCn, ...result.sanDns]
    .filter((d, i, arr) => d && arr.indexOf(d) === i)
    .join(', ');
  parsed.domains = domains;
  parsed.notBefore = result.notBefore;
  parsed.notAfter = result.notAfter;
  // 回填表单
  await formApi.setValues({
    domains,
    validRange: [result.notBefore, result.notAfter],
  });
}

async function onSubmit(values: Record<string, any>) {
  if (!isEdit.value && !String(values.privateKey ?? '').includes('PRIVATE KEY')) {
    message.error('请填写 PEM 格式证书私钥');
    return;
  }
  if (parsed.error) {
    message.warning(parsed.error);
    return;
  }
  const range = values.validRange ?? [];
  if (range.length < 2) {
    message.warning('请选择完整的有效期范围');
    return;
  }
  const notBefore = typeof range[0] === 'string' ? range[0] : String(range[0]);
  const notAfter = typeof range[1] === 'string' ? range[1] : String(range[1]);
  if (Date.parse(notAfter) <= Date.parse(notBefore)) {
    message.warning('有效期范围不正确：结束时间必须晚于开始时间');
    return;
  }

  modalApi.lock();
  try {
    const centerScope: 'all' | number[] =
      values.scopeType === 'partial' && Array.isArray(values.centerIds)
        ? values.centerIds.map(Number)
        : 'all';
    const payload = {
      certificate: String(values.certificate),
      certificateChain: String(values.certificateChain ?? '').trim() || undefined,
      centerScope,
      domains: String(values.domains),
      name: String(values.name),
      notAfter: notAfter.slice(0, 19).replaceAll('T', ' '),
      notBefore: notBefore.slice(0, 19).replaceAll('T', ' '),
      privateKey: String(values.privateKey),
    };
    if (isEdit.value) {
      await updateCertificateApi(editId.value, payload);
      message.success('证书已更新');
    } else {
      await createCertificateApi(payload);
      message.success('证书录入成功');
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
    <Alert
      v-if="isEdit && !canReadSecrets"
      class="mb-4"
      message="当前角色不能查看已有私钥。提交时留空会保留已保存的私钥。"
      show-icon
      type="info"
    />
    <div v-if="parsed.error" class="mb-2 rounded border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-600 dark:border-red-900 dark:bg-red-950 dark:text-red-400">
      {{ parsed.error }}
    </div>
    <div v-else-if="parsed.domains" class="mb-2 rounded border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs text-emerald-700 dark:border-emerald-900 dark:bg-emerald-950 dark:text-emerald-400">
      解析成功：CN = {{ parsed.subjectCn || '-' }}；SAN 域名 {{ parsed.domains }}；有效期 {{ parsed.notBefore.slice(0, 10) }} ~ {{ parsed.notAfter.slice(0, 10) }}
    </div>
    <Form />
  </Modal>
</template>
