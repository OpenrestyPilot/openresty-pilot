<script lang="ts" setup>
import type { NodeItem } from '#/api';

import { ref } from 'vue';

import { useVbenModal } from '@vben/common-ui';
import { Button, Input, message, Radio, Spin } from 'ant-design-vue';

import { bootstrapNodeAgentApi } from '#/api';

const kind = ref<'binary' | 'container'>('binary');
const loading = ref(false);
const commands = ref<{ curl: string; docker: string; wget: string }>();
const nodeName = ref('');

const [Modal, modalApi] = useVbenModal<{ record?: NodeItem }>({
  fullscreenButton: false,
  onOpenChange(open) {
    if (open) void issue();
    else commands.value = undefined;
  },
});

async function issue() {
  const node = modalApi.getData()?.record;
  if (!node) return;
  nodeName.value = node.name;
  modalApi.setState({ title: `快速接入 · ${node.name}` });
  loading.value = true;
  modalApi.lock();
  try {
    const result = await bootstrapNodeAgentApi(node.id);
    commands.value = result.commands;
  } catch {
    commands.value = undefined;
  } finally {
    loading.value = false;
    modalApi.unlock();
  }
}

const command = () => kind.value === 'container' ? commands.value?.docker : commands.value?.curl;

async function copyCommand() {
  const value = command();
  if (!value) return;
  try {
    await navigator.clipboard.writeText(value);
    message.success('接入命令已复制');
  } catch {
    message.error('复制失败，请手动选择命令复制');
  }
}

async function copyWget() {
  if (!commands.value?.wget) return;
  try {
    await navigator.clipboard.writeText(commands.value.wget);
    message.success('wget 接入命令已复制');
  } catch {
    message.error('复制失败，请手动选择命令复制');
  }
}
</script>

<template>
  <Modal class="w-[760px]">
    <Spin :spinning="loading">
      <div class="space-y-4 text-sm">
        <p class="text-gray-500">为「{{ nodeName }}」签发新的 Agent Token。Token 只在本次显示；重新签发会立即使旧 Token 失效。</p>
        <Radio.Group v-model:value="kind" button-style="solid">
          <Radio.Button value="binary">二进制 OpenResty</Radio.Button>
          <Radio.Button value="container">Docker 容器</Radio.Button>
        </Radio.Group>
        <div v-if="commands" class="space-y-2">
          <div class="font-medium">{{ kind === 'binary' ? '在 OpenResty 服务器上执行' : '替换容器启动命令' }}</div>
          <Input.TextArea :value="kind === 'container' ? commands.docker : commands.curl" :auto-size="{ minRows: 5, maxRows: 12 }" readonly />
          <div class="flex items-center gap-2">
            <Button type="primary" @click="copyCommand">复制命令</Button>
            <Button v-if="kind === 'binary'" @click="copyWget">复制 wget 命令</Button>
          </div>
          <p class="text-xs text-amber-700">命令包含节点凭据，请只在目标节点执行，不要粘贴到公开日志或工单。</p>
        </div>
      </div>
    </Spin>
  </Modal>
</template>
