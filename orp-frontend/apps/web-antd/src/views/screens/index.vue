<script setup lang="ts">
import { computed, onMounted } from 'vue';

const props = withDefaults(
  defineProps<{
    map?: string;
    mode?: string;
  }>(),
  { map: 'world', mode: '2d' },
);

const target = computed(() => {
  const base = import.meta.env.VITE_ORP_SCREEN_URL || '/screen/';
  const url = new URL(base, window.location.origin);
  url.searchParams.set('map', props.map);
  url.searchParams.set('mode', props.mode);
  return url.toString();
});

onMounted(() => {
  window.location.assign(target.value);
});
</script>

<template>
  <main class="flex h-[calc(100vh-64px)] items-center justify-center text-muted-foreground">
    正在打开独立大屏…
    <a class="sr-only" :href="target">无法自动跳转？点此打开</a>
  </main>
</template>
