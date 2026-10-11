/** 系统与权限管理（RBAC）API */
import type { PageResult } from './types';

import { requestClient } from '#/api/request';

/** 用户状态 */
export type RbacUserStatus = 'disabled' | 'enabled';

/** 用户条目（列表展开 roleNames） */
export interface RbacUserItem {
  createdAt: string;
  email: null | string;
  id: number;
  lastLoginAt: null | string;
  phone: null | string;
  realName: string;
  roleIds: number[];
  roleNames: string[];
  status: RbacUserStatus;
  username: string;
}

/** 用户创建/编辑载荷 */
export interface RbacUserPayload {
  email?: string;
  password?: string;
  phone?: string;
  realName: string;
  roleIds: number[];
  status: RbacUserStatus;
  username?: string;
}

/** 角色状态 */
export type RbacRoleStatus = 'disabled' | 'enabled';

/** 角色条目 */
export interface RbacRoleItem {
  builtin: boolean;
  code: string;
  centerIds: string[];
  createdAt: string;
  description: null | string;
  id: number;
  name: string;
  permissionKeys: string[];
  status: RbacRoleStatus;
  userCount?: number;
}

/** 角色创建/编辑载荷 */
export interface RbacRolePayload {
  centerIds?: string[];
  code?: string;
  description?: string;
  name: string;
  permissionKeys?: string[];
  status?: RbacRoleStatus;
}

/** 权限树节点（菜单/操作点） */
export interface RbacPermissionNode {
  children?: RbacPermissionNode[];
  description?: string;
  key: string;
  title: string;
  type: 'action' | 'menu';
}

/** 用户列表查询参数 */
export interface RbacUserParams {
  keyword?: string;
  page?: number;
  pageSize?: number;
  roleId?: number;
  status?: string;
}

/** 角色列表查询参数 */
export interface RbacRoleParams {
  keyword?: string;
  page?: number;
  pageSize?: number;
  status?: string;
}

/** 用户列表（支持关键字/状态/角色筛选） */
export function listRbacUsersApi(
  params: RbacUserParams,
): Promise<PageResult<RbacUserItem>> {
  return requestClient.get('/orp/rbac/users', { params });
}

/** 新增用户 */
export function createRbacUserApi(
  payload: RbacUserPayload,
): Promise<RbacUserItem> {
  return requestClient.post('/orp/rbac/users', payload);
}

/** 编辑用户（含角色变更） */
export function updateRbacUserApi(
  id: number,
  payload: RbacUserPayload,
): Promise<RbacUserItem> {
  return requestClient.put(`/orp/rbac/users/${id}`, payload);
}

/** 删除用户 */
export function deleteRbacUserApi(id: number): Promise<null> {
  return requestClient.delete(`/orp/rbac/users/${id}`);
}

/** 重置用户密码（恢复初始密码） */
export function resetRbacUserPasswordApi(
  id: number,
): Promise<{ resetTo: string; username: string }> {
  return requestClient.post(`/orp/rbac/users/${id}/reset-password`);
}

/** 角色列表 */
export function listRbacRolesApi(
  params: RbacRoleParams,
): Promise<PageResult<RbacRoleItem>> {
  return requestClient.get('/orp/rbac/roles', { params });
}

/** 新增角色 */
export function createRbacRoleApi(
  payload: RbacRolePayload,
): Promise<RbacRoleItem> {
  return requestClient.post('/orp/rbac/roles', payload);
}

/** 编辑角色 */
export function updateRbacRoleApi(
  id: number,
  payload: RbacRolePayload,
): Promise<RbacRoleItem> {
  return requestClient.put(`/orp/rbac/roles/${id}`, payload);
}

/** 删除角色 */
export function deleteRbacRoleApi(id: number): Promise<null> {
  return requestClient.delete(`/orp/rbac/roles/${id}`);
}

/** 权限定义树（菜单 + 操作点） */
export function listRbacPermissionsApi(): Promise<RbacPermissionNode[]> {
  return requestClient.get('/orp/rbac/permissions');
}

/** 读取角色当前权限集合 */
export function getRbacRolePermissionsApi(
  id: number,
): Promise<{ permissionKeys: string[] }> {
  return requestClient.get(`/orp/rbac/roles/${id}/permissions`);
}

/** 保存角色权限分配 */
export function assignRbacRolePermissionsApi(
  id: number,
  permissionKeys: string[],
): Promise<{ permissionKeys: string[] }> {
  return requestClient.put(`/orp/rbac/roles/${id}/permissions`, {
    permissionKeys,
  });
}
