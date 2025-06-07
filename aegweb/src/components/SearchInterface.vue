<!--src/components/SearchInterface.vue-->
<template>
  <div class="search-container">
    <header class="search-header">
      <h1>数据检索中心</h1>
    </header>

    <BizTableSelectors v-model:biz="selectedBiz" v-model:table="selectedTable" />

    <DynamicSearchForm
      v-if="searchColumns.length > 0"
      :columns="searchColumns"
      :key="`${selectedBiz}-${selectedTable}`"
      @search="handleSearch"
      :is-loading="isLoading"
    />

    <div v-if="isLoading" class="loading-indicator">正在查询...</div>
    <div v-if="error" class="error-message">{{ error }}</div>

    <div v-if="!isLoading && selectedTable">
      <DynamicViewRenderer
        :biz="selectedBiz"
        :table="selectedTable"
        :search-results="searchResults"
      />

      <div v-if="searchResults.length > 0 || currentPage > 1" class="pagination">
        <button @click="handlePageChange(currentPage - 1)" :disabled="currentPage <= 1">上一页</button>
        <span>第 {{ currentPage }} 页</span>
        <button @click="handlePageChange(currentPage + 1)" :disabled="searchResults.length < 20">下一页</button>
      </div>

      <div v-if="searchResults.length === 0 && !isInitialLoad" class="no-results">
        <p>没有找到匹配的结果，请尝试修改查询条件。</p>
      </div>
    </div>

  </div>
</template>

<script setup>
import { ref, watch } from 'vue';
import apiClient from '../services/apiClient';
import { ENDPOINTS } from '../services/apiEndpoints';

import BizTableSelectors from './BizTableSelectors.vue';
import DynamicSearchForm from './DynamicSearchForm.vue';
import DynamicViewRenderer from './DynamicViewRenderer.vue';

// 状态
const selectedBiz = ref('');
const selectedTable = ref('');
const searchColumns = ref([]);
const searchResults = ref([]);
const currentSearchParams = ref(null);
const currentPage = ref(1);
const isLoading = ref(false);
const error = ref('');
const isInitialLoad = ref(true); // 用于区分初始状态和搜索无结果状态

// 监听数据表变化，获取列配置
watch(selectedTable, async (newTable) => {
  // 重置状态
  searchResults.value = [];
  searchColumns.value = [];
  currentPage.value = 1;
  currentSearchParams.value = null;
  isInitialLoad.value = true;

  if (newTable && selectedBiz.value) {
    isLoading.value = true;
    error.value = '';
    try {
      const response = await apiClient.get(
        ENDPOINTS.COLUMNS,
        { params: { biz: selectedBiz.value, table: newTable } }
      );
      searchColumns.value = response.data;
    } catch (e) {
      error.value = '加载表单字段失败，请检查配置或网络。';
    } finally {
      isLoading.value = false;
    }
  }
});

// 处理搜索事件
const handleSearch = (searchParams) => {
  isInitialLoad.value = false;
  currentPage.value = 1;
  currentSearchParams.value = searchParams;
  executeQuery();
};

// 处理分页变化事件
const handlePageChange = (newPage) => {
  if (newPage < 1) return;
  currentPage.value = newPage;
  executeQuery();
};

// 执行查询
async function executeQuery() {
  if (!currentSearchParams.value) return;

  isLoading.value = true;
  error.value = '';

  const params = {
    biz: selectedBiz.value,
    table: selectedTable.value,
    page: currentPage.value,
    size: 20,
    ...currentSearchParams.value
  };

  try {
    const response = await apiClient.get(ENDPOINTS.SEARCH, { params });
    searchResults.value = response.data;
  } catch (e) {
    error.value = e.response?.data?.error || '查询失败，发生未知错误。';
    searchResults.value = [];
  } finally {
    isLoading.value = false;
  }
}
</script>

<style scoped>
.search-container {
  font-family: sans-serif;
  max-width: 1200px;
  margin: 0 auto;
  padding: 1rem;
}
.search-header {
  text-align: center;
  margin-bottom: 2rem;
}
.loading-indicator, .error-message, .no-results {
  text-align: center;
  padding: 2rem;
  font-size: 1.2rem;
  color: #666;
}
.error-message {
  color: red;
}
.no-results {
  border: 1px dashed #ccc;
  border-radius: 8px;
  margin-top: 1.5rem;
}
.pagination {
    margin-top: 1.5rem;
    text-align: center;
    display: flex;
    justify-content: center;
    align-items: center;
    gap: 1rem;
}
</style>