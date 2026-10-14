<script lang="ts" setup>
import type { RbacPermissionNode, RbacRoleItem } from '#/api';

import { computed, ref } from 'vue';

import { useVbenDrawer } from '@vben/common-ui';

import { Alert, Button, Checkbox, Input, message, Spin, Tag } from 'ant-design-vue';

import {
  assignRbacRolePermissionsApi,
  getRbacRolePermissionsApi,
  listRbacPermissionsApi,
} from '#/api';

defineOptions({ name: 'SystemRolePermDrawer' });

const emit = defineEmits<{ success: [] }>();

const loading = ref(false);
const saving = ref(false);
const tree = ref<RbacPermissionNode[]>([]);
const checkedKeys = ref<string[]>([]);
const keyword = ref('');
const record = ref<null | RbacRoleItem>(null);

/** 全量权限键集合 */
const allKeys = computed(() => {
  const out: string[] = [];
  const walk = (nodes: RbacPermissionNode[]) => {
    nodes.forEach((n) => {
      out.push(n.key);
      if (n.children) walk(n.children);
    });
  };
  walk(tree.value);
  return out;
});

/** 关键字过滤后的树（命中节点的父链保留） */
const filteredTree = computed(() => {
  const kw = keyword.value.trim().toLowerCase();
  if (!kw) return tree.value;
  const filter = (nodes: RbacPermissionNode[]): RbacPermissionNode[] => {
    const out: RbacPermissionNode[] = [];
    nodes.forEach((n) => {
      const kids = n.children ? filter(n.children) : undefined;
      if (n.title.toLowerCase().includes(kw) || n.key.toLowerCase().includes(kw) || (kids && kids.length > 0)) {
        out.push({ ...n, children: kids });
      }
    });
    return out;
  };
  return filter(tree.value);
});

/** 统计：已选 / 可选总数 */
const statText = computed(() => `${[...new Set(checkedKeys.value)].length} / ${allKeys.value.length}`);

const halfChecked = computed(() => {
  const set = new Set(checkedKeys.value);
  const half: string[] = [];
  const walk = (nodes: RbacPermissionNode[]) => {
    nodes.forEach((n) => {
      if (n.children && n.children.length > 0) {
        walk(n.children);
        const childKeys = n.children.map((c) => c.key);
        const hit = childKeys.filter((k) => set.has(k)).length;
        if (hit > 0 && hit < childKeys.length) half.push(n.key);
      }
    });
  };
  walk(tree.value);
  return half;
});

/** 勾选父节点时联动勾选全部子节点 */
function onNodeCheck(node: RbacPermissionNode, event: { target: { checked: boolean } }) {
  const checked = event.target.checked;
  const keys: string[] = [node.key];
  const walk = (ns: RbacPermissionNode[]) => ns.forEach((n) => { keys.push(n.key); if (n.children) walk(n.children); });
  if (node.children) walk(node.children);
  const set = new Set(checkedKeys.value);
  keys.forEach((k) => (checked ? set.add(k) : set.delete(k)));
  checkedKeys.value = [...set];
}

const [Drawer, drawerApi] = useVbenDrawer<{ record?: RbacRoleItem }>({
  class: 'w-[480px]',
  footer: false,
  async onOpenChange(isOpen: boolean) {
    if (isOpen) {
      record.value = drawerApi.getData()?.record ?? null;
      keyword.value = '';
      checkedKeys.value = [];
      drawerApi.setState({
        title: `分配权限 · ${record.value?.name ?? ''}`,
      });
      if (tree.value.length === 0) {
        loading.value = true;
        try {
          tree.value = await listRbacPermissionsApi();
        } finally {
          loading.value = false;
        }
      }
      if (record.value) {
        loading.value = true;
        try {
          const res = await getRbacRolePermissionsApi(record.value.id);
          checkedKeys.value = res.permissionKeys;
        } finally {
          loading.value = false;
        }
      }
    }
  },
  title: '分配权限',
});

async function onSave() {
  if (!record.value) return;
  saving.value = true;
  try {
    await assignRbacRolePermissionsApi(record.value.id, checkedKeys.value);
    message.success(`角色 ${record.value.name} 权限已更新`);
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
  <Drawer>
    <div class="flex h-full flex-col gap-3">
          <Alert
        :message="`已勾选 ${statText} 项权限。发布、节点运维、系统运维和审计权限彼此独立。`"
        show-icon
        type="info"
      />
      <Input
        v-model:value="keyword"
        allow-clear
        placeholder="按权限名称或标识搜索"
      />
      <Spin :spinning="loading" wrapper-class-name="flex-1 min-h-0 overflow-y-auto pr-1">
        <div class="space-y-2">
          <template v-for="node in filteredTree" :key="node.key">
            <!-- 顶级菜单 -->
            <div class="rounded-md border border-gray-200 dark:border-gray-700">
              <div class="flex items-center justify-between border-b border-gray-100 px-3 py-2 dark:border-gray-800">
                <Checkbox
                  :checked="checkedKeys.includes(node.key)"
                  :indeterminate="halfChecked.includes(node.key)"
                  @change="(e: any) => onNodeCheck(node, e)"
                >
                  <span class="text-sm font-medium">{{ node.title }}</span>
                </Checkbox>
                <Tag class="!m-0" color="blue">{{ node.key }}</Tag>
              </div>
              <div v-if="node.description" class="px-3 pt-2 text-xs text-gray-500">{{ node.description }}</div>
              <div class="space-y-1.5 px-3 py-2">
                <template v-for="child in node.children ?? []" :key="child.key">
                  <div v-if="child.children && child.children.length > 0" class="rounded bg-gray-50 p-2 dark:bg-gray-800/60">
                    <div class="flex items-center justify-between">
                      <Checkbox
                        :checked="checkedKeys.includes(child.key)"
                        :indeterminate="halfChecked.includes(child.key)"
                        @change="(e: any) => onNodeCheck(child, e)"
                      >
                        <span class="text-sm">{{ child.title }}</span>
                      </Checkbox>
                      <span class="font-mono text-xs text-gray-400">{{ child.key }}</span>
                    </div>
                    <div class="mt-1.5 flex flex-wrap gap-x-4 gap-y-1 pl-7">
                      <Checkbox
                        v-for="leaf in child.children"
                        :key="leaf.key"
                        :checked="checkedKeys.includes(leaf.key)"
                        @change="(e: any) => onNodeCheck(leaf, e)"
                      >
                        <span class="text-xs">{{ leaf.title }}</span>
                      </Checkbox>
                    </div>
                  </div>
                  <div v-else class="flex items-center justify-between pl-1">
                    <Checkbox
                      :checked="checkedKeys.includes(child.key)"
                      @change="(e: any) => onNodeCheck(child, e)"
                    >
                      <span class="text-xs">{{ child.title }}</span>
                    </Checkbox>
                    <span class="font-mono text-xs text-gray-400">{{ child.key }}</span>
                  </div>
                </template>
              </div>
            </div>
          </template>
          <div v-if="!loading && filteredTree.length === 0" class="py-8 text-center text-sm text-gray-400">
            未命中任何权限节点
          </div>
        </div>
      </Spin>
      <div class="flex justify-end gap-2 border-t border-gray-100 pt-3 dark:border-gray-800">
        <Button @click="drawerApi.close()">取消</Button>
        <Button :loading="saving" type="primary" @click="onSave">保存分配</Button>
      </div>
    </div>
  </Drawer>
</template>
