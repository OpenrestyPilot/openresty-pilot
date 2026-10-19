import type {
  ComponentRecordType,
  GenerateMenuAndRoutesOptions,
} from '@vben/types';

import { generateAccessible } from '@vben/access';
import { preferences } from '@vben/preferences';
import { useAccessStore } from '@vben/stores';
import { mapTree } from '@vben/utils';

import { message } from 'ant-design-vue';

import { getAllMenusApi } from '#/api';
import { BasicLayout, IFrameView } from '#/layouts';
import { $t } from '#/locales';

const forbiddenComponent = () => import('#/views/_core/fallback/forbidden.vue');

async function generateAccess(options: GenerateMenuAndRoutesOptions) {
  const pageMap: ComponentRecordType = import.meta.glob('../views/**/*.vue');

  const permissionByRoute: Record<string, string> = {
    AuditLog: 'orp:audit:read',
    ConfigPublish: 'orp:publish',
    SystemSetting: 'orp:read',
  };
  const requiredPermissions = ['orp:read', 'orp:audit:read', 'orp:publish', 'orp:node:operate', 'orp:system:manage', 'orp:secrets:read'];
  const assignedCodes = useAccessStore().accessCodes;
  const accessCodes = assignedCodes.includes('*')
    ? [...new Set([...assignedCodes, ...requiredPermissions])]
    : assignedCodes;
  const routes = mapTree(options.routes, (route) => {
    if (typeof route.name !== 'string' || route.name === 'Profile') return route;
    const authority = route.name === 'System' || ['SystemUser', 'SystemRole', 'SystemPerm', 'SystemConsumers'].includes(route.name)
      ? ['super']
      : [permissionByRoute[route.name] ?? 'orp:read'];
    route.meta = { ...route.meta!, authority };
    return route;
  });

  const layoutMap: ComponentRecordType = {
    BasicLayout,
    IFrameView,
  };

  return await generateAccessible(preferences.app.accessMode, {
    ...options,
    roles: [...(options.roles ?? []), ...accessCodes],
    routes,
    fetchMenuListAsync: async () => {
      message.loading({
        content: `${$t('common.loadingMenu')}...`,
        duration: 1.5,
      });
      return await getAllMenusApi();
    },
    // 可以指定没有权限跳转403页面
    forbiddenComponent,
    // 如果 route.meta.menuVisibleWithForbidden = true
    layoutMap,
    pageMap,
  });
}

export { generateAccess };
