// src/services/apiEndpoints.js

export const API_BASE_URL = '';

export const ENDPOINTS = {
  // 认证
  AUTH_STATUS: '/api/auth/status',
  SETUP_PING: '/api/setup?ping=1',
  SETUP: '/api/setup',
  LOGIN: '/api/login',

  // 业务信息接口
  BIZ_SUMMARY: '/api/biz',
  TABLES: '/api/tables',
  COLUMNS: '/api/columns',
  SEARCH: '/api/search',
  VIEW_CONFIG: '/api/view/config',

  // 管理员 API
  // 【优化】为所有接收参数的函数添加 encodeURIComponent，增强代码健壮性
  GET_BIZ_CONFIG: (bizName) => `/api/admin/config/biz/${encodeURIComponent(bizName)}`,

  UPDATE_BIZ_SETTINGS: (bizName) => `/api/admin/config/biz/${encodeURIComponent(bizName)}/settings`,

  UPDATE_BIZ_TABLES: (bizName) => `/api/admin/config/biz/${encodeURIComponent(bizName)}/tables`,

  GET_PHYSICAL_COLUMNS: (bizName, tableName) => `/api/admin/config/biz/${encodeURIComponent(bizName)}/tables/${encodeURIComponent(tableName)}/physical-columns`,

  UPDATE_TABLE_FIELDS: (bizName, tableName) => `/api/admin/config/biz/${encodeURIComponent(bizName)}/tables/${encodeURIComponent(tableName)}/fields`,

  /**
   * 获取一个业务组下所有表的视图配置
   */
  GET_BIZ_VIEWS: (bizName) => `/api/admin/config/biz/${encodeURIComponent(bizName)}/views`,

  /**
   * 更新一个业务组下所有表的视图配置
   */
  UPDATE_BIZ_VIEWS: (bizName) => `/api/admin/config/biz/${encodeURIComponent(bizName)}/views`,
};