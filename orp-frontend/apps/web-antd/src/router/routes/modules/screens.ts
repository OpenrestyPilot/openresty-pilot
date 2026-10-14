import type { RouteRecordRaw } from 'vue-router';

/** 大屏展示：跳转到匿名独立项目 orp-screen */
const routes: RouteRecordRaw[] = [
  {
    path: '/screens',
    name: 'Screens',
    meta: {
      icon: 'lucide:monitor-play',
      activeIcon: 'ph:monitor-play-fill',
      order: 1,
      title: '大屏展示',
    },
    redirect: '/screens/world-2d',
    children: [
      {
        path: 'world-2d',
        name: 'ScreenWorld2d',
        component: () => import('#/views/screens/index.vue'),
        meta: {
          icon: 'lucide:globe-2',
          activeIcon: 'ph:globe-hemisphere-west-fill',
          order: 1,
          title: '世界地图（2D）',
        },
        props: { map: 'world', mode: '2d' },
      },
      {
        path: 'world-3d',
        name: 'ScreenWorld3d',
        component: () => import('#/views/screens/index.vue'),
        meta: {
          icon: 'lucide:globe',
          activeIcon: 'ph:globe-fill',
          order: 2,
          title: '世界地图（3D）',
        },
        props: { map: 'world', mode: '3d' },
      },
      {
        path: 'sichuan-2d',
        name: 'ScreenSichuan2d',
        component: () => import('#/views/screens/index.vue'),
        meta: {
          icon: 'lucide:map',
          activeIcon: 'ph:map-trifold-fill',
          order: 3,
          title: '四川省地图（2D）',
        },
        props: { map: 'sichuan', mode: '2d' },
      },
      {
        path: 'sichuan-3d',
        name: 'ScreenSichuan3d',
        component: () => import('#/views/screens/index.vue'),
        meta: {
          icon: 'lucide:mountain',
          activeIcon: 'ph:mountains-fill',
          order: 4,
          title: '四川省地图（3D）',
        },
        props: { map: 'sichuan', mode: '3d' },
      },
      {
        path: 'chengdu-2d',
        name: 'ScreenChengdu2d',
        component: () => import('#/views/screens/index.vue'),
        meta: {
          icon: 'lucide:map-pin',
          activeIcon: 'ph:map-pin-fill',
          order: 5,
          title: '成都市地图（2D）',
        },
        props: { map: 'chengdu', mode: '2d' },
      },
      {
        path: 'chengdu-3d',
        name: 'ScreenChengdu3d',
        component: () => import('#/views/screens/index.vue'),
        meta: {
          icon: 'lucide:building-2',
          activeIcon: 'ph:buildings-fill',
          order: 6,
          title: '成都市地图（3D）',
        },
        props: { map: 'chengdu', mode: '3d' },
      },
    ],
  },
];

export default routes;
