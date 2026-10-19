<script lang="ts" setup>
import type {
  HttpListener,
  HttpListenerPayload,
  LocationRule,
  OrpIpGroup,
  OrpIpPolicy,
  UpstreamGroup,
} from '#/api';

import { computed, reactive, ref, watch } from 'vue';

import { useVbenDrawer } from '@vben/common-ui';

import {
  AutoComplete,
  Button,
  Empty,
  Input,
  InputNumber,
  message,
  Popconfirm,
  RadioGroup,
  Select,
  Space,
  Switch,
  Tag,
  Tooltip,
} from 'ant-design-vue';

import { listIpGroupsApi, listUpstreamGroupsApi, updateHttpListenerApi } from '#/api';
import { useDirtyGuard } from '#/composables/use-dirty-guard';

defineOptions({ name: 'HttpRoutesDrawer' });

const emit = defineEmits<{ success: [] }>();

const { markDirty, onBeforeClose, resetDirty } = useDirtyGuard();

const listener = ref<HttpListener>();
const routes = reactive<LocationRule[]>([]);
const saving = ref(false);

/** 已录入的 Upstream 组选项（可搜索下拉） */
const upstreamOptions = ref<{ label: string; value: string }[]>([]);

/** Location 匹配方式选项 */
const MATCH_OPTIONS = [
  { label: 'prefix · 前缀匹配 location /path', value: 'prefix' },
  { label: '= · 精确匹配 location = /path', value: '=' },
  { label: '^~ · 前缀优先（跳过正则）', value: '^~' },
  { label: '~ · 区分大小写正则', value: '~' },
  { label: '~* · 不区分大小写正则', value: '~*' },
];

/** Location 类型选项（三类互斥） */
const TYPE_OPTIONS = [
  { label: '代理转发（proxy_pass）', value: 'proxy' },
  { label: '静态资源（root / alias）', value: 'static' },
  { label: '直接返回（return）', value: 'direct' },
];

const HTTP_METHOD_OPTIONS = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'].map((value) => ({
  label: value,
  value,
}));

/** 上游协议选项（与 Upstream 组名组合生成 proxy_pass 目标） */
const PROTOCOL_OPTIONS = [
  { label: 'http://', value: 'http' },
  { label: 'https://', value: 'https' },
];

/** IP 策略优先模式选项（allow 组 / deny 组谁先生成） */
const IP_PRIORITY_OPTIONS = [
  { label: '白名单优先', value: 'allow-first' },
  { label: '黑名单优先', value: 'deny-first' },
];

/** IP / CIDR 格式（IPv4 段 0-255、掩码 /0-/32；兼容 IPv6 及 /0-/128） */
const IPV4_CIDR_PATTERN =
  /^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(?:\/(?:3[0-2]|[12]?\d))?$/;
const IPV6_CIDR_PATTERN =
  /^(?:[0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}(?:\/(?:12[0-8]|1[01]\d|[0-9]?\d))?$/;

/** 生成默认 IP 策略状态（关闭态） */
function makeIpPolicy(): OrpIpPolicy {
  return { enabled: false, priority: 'allow-first', allowList: [], denyList: [] };
}

/** 克隆既有 IP 策略（编辑回显，避免直接引用 Mock 数据） */
function cloneIpPolicy(src?: OrpIpPolicy): OrpIpPolicy {
  return src
    ? {
        enabled: src.enabled,
        priority: src.priority,
        allowList: [...src.allowList],
        denyList: [...src.denyList],
      }
    : makeIpPolicy();
}

/** IP 组导入：可选组列表与下拉选项（选中后展开合并至目标名单并去重） */
const ipGroups = ref<OrpIpGroup[]>([]);
const ipGroupOptions = computed(() =>
  ipGroups.value.map((group) => ({
    label: `${group.name}（${group.members.length} 项）`,
    value: group.id,
  })),
);

async function loadIpGroups() {
  try {
    const res = await listIpGroupsApi({ page: 1, pageSize: 100 });
    ipGroups.value = res.items;
  } catch {
    // IP 组加载失败不阻塞抽屉主流程
  }
}

function importRouteGroup(
  route: LocationRule,
  groupId: number,
  target: 'allow' | 'deny',
) {
  const group = ipGroups.value.find((item) => item.id === groupId);
  if (!group || !route.ipPolicy) return;
  const current =
    target === 'allow' ? route.ipPolicy.allowList : route.ipPolicy.denyList;
  const merged = new Set(current);
  let added = 0;
  for (const member of group.members) {
    if (!merged.has(member)) {
      merged.add(member);
      added += 1;
    }
  }
  if (target === 'allow') {
    route.ipPolicy.allowList = [...merged];
  } else {
    route.ipPolicy.denyList = [...merged];
  }
  message.success(`已从 IP 组「${group.name}」导入 ${added} 项（自动去重）`);
}

/** 校验单条路由的 IP 策略列表，返回错误信息（null 表示通过） */
function validateRouteIpPolicy(route: LocationRule): null | string {
  const policy = route.ipPolicy;
  if (!policy?.enabled) return null;
  const checks: Array<[string[], string]> = [
    [policy.allowList, '白名单'],
    [policy.denyList, '黑名单'],
  ];
  for (const [list, name] of checks) {
    for (const item of list) {
      if (!IPV4_CIDR_PATTERN.test(item) && !IPV6_CIDR_PATTERN.test(item)) {
        return `「${item}」不是合法的 IP 地址或 CIDR 网段（IP ${name}，示例：192.168.1.10 / 10.0.0.0/8）`;
      }
    }
  }
  return null;
}

/** 常用 location {} 上下文指令预设 */
const LOCATION_DIRECTIVE_OPTIONS = [
  {
    label: 'proxy_set_header · 代理请求头（如 X-Real-IP $remote_addr）',
    value: 'proxy_set_header',
  },
  {
    label: 'proxy_cache_valid · 缓存有效期（如 200 12h）',
    value: 'proxy_cache_valid',
  },
  { label: 'expires · 客户端缓存（如 7d / max）', value: 'expires' },
  { label: 'add_header · 附加响应头（如 Cache-Control）', value: 'add_header' },
  { label: 'access_log · 访问日志（如 logs/x.log main / off）', value: 'access_log' },
  { label: 'deny / allow · 访问控制（如 deny 10.0.0.0/8）', value: 'deny' },
  { label: 'limit_req · 请求限速（如 zone=api burst=100）', value: 'limit_req' },
  { label: 'try_files · 文件回退（如 $uri $uri/ /index.html）', value: 'try_files' },
];

const NAME_PATTERN = /^[a-z_][a-z0-9_]*$/;

watch(routes, () => markDirty(), { deep: true });

const [Drawer, drawerApi] = useVbenDrawer<{ record?: HttpListener }>({
  class: 'w-[880px]',
  onBeforeClose,
  async onOpenChange(isOpen) {
    if (isOpen) {
      loadIpGroups();
      const record = drawerApi.getData()?.record;
      if (record) {
        listener.value = record;
        routes.splice(
          0,
          routes.length,
          ...record.routes.map((item): LocationRule => ({
            ...item,
            allowedMethods: [...(item.allowedMethods ?? [])],
            directives: (item.directives ?? []).map((d) => ({ ...d })),
            ipPolicy: cloneIpPolicy(item.ipPolicy),
            upstreamProtocol: (item.upstream ?? '').startsWith('https://')
              ? 'https'
              : 'http',
          })),
        );
        drawerApi.setState({
          title: `Location 路由 - ${record.domain}:${record.port}`,
        });
      }
      await loadUpstreams();
      resetDirty();
    }
  },
  title: 'Location 路由',
});

async function loadUpstreams() {
  try {
    const res = await listUpstreamGroupsApi({ page: 1, pageSize: 200 });
    upstreamOptions.value = res.items.map((g: UpstreamGroup) => ({
      label: `${g.name}（${g.nodes.length} 节点 · ${g.lbPolicy}）`,
      value: g.name,
    }));
  } catch {
    upstreamOptions.value = [];
  }
}

/** 从既有 upstream 值（如 http://web-frontend）解析出组名 */
function upstreamGroupName(value?: string): string {
  const group = (value ?? '')
    .replace(/^https?:\/\//, '')
    .split(':')[0]
    ?.trim();
  return group ?? '';
}

function typeLabel(type?: string) {
  if (type === 'static') return '静态资源';
  if (type === 'direct') return '直接返回';
  return '代理转发';
}

let tmpId = -1;
function addRoute() {
  routes.push({
    allowedMethods: [],
    directives: [],
    id: tmpId--,
    ipPolicy: makeIpPolicy(),
    matchType: 'prefix',
    path: '',
    proxyTimeoutMs: 30_000,
    rateLimitEnabled: false,
    ratePerSecond: 100,
    rateLimitBurst: 50,
    rateLimitNodelay: true,
    type: 'proxy',
    upstream: undefined,
    upstreamProtocol: 'http',
  });
}

function removeRoute(index: number) {
  routes.splice(index, 1);
}

function addRouteDirective(route: LocationRule) {
  route.directives = route.directives ?? [];
  route.directives.push({ name: '', value: '' });
}

function removeRouteDirective(route: LocationRule, index: number) {
  route.directives?.splice(index, 1);
}

function filterOption(input: string, option: { label: string }) {
  return option.label.toLowerCase().includes(input.toLowerCase());
}

/** 类型切换时清空互斥字段 */
function onTypeChange(route: LocationRule) {
  route.upstream = undefined;
  route.proxyTimeoutMs = undefined;
  route.rootPath = undefined;
  route.staticMode = undefined;
  route.indexFiles = undefined;
  route.autoindex = undefined;
  route.returnCode = undefined;
  route.returnBody = undefined;
  if (route.type === 'proxy') {
    route.proxyTimeoutMs = 30_000;
    route.upstreamProtocol = 'http';
  }
}

/** 统计信息 */
const stats = computed(() => {
  const proxy = routes.filter((r) => r.type === 'proxy').length;
  const statics = routes.filter((r) => r.type === 'static').length;
  const direct = routes.filter((r) => r.type === 'direct').length;
  return { direct, proxy, statics };
});

/** 校验全部路由，返回错误信息（null 表示通过） */
function validateRoutes(): null | string {
  const seenPath = new Set<string>();
  for (const [index, route] of routes.entries()) {
    const no = `第 ${index + 1} 条 Location`;
    const path = route.path.trim();
    if (!path.startsWith('/')) {
      return `${no} 匹配路径「${path || '空'}」必须以 / 开头`;
    }
    const key = `${route.matchType} ${path}`;
    if (seenPath.has(key)) {
      return `${no} 匹配规则「${key}」重复定义`;
    }
    seenPath.add(key);
    if (route.type === 'proxy') {
      if (!upstreamGroupName(route.upstream)) {
        return `${no}（代理转发）必须选择转发 Upstream 组`;
      }
    } else if (route.type === 'static') {
      if (!route.rootPath?.trim()) {
        return `${no}（静态资源）必须填写资源目录路径`;
      }
      if (!route.rootPath?.trim().startsWith('/')) {
        return `${no}（静态资源）资源目录必须是绝对路径（以 / 开头）`;
      }
    } else if (route.type === 'direct') {
      if (!route.returnCode || route.returnCode < 100 || route.returnCode > 599) {
        return `${no}（直接返回）返回状态码必须是 100-599`;
      }
    }
    if (route.rateLimitEnabled) {
      if (!Number.isInteger(route.ratePerSecond) || route.ratePerSecond! < 1 || route.ratePerSecond! > 100_000) {
        return `${no} 限流速率必须是 1–100000 的整数请求/秒`;
      }
      if (!Number.isInteger(route.rateLimitBurst) || route.rateLimitBurst! < 0 || route.rateLimitBurst! > 1_000_000) {
        return `${no} 限流突发额度必须是 0–1000000 的整数`;
      }
    }
    const allowedMethods = route.allowedMethods ?? [];
    if (new Set(allowedMethods).size !== allowedMethods.length || allowedMethods.some((method) => !HTTP_METHOD_OPTIONS.some((option) => option.value === method))) {
      return `${no} 允许的 HTTP 方法无效或重复`;
    }
    // Location 块指令校验
    if (route.directives?.length) {
      const seenName = new Set<string>();
      for (const [di, d] of route.directives.entries()) {
        const name = d.name.trim();
        const value = d.value.trim();
        if (!name || !value) {
          return `${no} 第 ${di + 1} 条块指令的指令名与取值均为必填项`;
        }
        if (!NAME_PATTERN.test(name)) {
          return `${no} 指令名「${name}」不合法：仅支持小写字母、数字与下划线`;
        }
        if (seenName.has(name)) {
          return `${no} 指令 ${name} 重复定义`;
        }
        seenName.add(name);
      }
    }
    // Location 级 IP 策略校验
    const ipError = validateRouteIpPolicy(route);
    if (ipError) {
      return `${no} ${ipError}`;
    }
  }
  return null;
}

async function onSave() {
  if (!listener.value) {
    return;
  }
  const error = validateRoutes();
  if (error) {
    message.warning(error);
    return;
  }
  saving.value = true;
  try {
    await updateHttpListenerApi(listener.value.id, {
      centerId: listener.value.centerId,
      directives: listener.value.directives ?? [],
      domain: listener.value.domain,
      port: listener.value.port,
      routes: routes.map((item) => {
        const base: NonNullable<HttpListenerPayload['routes']>[number] = {
          directives: (item.directives ?? []).map((d) => ({
            name: d.name.trim(),
            value: d.value.trim(),
          })),
          id: typeof item.id === 'number' && item.id > 0 ? item.id : undefined,
          allowedMethods: [...(item.allowedMethods ?? [])],
          matchType: item.matchType,
          path: item.path.trim(),
          rateLimitEnabled: item.rateLimitEnabled ?? false,
          ratePerSecond: item.ratePerSecond ?? 100,
          rateLimitBurst: item.rateLimitBurst ?? 50,
          rateLimitNodelay: item.rateLimitNodelay ?? true,
          type: item.type,
        };
        if (item.type === 'proxy') {
          base.proxyTimeoutMs = item.proxyTimeoutMs;
          base.upstream = `${item.upstreamProtocol || 'http'}://${upstreamGroupName(item.upstream)}`;
        } else if (item.type === 'static') {
          base.autoindex = item.autoindex ?? false;
          base.indexFiles = item.indexFiles?.trim() || 'index.html';
          base.rootPath = item.rootPath?.trim();
          base.staticMode = item.staticMode || 'root';
        } else if (item.type === 'direct') {
          base.returnBody = item.returnBody?.trim() ?? '';
          base.returnCode = item.returnCode;
        }
        base.ipPolicy = item.ipPolicy
          ? {
              enabled: item.ipPolicy.enabled,
              priority: item.ipPolicy.priority,
              allowList: [...item.ipPolicy.allowList],
              denyList: [...item.ipPolicy.denyList],
            }
          : undefined;
        return base;
      }),
      tlsCertId: listener.value.tlsCertId,
    });
    message.success('Location 路由已保存');
    resetDirty();
    drawerApi.close();
    emit('success');
  } catch {
    // 错误提示由请求拦截器统一处理
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <Drawer class="w-[880px]">
    <div class="flex flex-col gap-3">
      <div class="flex items-center justify-between">
        <span class="text-xs text-gray-500">
          在域名 {{ listener?.domain }} 下维护 Location 路由，三类互斥：代理转发 / 静态资源 / 直接返回
        </span>
        <Button size="small" type="primary" @click="addRoute">添加 Location</Button>
      </div>

      <div class="flex gap-2 text-xs">
        <Tag color="blue">代理 {{ stats.proxy }}</Tag>
        <Tag color="green">静态 {{ stats.statics }}</Tag>
        <Tag color="orange">直返 {{ stats.direct }}</Tag>
        <Tag>合计 {{ routes.length }}</Tag>
      </div>

      <Empty v-if="routes.length === 0" description="暂无 Location 路由" />

      <div
        v-for="(route, index) in routes"
        :key="route.id"
        class="rounded border border-gray-200 p-3 dark:border-gray-700"
      >
        <div class="mb-2 flex items-center gap-2">
          <Tag :color="route.type === 'proxy' ? 'blue' : route.type === 'static' ? 'green' : 'orange'">
            #{{ index + 1 }} {{ typeLabel(route.type) }}
          </Tag>
          <span class="flex-1 text-xs text-gray-400">location {{ route.matchType === 'prefix' ? '' : route.matchType + ' ' }}{{ route.path || '(待填写)' }}</span>
          <Popconfirm title="确认删除该 Location？" @confirm="removeRoute(index)">
            <Button danger size="small" type="link">删除</Button>
          </Popconfirm>
        </div>

        <div class="grid grid-cols-1 gap-2 md:grid-cols-[1.2fr_1fr_1.8fr]">
          <Input
            v-model:value="route.path"
            placeholder="匹配路径，如 /v2/*"
          />
          <Select
            v-model:value="route.matchType"
            :options="MATCH_OPTIONS"
            placeholder="匹配方式"
          />
          <Select
            v-model:value="route.type"
            :options="TYPE_OPTIONS"
            placeholder="路由类型"
            @change="onTypeChange(route)"
          />
        </div>

        <div class="mt-2 flex flex-col gap-2 md:flex-row md:items-center">
          <span class="w-24 shrink-0 text-xs text-gray-500">允许的 HTTP 方法</span>
          <Select
            v-model:value="route.allowedMethods"
            :options="HTTP_METHOD_OPTIONS"
            class="w-full md:flex-1"
            mode="multiple"
            placeholder="不限制方法"
          />
          <span class="text-xs text-gray-400">留空表示允许所有方法</span>
        </div>

        <!-- 代理转发（proxy_pass）：协议 + 上游地址（自适应）+ 超时 同排 -->
        <div v-if="route.type === 'proxy'" class="mt-2 flex flex-col gap-2 md:flex-row md:items-center">
          <Select
            v-model:value="route.upstreamProtocol"
            :options="PROTOCOL_OPTIONS"
            class="w-full md:w-28 md:shrink-0"
            placeholder="上游协议"
          />
          <Select
            v-model:value="route.upstream"
            :options="upstreamOptions"
            :filter-option="(input, option) => String(option?.value ?? '').toLowerCase().includes(input.toLowerCase())"
            class="w-full md:min-w-0 md:flex-1"
            placeholder="选择已录入的 Upstream 组"
            show-search
          />
          <InputNumber
            v-model:value="route.proxyTimeoutMs"
            :min="1000"
            :max="600_000"
            :step="1000"
            addon-after="ms"
            class="w-full md:w-40 md:shrink-0"
            placeholder="代理超时"
          />
        </div>

        <!-- 静态资源（root / alias） -->
        <div v-else-if="route.type === 'static'" class="mt-2 grid grid-cols-1 gap-2 md:grid-cols-[1.4fr_0.8fr_1.4fr_0.6fr]">
          <Input
            v-model:value="route.rootPath"
            placeholder="资源目录，如 /data/www/static"
          />
          <Select
            v-model:value="route.staticMode"
            :options="[
              { label: 'root', value: 'root' },
              { label: 'alias', value: 'alias' },
            ]"
            placeholder="挂载方式"
          />
          <Input
            v-model:value="route.indexFiles"
            placeholder="默认首页，如 index.html index.htm"
          />
          <Tooltip title="autoindex：目录无首页文件时允许列目录">
            <div class="flex items-center gap-1">
              <span class="text-xs text-gray-500">autoindex</span>
              <Switch v-model:checked="route.autoindex" size="small" />
            </div>
          </Tooltip>
        </div>

        <!-- 直接返回（return） -->
        <div v-else class="mt-2 grid grid-cols-1 gap-2 md:grid-cols-[0.7fr_2.3fr]">
          <InputNumber
            v-model:value="route.returnCode"
            :min="100"
            :max="599"
            class="w-full"
            placeholder="状态码"
            :precision="0"
          />
          <Input
            v-model:value="route.returnBody"
            placeholder="返回内容，如 ok / error: not found"
          />
        </div>

        <!-- 路由级限流：默认关闭，按客户端 IP 在当前节点限速 -->
        <div class="mt-2 rounded bg-gray-50 p-2 dark:bg-gray-800/60">
          <div class="flex flex-wrap items-center gap-3">
            <Tooltip title="按 $binary_remote_addr 限制此 Location 的请求速率；共享区仅在单节点内共享，不是跨节点全局配额。">
              <span class="text-xs font-medium text-gray-600 dark:text-gray-300">路由限流</span>
            </Tooltip>
            <Switch
              v-model:checked="route.rateLimitEnabled"
              checked-children="启用"
              size="small"
              un-checked-children="关闭"
            />
            <template v-if="route.rateLimitEnabled">
              <InputNumber
                v-model:value="route.ratePerSecond"
                :min="1"
                :max="100_000"
                :precision="0"
                addon-after="请求/秒"
                class="w-40"
              />
              <InputNumber
                v-model:value="route.rateLimitBurst"
                :min="0"
                :max="1_000_000"
                :precision="0"
                addon-after="突发额度"
                class="w-40"
              />
              <Tooltip title="开启后突发额度内的请求不排队延迟处理">
                <div class="flex items-center gap-1 text-xs text-gray-500">
                  <span>突发不延迟</span>
                  <Switch v-model:checked="route.rateLimitNodelay" size="small" />
                </div>
              </Tooltip>
            </template>
          </div>
        </div>

        <!-- Location 块指令 -->
        <div class="mt-2 rounded bg-gray-50 p-2 dark:bg-gray-800/60">
          <div class="mb-1 flex items-center justify-between">
            <Tooltip
              title="location {} 块内指令（如 proxy_set_header、expires、add_header），仅对当前 Location 生效。"
            >
              <span class="text-xs font-medium text-gray-600 dark:text-gray-300">块指令（{{ route.directives?.length ?? 0 }}）</span>
            </Tooltip>
            <Button size="small" type="dashed" @click="addRouteDirective(route)">+ 指令</Button>
          </div>
          <div
            v-for="(d, di) in route.directives"
            :key="di"
            class="mb-1 flex flex-wrap items-center gap-1"
          >
            <AutoComplete
              v-model:value="d.name"
              :filter-option="filterOption"
              :options="LOCATION_DIRECTIVE_OPTIONS"
              class="!w-56"
              placeholder="指令名，如 proxy_set_header"
              size="small"
            />
            <Input
              v-model:value="d.value"
              class="!w-64"
              placeholder="取值，如 X-Real-IP $remote_addr"
              size="small"
            />
            <Button danger size="small" type="link" @click="removeRouteDirective(route, di)">删</Button>
          </div>
          <div v-if="!route.directives?.length" class="py-1 text-center text-xs text-gray-400">
            暂无块指令
          </div>
        </div>

        <!-- IP 访问控制策略（location {} 块级 allow/deny） -->
        <div class="mt-2 rounded bg-gray-50 p-2 dark:bg-gray-800/60">
          <div class="mb-1 flex items-center justify-between">
            <Tooltip
              title="location {} 块级 IP 访问控制（allow/deny 指令组）。优先模式决定生成顺序：白名单优先 = allow 规则在前，命中白名单直接放行；黑名单优先 = deny 规则在前，命中黑名单直接拒绝。"
            >
              <span class="text-xs font-medium text-gray-600 dark:text-gray-300">IP 访问控制策略</span>
            </Tooltip>
            <Switch
              v-model:checked="route.ipPolicy!.enabled"
              checked-children="启用"
              size="small"
              un-checked-children="关闭"
            />
          </div>
          <template v-if="route.ipPolicy!.enabled">
            <div class="mb-2 flex flex-wrap items-center gap-2">
              <span class="w-16 shrink-0 text-xs text-gray-500">优先模式</span>
              <RadioGroup
                v-model:value="route.ipPolicy!.priority"
                :options="IP_PRIORITY_OPTIONS"
                option-type="button"
                size="small"
              />
            </div>
            <div class="mb-2 flex items-center gap-2">
              <span class="w-16 shrink-0 text-xs text-gray-500">白名单 allow</span>
              <Select
                v-model:value="route.ipPolicy!.allowList"
                :open="false"
                class="min-w-0 flex-1"
                mode="tags"
                placeholder="输入 IP 或 CIDR 网段后回车，如 192.168.1.10 / 10.0.0.0/8"
                size="small"
              />
              <Select
                :options="ipGroupOptions"
                :value="undefined"
                allow-clear
                class="w-36 shrink-0"
                option-filter-prop="label"
                placeholder="从 IP 组导入"
                show-search
                size="small"
                @select="(value: any) => importRouteGroup(route, Number(value), 'allow')"
              />
            </div>
            <div class="flex items-center gap-2">
              <span class="w-16 shrink-0 text-xs text-gray-500">黑名单 deny</span>
              <Select
                v-model:value="route.ipPolicy!.denyList"
                :open="false"
                class="min-w-0 flex-1"
                mode="tags"
                placeholder="输入 IP 或 CIDR 网段后回车，如 172.16.0.0/16 / 192.168.2.100"
                size="small"
              />
              <Select
                :options="ipGroupOptions"
                :value="undefined"
                allow-clear
                class="w-36 shrink-0"
                option-filter-prop="label"
                placeholder="从 IP 组导入"
                show-search
                size="small"
                @select="(value: any) => importRouteGroup(route, Number(value), 'deny')"
              />
            </div>
          </template>
          <div v-else class="py-1 text-center text-xs text-gray-400">
            未启用（不生成 allow / deny 指令）
          </div>
        </div>
      </div>

      <div v-if="upstreamOptions.length === 0" class="text-xs text-orange-500">
        当前尚未录入任何 Upstream 组，代理转发类 Location 需先前往「上游服务器组」模块创建。
      </div>
    </div>
    <template #footer>
      <Space>
        <Button :loading="saving" type="primary" @click="onSave">保存</Button>
        <Button @click="drawerApi.close()">取消</Button>
      </Space>
    </template>
  </Drawer>
</template>
