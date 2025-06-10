<template>
  <div class="biz-management-container">
    <h1 class="page-title">业务管理</h1>
    <p class="page-description">
      这里是所有数据查询业务的配置入口。每个卡片代表一个独立的业务组。
    </p>

    <div v-if="isLoading" class="status-message loading">
      正在加载业务组列表...
    </div>

    <div v-if="error" class="status-message error">
      加载失败: {{ error }}
    </div>

    <div v-if="!isLoading && !error">
      <div v-if="bizGroups.length > 0" class="biz-cards-grid">
        <router-link
          v-for="group in bizGroups"
          :key="group.name"
          :to="{ name: 'AdminBizDetailConfig', params: { bizName: group.name } }"
          class="biz-card"
        >
          <div class="card-header">
            <span class="card-icon">🗃️</span>
            <h2 class="card-title">{{ group.name }}</h2>
            <span v-if="group.isConfigured" class="status-badge configured" title="已配置">✓</span>
            <span v-else class="status-badge unconfigured" title="待配置">!</span>
          </div>

          <div class="card-body">
            <p class="card-info">
              包含 <strong>{{ group.libs.length }}</strong> 个数据库
            </p>
          </div>
          <div class="card-footer">
            <span>{{ group.isConfigured ? '进入配置' : '开始配置' }}</span>
            <span class="arrow-icon">→</span>
          </div>
        </router-link>
      </div>

      <div v-else class="status-message info">
        当前没有发现任何可配置的业务组。
      </div>
    </div>

  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';

const bizGroups = ref([]);
const isLoading = ref(true);
const error = ref('');

const fetchBizGroups = async () => {
  isLoading.value = true;
  error.value = '';
  try {
    // 并行调用两个API
    const [summaryRes, configuredNamesRes] = await Promise.all([
      apiClient.get(ENDPOINTS.BIZ_SUMMARY),
      apiClient.get(ENDPOINTS.ADMIN_CONFIGURED_BIZ_NAMES)
    ]);

    const summaryData = summaryRes.data;
    const configuredNames = new Set(configuredNamesRes.data || []);

    // 将物理发现的业务组摘要转换为数组
    const groups = [];
    if (summaryData && typeof summaryData === 'object') {
      for (const bizName in summaryData) {
        if (Object.prototype.hasOwnProperty.call(summaryData, bizName)) {
          groups.push({
            name: bizName,
            libs: summaryData[bizName] || [],
            // 检查该业务组是否在“已配置”列表中，并添加标记
            isConfigured: configuredNames.has(bizName)
          });
        }
      }
    }

    // 按名称排序，保证显示顺序稳定
    groups.sort((a, b) => a.name.localeCompare(b.name));
    bizGroups.value = groups;

  } catch (err) {
    console.error("加载业务组失败:", err);
    error.value = err.response?.data?.error || '无法连接到服务器';
  } finally {
    isLoading.value = false;
  }
};

onMounted(() => {
  fetchBizGroups();
});
</script>

<style scoped>

.card-header {
  position: relative; /* 为了徽章定位 */
}

.status-badge {
  position: absolute;
  top: 10px;
  right: 10px;
  width: 22px;
  height: 22px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: bold;
  color: white;
}
.status-badge.configured {
  background-color: #28a745; /* 绿色 */
}
.status-badge.unconfigured {
  background-color: #ffc107; /* 黄色 */
}

.biz-management-container {
  max-width: 1200px;
}

.page-title {
  font-size: 1.8em;
  color: #2c3e50;
  margin: 0 0 0.5rem 0;
  font-weight: 600;
}

.page-description {
  font-size: 1em;
  color: #6c757d;
  margin-top: 0;
  margin-bottom: 2rem;
}

.status-message {
  padding: 1rem 1.25rem;
  margin: 1.5rem 0;
  border-radius: 8px;
  border: 1px solid;
  text-align: center;
}
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; color: #0050b3; }
.status-message.error { background-color: #fff1f0; border-color: #ffa39e; color: #cf1322; }
.status-message.info { background-color: #f8f9fa; border-color: #e9ecef; color: #6c757d; }

.biz-cards-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1.5rem;
}

.biz-card {
  background-color: #ffffff;
  border: 1px solid #e9ecef;
  border-radius: 12px;
  text-decoration: none;
  color: inherit;
  display: flex;
  flex-direction: column;
  transition: transform 0.2s ease-out, box-shadow 0.2s ease-out;
  overflow: hidden;
}

.biz-card:hover {
  transform: translateY(-5px);
  box-shadow: 0 8px 25px rgba(0, 0, 0, 0.07);
}

.card-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 1.25rem 1.25rem 0.75rem;
}

.card-icon {
  font-size: 1.5rem;
}

.card-title {
  margin: 0;
  font-size: 1.25em;
  font-weight: 600;
  color: #34495e;
}

.card-body {
  padding: 0 1.25rem;
  flex-grow: 1;
}

.card-info {
  margin: 0;
  color: #555;
  font-size: 0.95em;
}

.card-footer {
  margin-top: 1.5rem;
  padding: 0.75rem 1.25rem;
  background-color: #f8f9fa;
  color: #007bff;
  font-weight: 500;
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 0.9em;
  border-top: 1px solid #e9ecef;
  transition: background-color 0.2s;
}

.biz-card:hover .card-footer {
  background-color: #e6f2ff;
}

.arrow-icon {
  font-size: 1.2em;
  transition: transform 0.2s;
}

.biz-card:hover .arrow-icon {
  transform: translateX(4px);
}

.biz-management-container {
  max-width: 1200px;
}

.page-title {
  font-size: 1.8em;
  color: #2c3e50;
  margin: 0 0 0.5rem 0;
  font-weight: 600;
}

.page-description {
  font-size: 1em;
  color: #6c757d;
  margin-top: 0;
  margin-bottom: 2rem;
}

.status-message {
  padding: 1rem 1.25rem;
  margin: 1.5rem 0;
  border-radius: 8px;
  border: 1px solid;
  text-align: center;
}
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; color: #0050b3; }
.status-message.error { background-color: #fff1f0; border-color: #ffa39e; color: #cf1322; }
.status-message.info { background-color: #f8f9fa; border-color: #e9ecef; color: #6c757d; }

.biz-cards-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1.5rem;
}

.biz-card {
  background-color: #ffffff;
  border: 1px solid #e9ecef;
  border-radius: 12px;
  text-decoration: none;
  color: inherit;
  display: flex;
  flex-direction: column;
  transition: transform 0.2s ease-out, box-shadow 0.2s ease-out;
  overflow: hidden;
}

.biz-card:hover {
  transform: translateY(-5px);
  box-shadow: 0 8px 25px rgba(0, 0, 0, 0.07);
}

.card-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 1.25rem 1.25rem 0.75rem;
}

.card-icon {
  font-size: 1.5rem;
}

.card-title {
  margin: 0;
  font-size: 1.25em;
  font-weight: 600;
  color: #34495e;
}

.card-body {
  padding: 0 1.25rem;
  flex-grow: 1;
}

.card-info {
  margin: 0;
  color: #555;
  font-size: 0.95em;
}

.card-footer {
  margin-top: 1.5rem;
  padding: 0.75rem 1.25rem;
  background-color: #f8f9fa;
  color: #007bff;
  font-weight: 500;
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-size: 0.9em;
  border-top: 1px solid #e9ecef;
  transition: background-color 0.2s;
}

.biz-card:hover .card-footer {
  background-color: #e6f2ff;
}

.arrow-icon {
  font-size: 1.2em;
  transition: transform 0.2s;
}

.biz-card:hover .arrow-icon {
  transform: translateX(4px);
}
</style>