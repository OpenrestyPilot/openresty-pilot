import type { RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = [
  {
    children: [
      {
        meta: {
          icon: 'lucide:users',
          activeIcon: 'ph:users-fill',
          order: 0,
          title: '用户管理',
        },
        name: 'SystemUser',
        path: 'users',
        component: () => import('#/views/system/user/index.vue'),
      },
      {
        meta: {
          icon: 'lucide:shield',
          activeIcon: 'ph:shield-fill',
          order: 1,
          title: '角色管理',
        },
        name: 'SystemRole',
        path: 'roles',
        component: () => import('#/views/system/role/index.vue'),
      },
      {
        meta: {
          icon: 'lucide:key-round',
          activeIcon: 'ph:key-fill',
          order: 2,
          title: '权限定义',
        },
        name: 'SystemPerm',
        path: 'perms',
        component: () => import('#/views/system/perm/index.vue'),
      },
      {
        meta: {
          icon: 'lucide:key-square',
          activeIcon: 'ph:key-fill',
          order: 3,
          title: 'API 调用方与密钥',
        },
        name: 'SystemConsumers',
        path: 'api-consumers',
        component: () => import('#/views/api-consumers/index.vue'),
      },
    ],
    meta: {
      icon: 'lucide:settings',
      activeIcon: 'ph:gear-fill',
      order: 40,
      title: '系统与权限',
    },
    name: 'System',
    path: '/system',
    redirect: '/system/users',
  },
];

export default routes;
