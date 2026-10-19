import { useAccessStore } from '@vben/stores';

import { baseRequestClient, requestClient } from '#/api/request';

export namespace AuthApi {
  /** 登录接口参数 */
  export interface LoginParams {
    /** OTP 动态口令（开启两步验证后必填） */
    otpCode?: string;
    password?: string;
    username?: string;
  }

  /** 登录接口返回值 */
  export interface LoginResult {
    accessToken: string;
  }

  export interface LoginConfig {
    captchaEnabled: boolean;
    /** Only present when an operator explicitly enables local-demo prefill. */
    password?: string;
    username: string;
  }

  export interface RefreshTokenResult {
    data: string;
    status: number;
  }
}

/**
 * 登录
 */
export async function loginApi(data: AuthApi.LoginParams) {
  return requestClient.post<AuthApi.LoginResult>('/auth/login', data);
}

/** 获取登录页用户名和验证码开关；密码预填默认关闭。 */
export async function getLoginConfigApi() {
  return requestClient.get<AuthApi.LoginConfig>('/auth/config');
}

/**
 * 刷新accessToken
 */
export async function refreshTokenApi() {
  const token = useAccessStore().accessToken;
  return baseRequestClient.post<AuthApi.RefreshTokenResult>(
    '/auth/refresh',
    undefined,
    {
      withCredentials: true,
      headers: token ? { Authorization: `Bearer ${token}` } : {},
    },
  );
}

/**
 * 退出登录
 */
export async function logoutApi() {
  return requestClient.post('/auth/logout', undefined, {
    withCredentials: true,
  });
}

/**
 * 获取用户权限码
 */
export async function getAccessCodesApi() {
  return requestClient.get<string[]>('/auth/codes');
}
