import { expect, test } from '@playwright/test';

const pages = [
  '控制面大盘',
  '中心管理',
  '节点实例',
  '上游服务器组',
  'HTTP 流量',
  'Stream 流量',
  'TLS 证书',
  'DNS 解析器',
  'IP 组管理',
  '配置比对与下发',
  '实时日志',
  '审计日志',
  '系统配置',
];

test('real UI pages call Go APIs and center CRUD persists', async ({ page }) => {
  test.setTimeout(15 * 60 * 1000);
  const apiResponses: Array<{ method: string; status: number; url: string }> = [];

  page.on('response', (response) => {
    if (new URL(response.url()).pathname.startsWith('/api/')) {
      apiResponses.push({
        method: response.request().method(),
        status: response.status(),
        url: new URL(response.url()).pathname,
      });
    }
  });

  await page.goto('/');
  if (new URL(page.url()).pathname.includes('/auth/login')) {
    test.info().annotations.push({
      type: 'manual-step',
      description: '请在浏览器窗口完成登录页滑块并提交；本测试会等待登录成功后继续。',
    });
    await page.waitForURL((url) => !url.pathname.includes('/auth/login'), {
      timeout: 12 * 60 * 1000,
    });
  }

  const sideMenu = page.getByRole('menu').first();
  for (const title of pages) {
    await sideMenu.getByText(title, { exact: true }).click();
    await expect(page.getByRole('main').getByText(title, { exact: true }).first()).toBeVisible();
    await page.waitForTimeout(250);
  }

  await sideMenu.getByText('系统与权限', { exact: true }).click();
  for (const title of ['用户管理', '角色管理', '权限定义']) {
    await sideMenu.getByText(title, { exact: true }).click();
    await expect(page.getByRole('main').getByText(title, { exact: true }).first()).toBeVisible();
  }

  await page.getByRole('button', { name: 'EN', exact: true }).click();
  const accountMenu = page.getByRole('menu').last();
  await accountMenu.getByText('个人中心', { exact: true }).click();
  await expect(page.getByRole('main').getByText('个人中心', { exact: true }).first()).toBeVisible();
  await page.getByRole('button', { name: 'EN', exact: true }).click();
  await page.getByRole('menu').last().getByText('接口文档', { exact: true }).click();
  await expect(page.getByRole('main').getByText('Go 控制面已实现端点的 OpenAPI 3.0 文档。')).toBeVisible();

  const failedApiResponses = apiResponses.filter((response) => response.status >= 500);
  expect(failedApiResponses, '页面读取时 Go API 不应返回 5xx').toEqual([]);
  expect(apiResponses.length, '页面访问期间应有真实 Go API 请求').toBeGreaterThan(10);

  await page.goto('/centers');
  await expect(page.getByRole('heading', { name: '中心管理' })).toBeVisible();
  const code = 'codex-ui-e2e';
  const marker = 'ORP UI backend smoke test data; safe to keep.';
  const testRow = page.getByRole('row').filter({ hasText: code });

  if (!(await testRow.count())) {
    await page.getByRole('button', { name: '新建中心' }).click();
    const dialog = page.getByRole('dialog');
    await dialog.getByPlaceholder('例如：华东生产中心').fill('自动化验收测试中心');
    await dialog.getByPlaceholder('例如：cn-east-1').fill(code);
    await dialog.getByPlaceholder('中心的用途、归属等备注描述（可选）').fill(marker);
    await dialog.getByRole('button', { name: '确 认' }).click();
  } else {
    await expect(testRow).toContainText(marker);
  }

  await expect(page.getByRole('row').filter({ hasText: code })).toContainText(marker);
  await page.reload();
  await expect(page.getByRole('row').filter({ hasText: code })).toContainText('自动化验收测试中心');

  await test.info().attach('go-api-responses.json', {
    body: JSON.stringify(apiResponses, null, 2),
    contentType: 'application/json',
  });

  // The logged-in account must be visible when the role filter is set to all.
  await page.goto('/system/users');
  await expect(page.getByRole('main').getByText('暂无数据')).toBeHidden();
  await page.goto('/system/perms');
  await expect(page.getByRole('main').getByText('菜单节点 1 个')).toBeVisible();
  await expect(page.getByRole('main').getByText('操作点 2 个')).toBeVisible();
});
