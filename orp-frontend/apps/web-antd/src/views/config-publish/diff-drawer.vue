<script lang="ts" setup>
import type { PublishDiffResult } from '#/api';
import type { PublishDiffLine } from '#/api';

import { computed, ref, watch } from 'vue';

import { useVbenDrawer, useVbenModal } from '@vben/common-ui';

import {
  Alert,
  Button,
  message,
  Spin,
  Switch,
  Tag,
} from 'ant-design-vue';

import { createPublishApi, getPublishDiffApi } from '#/api';

import PrecheckModal from './precheck-modal.vue';

defineOptions({ name: 'ConfigDiffDrawer' });

const emit = defineEmits<{ published: [batchId: number] }>();

const loading = ref(false);
const publishing = ref(false);
const result = ref<null | PublishDiffResult>(null);
const nodeId = ref<null | number>(null);

const MODULE_TEXT: Record<string, string> = {
  center: '中心管理',
  dns: 'DNS 解析器',
  http: 'HTTP 流量',
  node: '节点实例',
  rollback: '版本回滚',
  stream: 'Stream 流量',
  tls: 'TLS 证书',
  upstream: '上游服务器组',
};
const ACTION_TEXT: Record<string, string> = {
  create: '新增',
  delete: '删除',
  rollback: '回滚',
  update: '修改',
};
const ACTION_COLOR: Record<string, string> = {
  create: 'green',
  delete: 'red',
  rollback: 'orange',
  update: 'blue',
};
const RESOURCE_ACTION_TEXT: Record<string, string> = {
  create: '新增',
  delete: '删除',
  update: '修改',
};
const RESOURCE_ACTION_COLOR: Record<string, string> = {
  create: 'green',
  delete: 'red',
  update: 'blue',
};
const RESOURCE_TEXT: Record<string, string> = {
  certificates: 'TLS 证书',
  'dns-resolvers': 'DNS 解析器',
  'http-error-pages': 'HTTP 错误页',
  'http-listeners': 'HTTP 监听器',
  'http-log-format': 'HTTP 日志格式',
  'http-directives': 'HTTP 指令',
  'stream-services': 'Stream 服务',
  'stream-log-format': 'Stream 日志格式',
  'stream-directives': 'Stream 指令',
  'upstream-groups': '上游服务器组',
};

const [Drawer, drawerApi] = useVbenDrawer<{ nodeId?: number }>({
  class: 'w-[880px]',
  async onOpenChange(isOpen) {
    if (isOpen) {
      nodeId.value = drawerApi.getData()?.nodeId ?? null;
      result.value = null;
      expanded.value = new Set();
      loading.value = true;
      try {
        if (nodeId.value != null) {
          const data = await getPublishDiffApi(nodeId.value);
          result.value = data;
          drawerApi.setState({
            title: `配置比对 · ${data.node.name}（${data.centerName}）`,
          });
        }
      } finally {
        loading.value = false;
      }
    }
  },
  title: '配置比对',
});

/** 下发前语法预检弹窗（全部实例通过后方可继续下发） */
const [PrecheckView, precheckApi] = useVbenModal({
  connectedComponent: PrecheckModal,
});

const events = computed(() => result.value?.events ?? []);

/** 打开语法预检弹窗，替代直接下发 */
function onPublish() {
  if (nodeId.value == null || publishing.value) {
    return;
  }
  precheckApi.setData({ nodeIds: [nodeId.value] }).open();
}

/** 语法预检全部通过后执行单实例下发 */
async function onPrecheckConfirmed(ids: number[]) {
  publishing.value = true;
  try {
    const res = await createPublishApi(ids);
    message.success(
      `已创建下发任务，实例 ${result.value?.node.name ?? ''} 开始下发`,
    );
    precheckApi.close();
    emit('published', res.batchId);
  } catch {
    // 预检弹窗保持打开便于重新预检；错误提示由请求拦截器统一处理
  } finally {
    publishing.value = false;
  }
}

function lineClass(t: -1 | 0 | 1) {
  if (t === 1) {
    return 'bg-emerald-500/10';
  }
  return t === -1 ? 'bg-rose-500/10' : '';
}
function signClass(t: -1 | 0 | 1) {
  if (t === 1) {
    return 'text-emerald-600';
  }
  return t === -1 ? 'text-rose-600' : 'text-gray-300';
}
function textClass(t: -1 | 0 | 1) {
  if (t === 1) {
    return 'text-emerald-700 dark:text-emerald-400';
  }
  return t === -1 ? 'text-rose-700 dark:text-rose-400' : '';
}

// ===== 未变更行折叠（保留上下文，可逐段展开） =====
const CONTEXT = 2;
const foldOn = ref(true);
const expanded = ref(new Set<number>());

interface ViewRow {
  fold?: { count: number; ix: number };
  ix: number;
  kind: 'fold' | 'line';
  line?: PublishDiffLine;
}
const viewLines = computed<ViewRow[]>(() => {
  const lines = result.value?.diff ?? [];
  // 折叠开关关闭时全量展示
  if (!foldOn.value) {
    return lines.map((line, ix) => ({ ix, kind: 'line', line }));
  }
  const out: ViewRow[] = [];
  let run: number[] = [];
  const flush = () => {
    if (!run.length) return;
    if (run.length <= CONTEXT * 2 + 1 || expanded.value.has(run[CONTEXT]!)) {
      run.forEach((ix) => out.push({ ix, kind: 'line', line: lines[ix] }));
    } else {
      run
        .slice(0, CONTEXT)
        .forEach((ix) => out.push({ ix, kind: 'line', line: lines[ix] }));
      out.push({
        fold: { count: run.length - CONTEXT * 2, ix: run[CONTEXT]! },
        ix: run[CONTEXT]!,
        kind: 'fold',
      });
      run
        .slice(-CONTEXT)
        .forEach((ix) => out.push({ ix, kind: 'line', line: lines[ix] }));
    }
    run = [];
  };
  lines.forEach((line, ix) => {
    if (line.t === 0) {
      run.push(ix);
    } else {
      flush();
      out.push({ ix, kind: 'line', line });
    }
  });
  flush();
  return out;
});

function toggleFold(ix: number) {
  const next = new Set(expanded.value);
  if (next.has(ix)) {
    next.delete(ix);
  } else {
    next.add(ix);
  }
  expanded.value = next;
}

watch(foldOn, () => {
  expanded.value = new Set();
});

// ===== 字符级差异渲染（行内微小变动加深高亮） =====
interface SegSpan {
  hot: boolean;
  text: string;
}
function segSpans(line: PublishDiffLine): SegSpan[] {
  if (line.t === 0 || !line.segs?.length) {
    return [{ hot: false, text: line.s }];
  }
  const out: SegSpan[] = [];
  let pos = 0;
  for (const [a, b] of line.segs) {
    if (a > pos) {
      out.push({ hot: false, text: line.s.slice(pos, a) });
    }
    if (b > a) {
      out.push({ hot: true, text: line.s.slice(a, b) });
    }
    pos = Math.max(pos, b);
  }
  if (pos < line.s.length) {
    out.push({ hot: false, text: line.s.slice(pos) });
  }
  return out;
}
function charClass(t: -1 | 0 | 1) {
  return t === 1
    ? 'bg-emerald-500/40 font-semibold'
    : 'bg-rose-500/40 font-semibold';
}
</script>

<template>
  <Drawer>
    <div v-if="loading" class="flex justify-center py-20">
      <Spin />
    </div>
    <template v-else-if="result">
      <Alert
        v-if="result.node.status === 'offline'"
        class="mb-3"
        message="该实例当前处于离线状态，下发任务将以失败结束，请先恢复实例在线后重试"
        show-icon
        type="warning"
      />

      <div class="mb-4 flex flex-wrap items-center gap-x-5 gap-y-2 text-sm">
        <span>
          实例：<b>{{ result.node.name }}</b>
          <span class="ml-1 text-xs text-gray-400">{{ result.node.host }}</span>
        </span>
        <span>中心：{{ result.centerName }}</span>
        <span>配置版本：v{{ result.version }}</span>
        <span>最近下发：{{ result.lastPublishedAt ?? '从未下发' }}</span>
        <span class="flex items-center gap-2">
          <Tag :color="result.addCount ? 'green' : 'default'">
            期望新增 {{ result.addCount }} 行
          </Tag>
          <Tag :color="result.delCount ? 'red' : 'default'">
            期望删除 {{ result.delCount }} 行
          </Tag>
          <Tag color="purple">
            文本差异共 {{ result.addCount + result.delCount }} 行
          </Tag>
          <Tag v-if="result.resourceDiffAvailable" color="cyan">
            资源净差异 {{ result.resourceDiff.length }} 项
          </Tag>
        </span>
      </div>

      <Alert
        v-if="!result.lastPublishedAt"
        class="mb-3"
        message="该实例尚无成功下发基线；下方差异以实例初始配置为基准，首次成功下发后开始统计配置变更事件。"
        show-icon
        type="info"
      />

      <div v-if="result.resourceDiffAvailable" class="mb-4">
        <div class="mb-2 font-medium">
          发布后资源净差异（{{ result.resourceDiff.length }}）
        </div>
        <div
          v-if="result.resourceDiff.length"
          class="flex flex-col gap-1.5"
        >
          <div
            v-for="(change, ix) in result.resourceDiff"
            :key="`${change.kind}-${change.id}-${ix}`"
            class="flex flex-wrap items-center gap-2 rounded border border-gray-100 bg-gray-50 px-3 py-1.5 text-xs dark:border-gray-700 dark:bg-gray-900"
          >
            <Tag class="!m-0" :color="RESOURCE_ACTION_COLOR[change.action]">
              {{ RESOURCE_ACTION_TEXT[change.action] ?? change.action }}
            </Tag>
            <Tag class="!m-0" color="geekblue">
              {{ RESOURCE_TEXT[change.kind] ?? change.kind }}
            </Tag>
            <span class="font-mono">#{{ change.id }}</span>
            <span v-if="change.fields?.length" class="text-gray-500">
              变更字段：{{ change.fields.join('、') }}
            </span>
          </div>
        </div>
        <div v-else class="text-xs text-gray-500">
          当前资源配置与最近一次成功下发时一致。
        </div>
      </div>

      <div v-if="events.length" class="mb-4">
        <div class="mb-2 font-medium">变更操作记录（{{ events.length }} 条）</div>
        <div class="mb-2 text-xs text-gray-500">
          这是成功下发后的审计操作条数；资源净差异统计最终资源变化，文本差异统计 NGINX 配置行数，三者单位不同，不要求一一对应。
        </div>
        <div class="flex flex-col gap-1.5">
          <div
            v-for="(ev, ix) in events"
            :key="ix"
            class="flex flex-wrap items-center gap-2 rounded border border-gray-100 bg-gray-50 px-3 py-1.5 text-xs dark:border-gray-700 dark:bg-gray-900"
          >
            <span class="text-gray-400">{{ ev.ts }}</span>
            <Tag class="!m-0" :color="ACTION_COLOR[ev.action]">
              {{ ACTION_TEXT[ev.action] ?? ev.action }}
            </Tag>
            <Tag class="!m-0" color="geekblue">
              {{ MODULE_TEXT[ev.module] ?? ev.module }}
            </Tag>
            <span class="font-mono">{{ ev.target }}</span>
          </div>
        </div>
      </div>

      <div class="mb-2 flex items-center justify-between">
        <div class="font-medium">配置差异 · 期望配置 vs 当前运行配置</div>
        <label class="flex cursor-pointer items-center gap-2 text-xs text-gray-500">
          <Switch v-model:checked="foldOn" size="small" />
          折叠未变更行
        </label>
      </div>
      <div
        class="overflow-auto rounded-md border border-gray-200 bg-gray-50 font-mono text-xs leading-5 dark:border-gray-700 dark:bg-gray-900"
        style="max-height: 44vh"
      >
        <template v-for="row in viewLines" :key="row.ix">
          <div
            v-if="row.kind === 'fold'"
            class="flex cursor-pointer select-none items-center bg-gray-200/60 py-0.5 text-xs text-gray-500 hover:bg-gray-300/60 dark:bg-gray-800 dark:text-gray-400 dark:hover:bg-gray-700"
            @click="toggleFold(row.fold!.ix)"
          >
            <span class="pl-16">
              ⋯ 已折叠 {{ row.fold!.count }} 行未变更内容（点击展开）
            </span>
          </div>
          <div v-else class="flex" :class="lineClass(row.line!.t)">
            <span
              class="w-12 shrink-0 px-2 text-right text-gray-400 select-none"
            >
              {{ row.ix + 1 }}
            </span>
            <span
              class="w-5 shrink-0 text-center select-none"
              :class="signClass(row.line!.t)"
            >
              {{ row.line!.t === 1 ? '+' : row.line!.t === -1 ? '−' : '' }}
            </span>
            <span class="px-2 whitespace-pre" :class="textClass(row.line!.t)">
              <span
                v-for="(seg, j) in segSpans(row.line!)"
                :key="j"
                :class="seg.hot ? charClass(row.line!.t) : ''"
              >{{ seg.text }}</span>
            </span>
          </div>
        </template>
      </div>
    </template>

    <template #footer>
      <div class="flex w-full items-center justify-between">
        <span class="text-xs text-gray-400">
          「保存并下发」将以期望配置覆盖实例运行配置
        </span>
        <div class="flex gap-2">
          <Button @click="drawerApi.close()">取消</Button>
          <Button
            :disabled="!result"
            :loading="publishing"
            type="primary"
            @click="onPublish"
          >
            保存并下发
          </Button>
        </div>
      </div>
    </template>
  </Drawer>
  <PrecheckView @confirmed="onPrecheckConfirmed" />
</template>
