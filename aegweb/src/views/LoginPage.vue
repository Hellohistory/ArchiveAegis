<!--src/views/LoginPage.vue-->
<template>
  <div class="login-page-wrapper">
    <div class="login-container">
      <header class="login-header">
        <h1>欢迎登录</h1>
        <p class="subtitle">ArchiveAegis 管理后台</p>
      </header>

      <form @submit.prevent="handleLogin" class="login-form">
        <div class="form-group">
          <label for="username">用户名</label>
          <input
            id="username"
            type="text"
            v-model="username"
            placeholder="请输入管理员用户名"
            required
            :disabled="isLoading"
            autocomplete="username"
          />
        </div>

        <div class="form-group">
          <label for="password">密码</label>
          <input
            id="password"
            type="password"
            v-model="password"
            placeholder="请输入密码"
            required
            :disabled="isLoading"
            autocomplete="current-password"
          />
        </div>

        <div v-if="errorMessage" class="error-message">
          {{ errorMessage }}
        </div>

        <button type="submit" :disabled="isLoading" class="login-button">
          {{ isLoading ? '正在登录...' : '登 录' }}
        </button>
      </form>

      <footer class="login-footer">
        <router-link to="/setup-admin">还没有账户？前往初始设置</router-link>
        <p class="dev-note">(此链接主要用于首次安装)</p>
      </footer>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue';
import { useRouter } from 'vue-router';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';


const username = ref('');
const password = ref('');
const isLoading = ref(false);
const errorMessage = ref('');

const router = useRouter();

// 处理登录逻辑
const handleLogin = async () => {
  isLoading.value = true;
  errorMessage.value = '';

  if (!username.value || !password.value) {
    errorMessage.value = '用户名和密码不能为空。';
    isLoading.value = false;
    return;
  }

  try {
    const formData = new URLSearchParams();
    formData.append('user', username.value);
    formData.append('pass', password.value);

    const response = await apiClient.post(ENDPOINTS.LOGIN, formData);

    if (response.data && response.data.token) {
      localStorage.setItem('authToken', response.data.token);
      localStorage.setItem('username', response.data.user.username);
      localStorage.setItem('userRole', response.data.user.role);

      await router.push('/admin/dashboard');
    } else {
      errorMessage.value = '登录响应异常，请联系管理员。';
    }

  } catch (error) {

    if (error.response) {
      errorMessage.value = `登录失败: ${error.response.data.error || '服务器返回未知错误'}`;
    } else if (error.request) {
      errorMessage.value = '无法连接到服务器，请检查您的网络连接。';
    } else {
      errorMessage.value = `请求失败: ${error.message}`;
    }
    console.error('Login failed:', error);
  } finally {
    isLoading.value = false;
  }
};
</script>

<style scoped>
.login-page-wrapper {
  display: flex;
  justify-content: center;
  align-items: center;
  min-height: 100vh;
  background-color: #f0f2f5;
  padding: 20px;
}

.login-container {
  width: 100%;
  max-width: 400px;
  padding: 40px;
  background-color: #fff;
  border-radius: 8px;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.1);
  text-align: center;
}

.login-header h1 {
  font-size: 2em;
  color: #333;
  margin-bottom: 10px;
}

.subtitle {
  color: #666;
  margin-bottom: 30px;
}

.login-form {
  display: flex;
  flex-direction: column;
}

.form-group {
  margin-bottom: 20px;
  text-align: left;
}

.form-group label {
  display: block;
  margin-bottom: 8px;
  font-weight: 500;
  color: #495057;
}

.form-group input {
  width: 100%;
  padding: 12px 15px;
  border: 1px solid #ced4da;
  border-radius: 4px;
  font-size: 1em;
  box-sizing: border-box;
  transition: border-color 0.2s, box-shadow 0.2s;
}

.form-group input:focus {
  border-color: #80bdff;
  outline: 0;
  box-shadow: 0 0 0 0.2rem rgba(0, 123, 255, 0.25);
}

.login-button {
  padding: 12px;
  width: 100%;
  background-color: #007bff;
  color: white;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 1.1em;
  font-weight: 500;
  transition: background-color 0.2s;
  margin-top: 10px;
}

.login-button:hover:not(:disabled) {
  background-color: #0056b3;
}

.login-button:disabled {
  background-color: #8dbdff;
  cursor: not-allowed;
}

.error-message {
  color: #dc3545;
  background-color: #f8d7da;
  border: 1px solid #f5c6cb;
  padding: 10px;
  border-radius: 4px;
  margin-bottom: 20px;
  text-align: center;
  font-size: 0.9em;
}

.login-footer {
  margin-top: 30px;
  font-size: 0.9em;
}

.login-footer a {
  color: #007bff;
  text-decoration: none;
}

.login-footer a:hover {
  text-decoration: underline;
}

.dev-note {
  color: #888;
  font-size: 0.8em;
  margin-top: 5px;
}
</style>