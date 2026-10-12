<script setup lang="ts">
import { Page } from '@vben/common-ui';
</script>

<template>
  <Page
    description="Go 控制面已实现端点的 OpenAPI 3.0 文档。"
    title="接口文档"
  >
    <!-- 同源静态托管：public/swagger 构建后位于 /swagger/，由 iframe 内嵌官方 swagger-ui 渲染 -->
    <iframe
      allow="fullscreen"
      class="h-[calc(100vh-260px)] min-h-[480px] w-full rounded-md border border-gray-200 bg-white dark:border-gray-700"
      src="swagger/index.html"
      title="ORP API 文档（Swagger UI）"
    />
  </Page>
</template>
