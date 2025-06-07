// src/services/apiClient.js
import axios from 'axios';
import { API_BASE_URL } from './apiEndpoints';   // 基础 URL

/* ------------------ 工具函数：统一管理 token ------------------ */

/**
 * 从 sessionStorage 或 localStorage 读取 token
 * 优先使用 sessionStorage，关闭标签页即失效
 */
function getToken() {
  return (
    sessionStorage.getItem('authToken') ||
    localStorage.getItem('authToken') ||
    ''
  );
}

/**
 * 将 token 同步写入 sessionStorage（优先）和 localStorage（可选）
 * 你可以根据项目需求，仅保存在 sessionStorage
 */
function setToken(token) {
  sessionStorage.setItem('authToken', token);
  localStorage.setItem('authToken', token);
}

/**
 * 清除本地存储中的 token 与用户名等信息
 */
function clearAuthStorage() {
  sessionStorage.removeItem('authToken');
  sessionStorage.removeItem('username');
  localStorage.removeItem('authToken');
  localStorage.removeItem('username');
}

/* ------------------ 创建 axios 实例 ------------------ */

const apiClient = axios.create({
  baseURL: API_BASE_URL,
  timeout: 10_000, // 10 秒
  headers: {
    'Content-Type': 'application/json',
  },
});

/* ------------------ 请求拦截器 ------------------ */
apiClient.interceptors.request.use(
  (config) => {
    const token = getToken();

    // 对 /setup 与 /login 接口不注入 Authorization
    const isPublic =
      config.url.includes('/setup') || config.url.includes('/login');

    if (token && !isPublic) {
      config.headers.Authorization = `Bearer ${token}`;
    }

    // /setup 与 /login 的 POST 请求改用 x-www-form-urlencoded
    if (
      isPublic &&
      config.method === 'post'
    ) {
      config.headers['Content-Type'] =
        'application/x-www-form-urlencoded';
    }

    return config;
  },
  (error) => Promise.reject(error),
);

/* ------------------ 响应拦截器 ------------------ */
apiClient.interceptors.response.use(
  (response) => response, // 成功直接返回
  (error) => {
    // 统一处理 401 Unauthorized 或 403 Forbidden
    const { response } = error;
    if (response && (response.status === 401 || response.status === 403)) {
      // 清除本地失效 token
      clearAuthStorage();

      // 跳转到登录页（避免在登录页循环跳转）
      if (!window.location.pathname.startsWith('/login')) {
        window.location.href = '/login';
      }
    }

    return Promise.reject(error); // 继续向上抛
  },
);

export default apiClient;
export { getToken, setToken, clearAuthStorage };
