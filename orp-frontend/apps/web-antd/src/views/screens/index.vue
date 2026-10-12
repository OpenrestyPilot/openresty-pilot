<script setup lang="ts">
import { computed, onActivated, onBeforeUnmount, onDeactivated, onMounted, ref } from 'vue';
import type { DashboardMetrics, DashboardTopRankings } from '#/api/orp/types';

import { consumeDashboardEvents } from '#/api/orp/dashboard';
/**
 * 大屏展示页面壳：iframe 内嵌 public/screens/index.html（深色科技大屏）。
 * 路由 props 注入 map（world/sichuan/chengdu）与 mode（2d/3d）。
 */
const props = withDefaults(
  defineProps<{
    map?: string;
    mode?: string;
  }>(),
  { map: 'world', mode: '2d' },
);

const src = computed(() => `/screens/index.html?map=${encodeURIComponent(props.map)}&mode=${encodeURIComponent(props.mode)}`);
const frame = ref<HTMLIFrameElement>();
let streamController: AbortController | null = null;
let latestData: null | { metrics: DashboardMetrics; rankings: DashboardTopRankings } = null;
let streamActive = false;

function pushLatest() {
  if (!latestData) return;
  frame.value?.contentWindow?.postMessage(
    { type: 'orp-live-dashboard', ...latestData },
    window.location.origin,
  );
}

async function connect() {
  streamController?.abort();
  streamController = new AbortController();
  const signal = streamController.signal;
  while (!signal.aborted) {
    try {
      await consumeDashboardEvents({ range: '1h' }, signal, ({ metrics, rankings }) => {
        latestData = { metrics, rankings };
        pushLatest();
      }, () => {
        frame.value?.contentWindow?.postMessage(
          { type: 'orp-live-dashboard-error' },
          window.location.origin,
        );
      });
    } catch {
      if (signal.aborted) return;
      frame.value?.contentWindow?.postMessage(
        { type: 'orp-live-dashboard-error' },
        window.location.origin,
      );
      await new Promise<void>((resolve) => setTimeout(resolve, 3000));
    }
  }
}

function startStream() {
  if (streamActive) return;
  streamActive = true;
  void connect();
}

function stopStream() {
  streamActive = false;
  streamController?.abort();
  streamController = null;
}

onMounted(startStream);
onActivated(startStream);
onDeactivated(stopStream);
onBeforeUnmount(() => {
  stopStream();
});
</script>

<template>
  <div class="h-[calc(100vh-64px)] w-full overflow-hidden rounded-md">
    <iframe
      ref="frame"
      allow="fullscreen"
      class="h-full w-full border-0"
      :src="src"
      title="ORP 实时请求大屏"
      @load="pushLatest"
    />
  </div>
</template>
