<!--src/views/SetupAdminPage.vue-->
<template>
  <div class="setup-admin-container">
    <div class="setup-admin-card">
      <h2>初始管理员账户设置</h2>

      <div v-if="isPollingToken && !formReady && !setupNotPossibleMessage" class="loading-section">
        <p>正在获取安装配置，请稍候 (尝试: {{ pollingAttempts }}/{{ MAX_POLLING_ATTEMPTS }})...</p>
      </div>

      <div v-if="setupNotPossibleMessage" class="error-message static-error">
        <p>{{ setupNotPossibleMessage }}</p>
        <router-link v-if="redirectToLogin" to="/login" class="button-link">前往登录</router-link>
      </div>

      <form v-if="formReady && !setupNotPossibleMessage" @submit.prevent="handleSetupSubmit">
        <p class="instructions">
          欢迎使用 ArchiveAegis！请设置您的第一个管理员账户。
        </p>

        <div class="form-group">
          <label for="username">管理员用户名:</label>
          <input type="text" id="username" v-model="username" required :disabled="isLoading" />
        </div>

        <div class="form-group">
          <label for="password">密码:</label>
          <input type="password" id="password" v-model="password" required :disabled="isLoading" />
        </div>

        <div class="form-group">
          <label for="confirmPassword">确认密码:</label>
          <input type="password" id="confirmPassword" v-model="confirmPassword" required :disabled="isLoading" />
        </div>

        <div v-if="errorMessage" class="error-message">
          {{ errorMessage }}
        </div>
        <div v-if="successMessage" class="success-message">
          {{ successMessage }}
        </div>

        <button type="submit" :disabled="isLoading || !formReady" class="submit-button">
          {{ isLoading ? '正在创建...' : '创建管理员账户' }}
        </button>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue';
import { useRouter } from 'vue-router';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';


const POLLING_INTERVAL = 3000;
const MAX_POLLING_ATTEMPTS = 10;


const router = useRouter();

const setupToken = ref('');
const username = ref('');
const password = ref('');
const confirmPassword = ref('');
const errorMessage = ref('');
const successMessage = ref('');
const setupNotPossibleMessage = ref('');
const redirectToLogin = ref(false);
const isLoading = ref(false);
const isPollingToken = ref(true);
const formReady = ref(false);
let pollingIntervalId = null;
const pollingAttempts = ref(0);

const pollForSetupToken = async () => {
  isPollingToken.value = true;
  pollingAttempts.value = 0;

  const attemptFetch = async () => {
    if (pollingAttempts.value >= MAX_POLLING_ATTEMPTS) {
      setupNotPossibleMessage.value = '获取安装配置超时。请刷新页面或检查后端服务。';
      isPollingToken.value = false;
      if (pollingIntervalId) clearInterval(pollingIntervalId);
      return;
    }

    pollingAttempts.value++;
    try {
      const response = await apiClient.get(ENDPOINTS.SETUP_PING);
      if (response.data && response.data.token) {
        setupToken.value = response.data.token;
        formReady.value = true;
        isPollingToken.value = false;
        if (pollingIntervalId) clearInterval(pollingIntervalId);
        console.log('安装令牌获取成功:', setupToken.value);
      } else {
        console.log(`轮询尝试 ${pollingAttempts.value}: 等待安装令牌...`);
      }
    } catch (error) {
      // [优化] 在轮询时提前处理 "已设置" 的情况
      if (error.response && error.response.status === 403) {
        setupNotPossibleMessage.value = '系统已完成初始设置，无需重复操作。';
        redirectToLogin.value = true;
        isPollingToken.value = false;
        if (pollingIntervalId) clearInterval(pollingIntervalId);
      } else {
        console.error(`轮询尝试 ${pollingAttempts.value} 失败:`, error);
      }
    }
  };

  await attemptFetch();
  if (isPollingToken.value && !formReady.value) {
    pollingIntervalId = setInterval(attemptFetch, POLLING_INTERVAL);
  }
};

const validateForm = () => {
  errorMessage.value = '';
  if (!username.value.trim() || !password.value || !confirmPassword.value) {
    errorMessage.value = '所有字段均为必填项。';
    return false;
  }
  if (password.value.length < 6) {
    errorMessage.value = '密码长度至少为6位。';
    return false;
  }
  if (password.value !== confirmPassword.value) {
    errorMessage.value = '两次输入的密码不一致。';
    return false;
  }
  return true;
};

const handleSetupSubmit = async () => {
  if (!validateForm()) return;

  isLoading.value = true;
  successMessage.value = '';
  errorMessage.value = '';

  const params = new URLSearchParams();
  params.append('token', setupToken.value);
  params.append('user', username.value.trim());
  params.append('pass', password.value);

  try {
    const response = await apiClient.post(ENDPOINTS.SETUP, params);

    const responseData = response.data;
    if (!responseData || !responseData.token) {
      throw new Error("响应中未找到认证令牌。");
    }

    successMessage.value = `管理员账户 '${responseData.user.username}' 创建成功！2秒后将自动跳转...`;
    localStorage.setItem('authToken', responseData.token);
    localStorage.setItem('username', responseData.user.username);
    localStorage.setItem('userRole', responseData.user.role);

    setTimeout(() => {
      router.push('/dashboard');
    }, 2000);

  } catch (error) {
    if (error.response && error.response.status === 403) {
      setupNotPossibleMessage.value = '系统初始化已完成，无法重复设置。';
      redirectToLogin.value = true;
    } else if (error.response && error.response.data && error.response.data.error) {
      errorMessage.value = `创建失败: ${error.response.data.error}`;
    } else {
      errorMessage.value = '创建失败: 网络连接问题或服务器无响应。';
    }
    console.error('设置管理员账户过程中发生错误:', error);
  } finally {
    isLoading.value = false;
  }
};

onMounted(() => {
  if (localStorage.getItem('authToken')) {
    setupNotPossibleMessage.value = '您已登录，无需进行初始设置。';
    redirectToLogin.value = true;
    return;
  }
  pollForSetupToken();
});

onUnmounted(() => {
  if (pollingIntervalId) {
    clearInterval(pollingIntervalId);
  }
});
</script>

<style scoped>
.setup-admin-container {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 80vh;
  background-color: #f4f7f6;
  padding: 20px;
}

.setup-admin-card {
  background-color: #fff;
  padding: 30px 40px;
  border-radius: 8px;
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.1);
  width: 100%;
  max-width: 450px;
  text-align: center;
}

h2 {
  color: #333;
  margin-bottom: 25px;
  font-weight: 600;
}

.instructions {
  font-size: 0.95em;
  color: #555;
  margin-bottom: 20px;
  line-height: 1.5;
}

.form-group {
  margin-bottom: 20px;
  text-align: left;
}

.form-group label {
  display: block;
  margin-bottom: 8px;
  color: #555;
  font-weight: 500;
  font-size: 0.9em;
}

.form-group input {
  width: 100%;
  padding: 12px;
  border: 1px solid #ddd;
  border-radius: 4px;
  box-sizing: border-box;
  font-size: 1em;
}

.form-group input:focus {
  border-color: #007bff;
  box-shadow: 0 0 0 0.2rem rgba(0, 123, 255, 0.25);
  outline: none;
}
.form-group input:disabled {
  background-color: #e9ecef;
  opacity: 0.7;
}


.submit-button {
  width: 100%;
  padding: 12px;
  background-color: #007bff;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1em;
  font-weight: 500;
  transition: background-color 0.2s;
}

.submit-button:disabled {
  background-color: #a0cfff;
  cursor: not-allowed;
}

.submit-button:not(:disabled):hover {
  background-color: #0056b3;
}

.error-message,
.success-message,
.loading-section p,
.static-error p {
  margin-top: 15px;
  margin-bottom: 15px;
  padding: 10px;
  border-radius: 4px;
  font-size: 0.9em;
  word-wrap: break-word;
}

.error-message, .static-error p {
  color: #721c24;
  background-color: #f8d7da;
  border: 1px solid #f5c6cb;
}
.static-error p {
  margin-bottom: 10px;
}

.success-message {
  color: #155724;
  background-color: #d4edda;
  border: 1px solid #c3e6cb;
}

.loading-section p {
  color: #0c5460;
  background-color: #d1ecf1;
  border: 1px solid #bee5eb;
}

.button-link {
  display: inline-block;
  margin-top: 10px;
  padding: 8px 15px;
  background-color: #28a745;
  color: white;
  text-decoration: none;
  border-radius: 4px;
  font-size: 0.9em;
  transition: background-color 0.2s;
}
.button-link:hover {
  background-color: #218838;
}
</style>