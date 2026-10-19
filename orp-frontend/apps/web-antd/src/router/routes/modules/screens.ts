import type { RouteRecordRaw } from 'vue-router';

/** 大屏入口：在单一页面中选择地图范围和展示模式。 */
const routes: RouteRecordRaw[] = [
  {
    path: '/screens',
    name: 'Screens',
    component: () => import('#/views/screens/index.vue'),
    meta: {
      icon: 'lucide:monitor-play',
      activeIcon: 'ph:monitor-play-fill',
      order: 1,
      title: '大屏展示',
    },
  },
];

export default routes;
