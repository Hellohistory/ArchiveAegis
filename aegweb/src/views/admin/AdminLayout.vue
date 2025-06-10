<template>
  <div class="admin-layout">
    <header class="admin-header">
      <div class="header-left">
        <span class="app-title">ArchiveAegis 管理后台</span>
      </div>
      <div class="header-right">
        <span class="user-info">欢迎管理员, {{ username }} </span>
        <button @click="handleLogout" class="logout-button">登出</button>
      </div>
    </header>

    <div class="admin-main-container">
      <aside class="admin-sidebar">
        <nav>
          <ul>
            <li>
              <router-link :to="{ name: 'AdminDashboard' }">仪表盘</router-link>
            </li>
            <li>
              <router-link :to="{ name: 'AdminBizManagement' }">业务管理</router-link>
            </li>
            <li>
              <router-link :to="{ name: 'AdminRateControl' }">速率控制</router-link>
            </li>
          </ul>
        </nav>
      </aside>

      <main class="admin-content">
        <router-view />
      </main>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue';
import { authService } from '@/services/apiClient';

// script 部分无需改动
const username = ref(authService.getUsername() || 'Admin');

const handleLogout = () => {
  authService.logout();
};
</script>

<style scoped>
/* 样式保持不变，这里省略以保持简洁 */
.admin-layout {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background-color: #f0f2f5;
}

.admin-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0 24px;
  height: 64px;
  background-color: #ffffff;
  box-shadow: 0 2px 8px #f0f1f2;
  flex-shrink: 0;
  z-index: 10;
}

.app-title {
  font-size: 1.4em;
  font-weight: 600;
  color: #333;
}

.header-right {
  display: flex;
  align-items: center;
}

.user-info {
  margin-right: 20px;
  color: #555;
}

.logout-button {
  padding: 6px 12px;
  border: 1px solid #d9d9d9;
  background-color: transparent;
  color: #333;
  border-radius: 4px;
  cursor: pointer;
  transition: all 0.2s;
}

.logout-button:hover {
  color: #ff4d4f;
  border-color: #ff4d4f;
}

.admin-main-container {
  display: flex;
  flex-grow: 1;
  overflow: hidden; /* 防止内容溢出 */
}

.admin-sidebar {
  width: 200px;
  background-color: #ffffff;
  padding-top: 20px;
  flex-shrink: 0;
  box-shadow: 2px 0 8px #f0f1f2;
}

.admin-sidebar nav ul {
  list-style: none;
  padding: 0;
  margin: 0;
}

.admin-sidebar nav a {
  display: block;
  padding: 12px 24px;
  color: #333;
  text-decoration: none;
  font-size: 1em;
  transition: background-color 0.2s;
}

.admin-sidebar nav a:hover {
  background-color: #e6f7ff;
}

/* Vue Router 会自动为激活的链接添加这个 class */
.admin-sidebar nav a.router-link-active {
  background-color: #1890ff;
  color: #ffffff;
  font-weight: 500;
}

.admin-content {
  flex-grow: 1;
  padding: 24px;
  overflow-y: auto; /* 如果内容超长，允许滚动 */
}
</style>