<script lang="ts" setup>
import type { NodeItem, NodeMetrics } from '#/api';

import { ref, watch } from 'vue';

import { useVbenDrawer } from '@vben/common-ui';

import { Descriptions, DescriptionsItem, Progress, Spin, Tag, message } from 'ant-design-vue';

import { consumeNodeMetricEvents } from '#/api';

defineOptions({ name: 'NodeMetricsDrawer' });

const loading = ref(false);
const node = ref<NodeItem>();
const metrics = ref<NodeMetrics>();
const connected = ref(false);
const updatedAt = ref('');
let controller: AbortController | undefined;

const STATUS_COLOR: Record<string, string> = {
  maintenance: 'processing',
  offline: 'default',
  online: 'success',
};

const STATUS_TEXT: Record<string, string> = {
  maintenance: '维护中',
  offline: '离线',
  online: '在线',
};

function formatBandwidth(value: number | undefined) {
  if (value === undefined || !Number.isFinite(value) || value < 0) return '-';
  if (value > 0 && value < 0.001) return '< 0.001';
  return new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 3 }).format(value);
}

function formatUptime(days: number | undefined) {
  if (days === undefined || !Number.isFinite(days) || days < 0) return '-';
  const totalMinutes = Math.floor(days * 24 * 60);
  const wholeDays = Math.floor(totalMinutes / (24 * 60));
  const hours = Math.floor((totalMinutes % (24 * 60)) / 60);
  const minutes = totalMinutes % 60;
  const parts = [];
  if (wholeDays) parts.push(`${wholeDays} 天`);
  if (hours) parts.push(`${hours} 小时`);
  if (minutes || parts.length === 0) parts.push(`${minutes} 分钟`);
  return parts.join(' ');
}

function stopMetricsStream() {
  controller?.abort();
  controller = undefined;
  connected.value = false;
}

async function startMetricsStream(id: number) {
  stopMetricsStream();
  controller = new AbortController();
  const current = controller;
  loading.value = !metrics.value;
  try {
    await consumeNodeMetricEvents(
      id,
      current.signal,
      (snapshot) => {
        metrics.value = snapshot;
        updatedAt.value = new Date().toLocaleTimeString();
        loading.value = false;
      },
      (state) => {
        connected.value = state;
      },
    );
  } catch (error) {
    if (!current.signal.aborted) {
      message.error(error instanceof Error ? error.message : '节点指标连接失败');
    }
  } finally {
    if (controller === current) {
      loading.value = false;
      connected.value = false;
    }
  }
}

const [Drawer, drawerApi] = useVbenDrawer<{ record?: NodeItem }>({
  class: 'w-[560px]',
  onOpenChange(isOpen) {
    if (isOpen) {
      const record = drawerApi.getData()?.record;
      if (record) {
        node.value = record;
        metrics.value = undefined;
        updatedAt.value = '';
        startMetricsStream(record.id);
      }
    } else {
      stopMetricsStream();
    }
  },
  title: '节点指标',
});

watch(
  () => node.value?.id,
  () => {
    drawerApi.setState({
      title: node.value ? `节点指标 - ${node.value.name}` : '节点指标',
    });
  },
);
</script>

<template>
  <Drawer>
    <div class="mb-3 flex items-center justify-between text-xs">
      <span>
        <Tag :color="connected ? 'success' : 'default'">
          {{ connected ? 'SSE 实时连接中 · 1 秒刷新' : '正在连接指标流…' }}
        </Tag>
      </span>
      <span v-if="updatedAt" class="text-gray-400">最近更新 {{ updatedAt }}</span>
    </div>
    <Spin :spinning="loading">
      <div class="flex flex-col gap-4">
        <Descriptions
          :column="2"
          bordered
          size="small"
          title="基础信息"
        >
          <DescriptionsItem label="节点名称" :span="1">
            {{ node?.name }}
          </DescriptionsItem>
          <DescriptionsItem label="运行状态" :span="1">
            <Tag v-if="node" :color="STATUS_COLOR[node.status]">
              {{ STATUS_TEXT[node.status] }}
            </Tag>
          </DescriptionsItem>
          <DescriptionsItem label="主机地址" :span="1">
            {{ node?.host }}
          </DescriptionsItem>
          <DescriptionsItem label="所属中心" :span="1">
            {{ node?.centerName }}
          </DescriptionsItem>
          <DescriptionsItem label="活动版本" :span="1">
            {{ node?.activeVersion }}
          </DescriptionsItem>
          <DescriptionsItem label="通信端点" :span="1">
            {{ node?.controlEndpoint }}
          </DescriptionsItem>
        </Descriptions>

        <Descriptions :column="2" bordered size="small" title="运行指标">
          <DescriptionsItem label="CPU 使用率">
            <div class="flex items-center gap-2">
              <Progress
                :percent="metrics?.cpuPercent ?? 0"
                :status="(metrics?.cpuPercent ?? 0) > 80 ? 'exception' : 'normal'"
                :stroke-width="10"
                size="small"
                style="max-width: 140px; min-width: 100px;"
              />
            </div>
          </DescriptionsItem>
          <DescriptionsItem label="内存使用率">
            <div class="flex items-center gap-2">
              <Progress
                :percent="metrics?.memPercent ?? 0"
                :status="(metrics?.memPercent ?? 0) > 80 ? 'exception' : 'normal'"
                :stroke-width="10"
                size="small"
                style="max-width: 140px; min-width: 100px;"
              />
            </div>
          </DescriptionsItem>
          <DescriptionsItem label="QPS">
            {{ metrics?.qps.toLocaleString() ?? '-' }}
          </DescriptionsItem>
          <DescriptionsItem label="活动连接数">
            {{ metrics?.connections.toLocaleString() ?? '-' }}
          </DescriptionsItem>
          <DescriptionsItem label="入带宽 (Mbps)">
            {{ formatBandwidth(metrics?.bandwidthInMbps) }}
          </DescriptionsItem>
          <DescriptionsItem label="出带宽 (Mbps)">
            {{ formatBandwidth(metrics?.bandwidthOutMbps) }}
          </DescriptionsItem>
          <DescriptionsItem label="24h 请求量">
            {{ metrics?.requestTotal24h.toLocaleString() ?? '-' }}
          </DescriptionsItem>
          <DescriptionsItem label="错误率">
            {{ metrics ? `${metrics.errorRatePercent}%` : '-' }}
          </DescriptionsItem>
          <DescriptionsItem label="持续运行" :span="2">
            {{ formatUptime(metrics?.uptimeDays) }}
          </DescriptionsItem>
        </Descriptions>
      </div>
    </Spin>
  </Drawer>
</template>
