<!--src/views/DashboardPage.vue-->
<template>
  <div class="dashboard-container">
    <header class="dashboard-header">
      <h1>欢迎来到仪表盘, {{ username }}!</h1>
      <div class="user-info">
        <span>您的角色是: {{ userRole }}</span>
        <button @click="logout" class="logout-button">退出登录</button>
      </div>
    </header>

    <section class="global-management-section">
      <h2>系统管理</h2>
      <div class="management-card-list">
        <router-link to="/admin/settings/rate-limit" class="management-card">
          <div class="card-icon">⚙️</div>
          <div class="card-title">全局速率限制</div>
          <div class="card-description">配置全局IP、用户和业务组的默认API请求速率。</div>
        </router-link>
      </div>
    </section>

    <section class="biz-groups-section">
      <h2>业务组管理</h2>
      <div v-if="isLoadingBizGroups" class="loading-message">正在加载业务组列表...</div>
      <div v-if="bizGroupsError" class="error-message">加载业务组失败: {{ bizGroupsError }}</div>
      <ul v-if="bizGroups.length > 0" class="biz-group-list">
        <li v-for="group in bizGroups" :key="group.name" class="biz-group-item">
          <div class="group-info">
            <span class="group-name">{{ group.name }}</span>
            <span class="libs-count">包含 {{ group.libs.length }} 个库</span>
          </div>
          <div class="group-actions">
            <router-link :to="{ name: 'AdminBizConfig', params: { bizName: group.name } }" class="configure-button">
              配置
            </router-link>
          </div>
        </li>
      </ul>
    </section>

  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue';
import { useRouter } from 'vue-router';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';

const router = useRouter();
const username = ref('');
const userRole = ref('');
const bizGroups = ref([]);
const isLoadingBizGroups = ref(false);
const bizGroupsError = ref('');

onMounted(async () => {
  username.value = localStorage.getItem('username') || '管理员';
  userRole.value = localStorage.getItem('userRole') || 'admin';
  await fetchBizGroups();
});

const fetchBizGroups = async () => {
  isLoadingBizGroups.value = true;
  bizGroupsError.value = '';
  try {
    const response = await apiClient.get(ENDPOINTS.BIZ_SUMMARY);
    const summaryData = response.data;
    const groups = [];
    if (summaryData && typeof summaryData === 'object') {
      for (const bizName in summaryData) {
        if (Object.prototype.hasOwnProperty.call(summaryData, bizName)) {
          groups.push({ name: bizName, libs: summaryData[bizName] || [] });
        }
      }
      groups.sort((a, b) => a.name.localeCompare(b.name));
      bizGroups.value = groups;
    }
  } catch (error) {
    bizGroupsError.value = error.response?.data?.error || '获取业务组列表失败';
  } finally {
    isLoadingBizGroups.value = false;
  }
};

const logout = () => {
  localStorage.removeItem('authToken');
  localStorage.removeItem('username');
  localStorage.removeItem('userRole');
  router.push('/login');
};
</script>

<style scoped>
.dashboard-container {
  padding: 20px 30px;
  max-width: 1200px;
  margin: 0 auto;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
}

.dashboard-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 30px;
  padding-bottom: 20px;
  border-bottom: 1px solid #e9ecef; /* 更柔和的边框色 */
}

.dashboard-header h1 {
  font-size: 1.8em; /* 调整大小 */
  color: #2c3e50; /* 深蓝灰色 */
  margin: 0;
  font-weight: 600;
}

.user-info {
  display: flex;
  align-items: center;
}

.user-info span {
  margin-right: 15px;
  font-size: 0.95em; /* 略微增大 */
  color: #55595c; /* 中灰色 */
}

.logout-button {
  padding: 8px 15px;
  background-color: #e74c3c; /* 更鲜明的红色 */
  color: white;
  border: none;
  border-radius: 5px; /* 圆角稍大 */
  cursor: pointer;
  font-size: 0.9em;
  transition: background-color 0.2s ease, box-shadow 0.2s ease;
  box-shadow: 0 1px 3px rgba(0,0,0,0.1);
}

.logout-button:hover {
  background-color: #c0392b; /* 悬停时颜色加深 */
  box-shadow: 0 2px 5px rgba(0,0,0,0.15);
}

.biz-groups-section h2 {
  font-size: 1.6em; /* 调整大小 */
  color: #34495e; /* 略深的蓝灰色 */
  margin-top: 30px; /* 与上方header间距 */
  margin-bottom: 20px;
  border-bottom: 1px solid #f1f3f5; /* 更浅的边框 */
  padding-bottom: 10px;
  font-weight: 500;
}

.loading-message,
.error-message,
.info-message {
  padding: 15px;
  margin-bottom: 20px;
  border-radius: 5px;
  font-size: 0.95em;
  border-width: 1px;
  border-style: solid;
}

.loading-message {
  background-color: #e6f7ff;
  border-color: #91d5ff;
  color: #0050b3;
}

.error-message {
  background-color: #fff1f0;
  border-color: #ffa39e;
  color: #cf1322;
}

.info-message {
  background-color: #e6fffb;
  border-color: #87e8de;
  color: #006d75;
}
.info-message .tip {
  margin-top: 10px;
  font-size: 0.9em;
  color: #005057;
}

.biz-group-list {
  list-style-type: none;
  padding: 0;
}

.biz-group-item {
  background-color: #ffffff;
  border: 1px solid #dee2e6; /* 标准边框色 */
  border-radius: 6px;
  padding: 18px 25px; /* 增加内边距 */
  margin-bottom: 15px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  transition: box-shadow 0.2s ease-in-out, transform 0.1s ease-in-out;
}

.biz-group-item:hover {
  box-shadow: 0 4px 12px rgba(0,0,0,0.1);
  transform: translateY(-2px); /* 轻微上浮效果 */
}

.group-info {
  display: flex;
  flex-direction: column;
  flex-grow: 1; /* 让信息区占据更多空间 */
}

.group-name {
  font-size: 1.25em; /* 增大 */
  font-weight: 600;
  color: #2c3e50;
  margin-bottom: 6px; /* 调整间距 */
}

.libs-count {
  font-size: 0.9em; /* 增大 */
  color: #6c757d; /* Bootstrap muted color */
}

.configure-button {
  padding: 9px 20px; /* 调整按钮大小 */
  background-color: #007bff;
  color: white;
  text-decoration: none;
  border-radius: 5px;
  font-size: 0.95em; /* 增大 */
  font-weight: 500;
  transition: background-color 0.2s ease, box-shadow 0.2s ease;
  white-space: nowrap; /* 防止按钮文字换行 */
}

.configure-button:hover {
  background-color: #0069d9; /* 悬停时颜色加深 */
  box-shadow: 0 1px 3px rgba(0,0,0,0.1);
}
</style>