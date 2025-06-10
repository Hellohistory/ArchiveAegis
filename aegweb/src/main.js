// src/main.js
import { createApp } from 'vue';
import App from './App.vue';
import router from './router';
import apiClient from './services/apiClient';
import { ENDPOINTS } from './services/apiEndpoints';
import { systemStatus } from './services/systemStatus';

const app = createApp(App);

app.use(router);

/**
 * 异步初始化函数
 * 在挂载 Vue 应用之前，首先获取后端系统状态。
 * 这是确保路由守卫能够正确工作的关键。
 */
async function initializeApp() {
  try {
    const response = await apiClient.get(ENDPOINTS.AUTH_STATUS);
    // 将从 API 获取的状态 ('needs_setup' 或 'ready_for_login') 存入全局状态
    systemStatus.value = response.data.status;
  } catch (error) {
    console.error('获取系统状态失败，将默认设置为 "ready_for_login"。请检查后端服务是否正在运行。', error);
    // 如果 API 调用失败（例如后端未启动），提供一个默认值以避免应用崩溃
    systemStatus.value = 'ready_for_login';
  }

  // 等待路由完全准备好（包括执行完初始导航）之后再挂载应用
  // 这可以确保在任何页面组件渲染之前，我们的 systemStatus 已经就绪，
  // 并且初始的重定向逻辑（在 router.beforeEach 中）已经执行完毕。
  await router.isReady();

  app.mount('#app');
}

// 执行初始化
initializeApp();