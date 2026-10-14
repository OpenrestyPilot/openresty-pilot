<script lang="ts" setup>
import type { VbenFormSchema } from '@vben/common-ui';

import { computed, markRaw, nextTick, onMounted, ref } from 'vue';

import {
  AuthenticationLogin,
  SliderCaptcha,
  z,
} from '@vben/common-ui';
import { $t } from '@vben/locales';

import { getLoginConfigApi } from '#/api/core/auth';
import { useAuthStore } from '#/store';

defineOptions({ name: 'Login' });

const authStore = useAuthStore();
const loginFormRef = ref<InstanceType<typeof AuthenticationLogin> | null>(null);
const captchaEnabled = ref(false);
const defaultCredentials = ref({ username: '', password: '' });

onMounted(async () => {
  try {
    const config = await getLoginConfigApi();
    defaultCredentials.value = {
      username: config.username || '',
      password: config.password || '',
    };
    captchaEnabled.value = config.captchaEnabled;
    await nextTick();
    const formApi = loginFormRef.value?.getFormApi();
    formApi?.setFieldValue('username', defaultCredentials.value.username);
    formApi?.setFieldValue('password', defaultCredentials.value.password);
  } catch {
    // 配置接口不可用时仍允许手动输入账号密码。
  }
});

const formSchema = computed((): VbenFormSchema[] => {
  return [
    {
      component: 'VbenInput',
      componentProps: {
        placeholder: $t('authentication.usernameTip'),
      },
      defaultValue: defaultCredentials.value.username,
      fieldName: 'username',
      label: $t('authentication.username'),
      rules: z.string().min(1, { message: $t('authentication.usernameTip') }),
    },
    {
      component: 'VbenInputPassword',
      componentProps: {
        placeholder: $t('authentication.password'),
      },
      defaultValue: defaultCredentials.value.password,
      fieldName: 'password',
      label: $t('authentication.password'),
      rules: z.string().min(1, { message: $t('authentication.passwordTip') }),
    },
    ...(captchaEnabled.value ? [{
      component: markRaw(SliderCaptcha),
      fieldName: 'captcha',
      rules: z.boolean().refine((value) => value, {
        message: $t('authentication.verifyRequiredTip'),
      }),
    }] : []),
  ];
});
</script>

<template>
  <AuthenticationLogin
    ref="loginFormRef"
    :form-schema="formSchema"
    :loading="authStore.loginLoading"
    :show-code-login="false"
    :show-forget-password="false"
    :show-qrcode-login="false"
    :show-register="false"
    :show-remember-me="false"
    @submit="authStore.authLogin"
  />
</template>
