/**
 * ORP 审计上下文（h3 事件适配层）
 *
 * 从 Nitro 请求事件中提取操作人与客户端 IP。
 * 注意：orp-store 本体已纯化（不依赖 h3/jwt），
 * 预览环境的 vite 内嵌 mock（web-antd/mock/orp-mock.ts）使用 connect 版平替实现。
 */
import type { EventHandlerRequest, H3Event } from 'h3';

import { verifyAccessToken } from './jwt-utils';

export function getOperator(event: H3Event<EventHandlerRequest>): string {
  const user = verifyAccessToken(event);
  return user?.realName || user?.username || '系统管理员';
}

export function getClientIp(event: H3Event<EventHandlerRequest>): string {
  return (
    event.headers.get('x-forwarded-for')?.split(',')[0]?.trim() || '127.0.0.1'
  );
}
