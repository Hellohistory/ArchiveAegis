// src/services/apiEndpoints.js

export const ENDPOINTS = {
  // 认证
  AUTH_STATUS: '/auth/status',
  SETUP_PING: '/setup?ping=1',
  SETUP: '/setup',
  LOGIN: '/login',

  // 业务信息接口
  BIZ_SUMMARY: '/biz',
  TABLES: '/tables',
  COLUMNS: '/columns',
  SEARCH: '/search',
  VIEW_CONFIG: '/view/config',

  // ===============================================
  // 管理员 API - 业务配置
  // ===============================================
  GET_BIZ_CONFIG: (bizName) => `/admin/config/biz/${encodeURIComponent(bizName)}`,
  UPDATE_BIZ_SETTINGS: (bizName) => `/admin/config/biz/${encodeURIComponent(bizName)}/settings`,
  UPDATE_BIZ_TABLES: (bizName) => `/admin/config/biz/${encodeURIComponent(bizName)}/tables`,
  GET_PHYSICAL_COLUMNS: (bizName, tableName) => `/admin/config/biz/${encodeURIComponent(bizName)}/tables/${encodeURIComponent(tableName)}/physical-columns`,
  ADMIN_CONFIGURED_BIZ_NAMES: '/admin/configured-biz-names',
  UPDATE_TABLE_FIELDS: (bizName, tableName) => `/admin/config/biz/${encodeURIComponent(bizName)}/tables/${encodeURIComponent(tableName)}/fields`,
  GET_BIZ_VIEWS: (bizName) => `/admin/config/biz/${encodeURIComponent(bizName)}/views`,
  UPDATE_BIZ_VIEWS: (bizName) => `/admin/config/biz/${encodeURIComponent(bizName)}/views`,

  // ===============================================
  // 管理员 API - 速率限制配置
  // ===============================================
  IP_LIMIT: '/admin/settings/ip_limit',
  BIZ_RATELIMIT: (bizName) => `/admin/config/biz/${encodeURIComponent(bizName)}/ratelimit`,
};