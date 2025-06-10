<template>
  <div id="app-layout">
    <div v-if="isInitializing" class="app-loading-container">
      <div class="spinner"></div>
      <p>正在初始化应用...</p>
    </div>

    <div v-else-if="initializationError" class="app-error-container">
      <h1>应用启动失败</h1>
      <p>{{ initializationError }}</p>
      <button @click="retryInitialization">重试</button>
    </div>

    <router-view v-else />
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue';
import { useRouter } from 'vue-router';
import apiClient, { authService } from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import { systemStatus } from '@/services/systemStatus'; // 导入我们之前创建的全局状态

const isInitializing = ref(true);
const initializationError = ref('');
const router = useRouter();

/**
 * [关键修改] 重写了整个应用初始化逻辑
 * 流程：
 * 1. 永远先向后端查询权威的系统状态 (`/api/auth/status`)。
 * 2. 根据状态决定下一步操作。
 * - 如果是 'needs_setup'，则清理本地可能存在的无效Token，并强制跳转到安装页。
 * - 如果是 'ready_for_login'，再检查本地是否有Token，决定是停留在当前页还是跳转到登录页。
 */
const initializeApp = async () => {
  isInitializing.value = true;
  initializationError.value = '';

  try {
    // 第1步：无条件获取后端系统状态
    const response = await apiClient.get(ENDPOINTS.AUTH_STATUS);
    const status = response.data.status;
    systemStatus.value = status; // 更新全局状态
    console.log(`App.vue: 获取到系统状态: ${status}`);

    // 第2步：根据状态进行分支处理
    if (status === 'needs_setup') {
      // 如果系统需要安装，任何本地的Token都应视为无效，立即清除。
      authService.logout(); // logout 会清除 token 并重定向，但我们在这里接管重定向
      // 强制跳转到安装页面
      await router.replace({ name: 'SetupAdmin' });

    } else if (status === 'ready_for_login') {
      // 系统已就绪，可以进行登录逻辑
      const token = authService.getToken();
      const currentRoute = router.currentRoute.value;

      if (token) {
        // 如果有Token，我们假设它是有效的。让路由守卫和API拦截器处理后续验证。
        // 如果用户直接访问根路径，可以友好地重定向到仪表盘。
        if (currentRoute.path === '/') {
          await router.replace({ name: 'AdminDashboard' });
        }
      } else {
        // 如果没有Token，并且当前页面不是公共页面（如登录/安装），则跳转到登录页
        // 这里的逻辑可以由 router.beforeEach 中的 `needsAuth` 来处理，
        // App.vue 中可以简化为不操作，让路由守卫决定。
        // 为保险起见，如果未登录且不在登录页，可以推一把。
        if (currentRoute.name !== 'Login' && currentRoute.meta.requiresAuth) {
           await router.replace({ name: 'Login' });
        }
      }
    }

  } catch (error) {
    console.error("应用初始化失败:", error);
    initializationError.value = '无法连接到后端服务。请确保服务已启动并刷新页面。';
  } finally {
    isInitializing.value = false;
  }
};

const retryInitialization = () => {
  // 使用 window.location.reload() 来获得最干净的重试效果
  window.location.reload();
};

onMounted(() => {
  initializeApp();
});
</script>

<style>
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif;
  margin: 0;
  background-color: #f4f7f6;
  color: #333;
}

#app-layout {
  min-height: 100vh;
}

.app-loading-container,
.app-error-container {
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  height: 100vh;
  text-align: center;
}

.spinner {
  border: 4px solid rgba(0, 0, 0, 0.1);
  width: 48px;
  height: 48px;
  border-radius: 50%;
  border-left-color: #007bff;
  animation: spin 1s ease infinite;
  margin-bottom: 20px;
}

@keyframes spin {
  0% { transform: rotate(0deg); }
  100% { transform: rotate(360deg); }
}

.app-error-container h1 {
  color: #dc3545;
}

.app-error-container button {
  margin-top: 20px;
  padding: 10px 20px;
  font-size: 1em;
  cursor: pointer;
  border: 1px solid #007bff;
  background-color: transparent;
  color: #007bff;
  border-radius: 5px;
  transition: all 0.2s;
}
.app-error-container button:hover {
  background-color: #007bff;
  color: white;
}
</style>