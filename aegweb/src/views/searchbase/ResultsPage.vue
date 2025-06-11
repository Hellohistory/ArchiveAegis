<template>
  <div class="results-page-container">
    <div class="results-content">
      <router-link to="/search" class="back-link">&larr; 返回重新查询</router-link>
      <h1>查询结果</h1>

      <div v-if="isLoading" class="status-indicator">
        <p>正在加载结果...</p>
      </div>

      <div v-else-if="error" class="status-indicator error-message">
        <p>{{ error }}</p>
      </div>

      <div v-else-if="results.length === 0" class="status-indicator">
        <p>未找到符合条件的结果。</p>
      </div>

      <div v-else>
        <div v-if="viewConfig.view_type === 'table'">
          <table>
            <thead>
              <tr>
                <th v-for="col in viewConfig.binding.table.columns" :key="col.field">{{ col.displayName }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(item, index) in results" :key="index">
                <td v-for="col in viewConfig.binding.table.columns" :key="col.field">{{ item[col.field] }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="viewConfig.view_type === 'cards'" class="cards-container">
          <div v-for="(item, index) in results" :key="index" class="card">
            <h3 class="card-title">{{ item[viewConfig.binding.card.title] }}</h3>
            <p class="card-subtitle">{{ item[viewConfig.binding.card.subtitle] }}</p>
            <p class="card-description">{{ item[viewConfig.binding.card.description] }}</p>
          </div>
        </div>

        <div class="pagination">
          <button @click="changePage(-1)" :disabled="currentPage === 1">上一页</button>
          <span>第 {{ currentPage }} 页</span>
          <button @click="changePage(1)" :disabled="isLastPage">下一页</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';

const route = useRoute();
const router = useRouter();

const results = ref([]);
const viewConfig = ref(null);
const isLoading = ref(true);
const error = ref('');
const currentPage = ref(1);
const isLastPage = ref(false);

const fetchData = async () => {
  isLoading.value = true;
  error.value = '';
  results.value = [];

  const queryParams = { ...route.query, page: currentPage.value, size: 50 };

  if (!queryParams.biz) {
    error.value = "无效的查询请求：缺少业务组信息。";
    isLoading.value = false;
    return;
  }

  try {
    const [searchResponse, viewConfigResponse] = await Promise.all([
      apiClient.get(ENDPOINTS.SEARCH, { params: queryParams }),
      apiClient.get(ENDPOINTS.VIEW_CONFIG, { params: { biz: queryParams.biz, table: queryParams.table } })
    ]);

    results.value = searchResponse.data;
    viewConfig.value = viewConfigResponse.data;

    isLastPage.value = searchResponse.data.length < queryParams.size;

  } catch (err) {
    error.value = err.response?.data?.error || "获取查询结果失败。";
    console.error(err);
  } finally {
    isLoading.value = false;
  }
};

const changePage = (increment) => {
  const newPage = currentPage.value + increment;
  currentPage.value = newPage;

  router.push({ query: { ...route.query, page: newPage }});
};

watch(
  () => route.query.page,
  (newPage) => {
    currentPage.value = newPage ? parseInt(newPage, 10) : 1;
    fetchData();
  },
  { immediate: true }
);
</script>

<style scoped>
.results-page-container { padding: 2rem; background-color: #f4f7f6; min-height: 100vh; }
.results-content { max-width: 1200px; margin: 0 auto; background: white; padding: 2rem; border-radius: 8px; }
.back-link { text-decoration: none; color: #007bff; margin-bottom: 1rem; display: inline-block; }
.status-indicator { text-align: center; padding: 2rem; color: #666; }
.error-message { background-color: #fef2f2; color: #ef4444; border-radius: 4px; }
table { width: 100%; border-collapse: collapse; margin-top: 1rem; }
th, td { border: 1px solid #ddd; padding: 0.8rem; text-align: left; }
th { background-color: #f8f9fa; }
.pagination { display: flex; justify-content: center; align-items: center; gap: 1rem; margin-top: 2rem; }
.pagination button { padding: 0.5rem 1rem; cursor: pointer; }
.pagination button:disabled { cursor: not-allowed; opacity: 0.5; }

.cards-container { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 1rem; }
.card { border: 1px solid #ddd; padding: 1rem; border-radius: 4px; }
</style>