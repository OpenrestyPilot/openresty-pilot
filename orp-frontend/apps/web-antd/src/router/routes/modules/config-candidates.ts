import type { RouteRecordRaw } from 'vue-router';

const routes: RouteRecordRaw[] = [
  {
    meta: {
      icon: 'lucide:files',
      activeIcon: 'ph:files-fill',
      order: 7.6,
      title: '配置候选与校验',
    },
    name: 'ConfigCandidates',
    path: '/config-candidates',
    component: () => import('#/views/config-candidates/index.vue'),
  },
];

export default routes;
