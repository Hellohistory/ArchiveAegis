<!--src/App.vue-->
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

const isInitializing = ref(true);
const initializationError = ref('');
const router = useRouter();

const initializeApp = async () => {
  isInitializing.value = true;
  initializationError.value = '';

  try {
    const token = authService.getToken();

    if (token) {
      console.log('App.vue: 检测到本地存在Token，检查是否需要跳转。');
      if (router.currentRoute.value.path === '/') {
        await router.replace({ name: 'AdminDashboard' });
      }
      isInitializing.value = false;
      return;
    }

    console.log('App.vue: 本地无Token，开始向后端查询系统状态...');
    const response = await apiClient.get(ENDPOINTS.AUTH_STATUS);
    const status = response.data.status;
    console.log(`App.vue: 获取到系统状态: ${status}`);

    if (status === 'needs_setup') {
      await router.replace('/setup-admin');
    } else if (status === 'ready_for_login') {
      await router.replace('/login');
    }

  } catch (error) {
    console.error("应用初始化失败:", error);
    initializationError.value = '无法连接到后端服务，请确保服务已启动并刷新页面。';
  } finally {
    isInitializing.value = false;
  }
};

const retryInitialization = () => {
  initializeApp();
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