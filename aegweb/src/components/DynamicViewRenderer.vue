<!--src/components/DynamicViewRenderer.vue-->
<template>
  <div class="renderer-wrapper">
    <div v-if="isLoading" class="status-info">正在加载视图配置...</div>
    <div v-else-if="error" class="status-info error">{{ error }}</div>

    <div v-else-if="viewConfig && props.searchResults.length > 0">
      <component
        :is="viewComponent"
        :results="props.searchResults"
        :config="viewBindingConfig"
      />
    </div>
  </div>
</template>

<script setup>
import { ref, watch, computed } from 'vue';
import apiClient from '../services/apiClient';
import { ENDPOINTS } from '../services/apiEndpoints';
import CardView from './CardView.vue';
import TableView from './TableView.vue';

// 映射视图类型到组件
const viewComponentMap = {
  cards: CardView,
  table: TableView,
};

const props = defineProps({
  biz: String,
  table: String,
  searchResults: Array,
});

const viewConfig = ref(null);
const isLoading = ref(false);
const error = ref('');
const viewComponent = computed(() => {
  if (!viewConfig.value) return null;
  return viewComponentMap[viewConfig.value.view_type] || null;
});

const viewBindingConfig = computed(() => {
    if (!viewConfig.value) return {};
    return viewConfig.value.binding ? (viewConfig.value.binding[viewConfig.value.view_type] || {}) : {};
});

watch(() => props.table, async (newTable) => {
  viewConfig.value = null;
  if (newTable && props.biz) {
    isLoading.value = true;
    error.value = '';
    try {
      const response = await apiClient.get(ENDPOINTS.VIEW_CONFIG, {
        params: { biz: props.biz, table: newTable }
      });
      viewConfig.value = response.data;
    } catch (e) {
      if (e.response && e.response.status === 404) {
        error.value = "此表没有定义默认视图，请联系管理员。";
      } else {
        error.value = "加载视图配置失败，请重试。";
      }
      console.error("加载视图配置失败:", e);
    } finally {
      isLoading.value = false;
    }
  }
}, { immediate: true });
</script>

<style scoped>
.status-info {
  text-align: center;
  padding: 2rem;
  color: #666;
}
.error {
  color: #d9534f;
  background-color: #f2dede;
  border: 1px solid #ebccd1;
  border-radius: 8px;
}
</style>
<style scoped>
.status-info {
  text-align: center;
  padding: 2rem;
  color: #666;
}
.error {
  color: #d9534f;
  background-color: #f2dede;
  border: 1px solid #ebccd1;
  border-radius: 8px;
}
</style>