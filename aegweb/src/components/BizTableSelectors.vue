<template>
  <div class="selectors-wrapper">
    <div class="selector-item">
      <label for="biz-select">业务组:</label>
      <div class="select-container">
        <select id="biz-select" :value="biz" @change="onBizChange" :disabled="isLoadingBiz">
          <option disabled value="">{{ isLoadingBiz ? '加载中...' : '请选择业务组' }}</option>
          <option v-for="b in bizOptions" :key="b.name" :value="b.name">
            {{ b.display_name || b.name }}
          </option>
        </select>
      </div>
    </div>

    <div class="selector-item">
      <label for="table-select">数据表:</label>
      <div class="select-container">
        <select id="table-select" :value="table" @change="$emit('update:table', $event.target.value)" :disabled="!biz || isLoadingTables">
          <option disabled value="">{{ !biz ? '请先选择业务组' : isLoadingTables ? '加载中...' : '请选择数据表' }}</option>
          <option v-for="t in tableOptions" :key="t" :value="t">{{ t }}</option>
        </select>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, watch } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';

const props = defineProps({
  biz: String,
  table: String
});
const emit = defineEmits(['update:biz', 'update:table']);

const bizOptions = ref([]);
const tableOptions = ref([]);
const isLoadingBiz = ref(false);
const isLoadingTables = ref(false);

onMounted(async () => {
  isLoadingBiz.value = true;
  try {
    const response = await apiClient.get(ENDPOINTS.BIZ_SUMMARY);
    const rawData = response.data;

    bizOptions.value = Object.keys(rawData).map(key => ({
      name: key,
      display_name: key
    }));

  } catch (error) {
    console.error("加载业务组失败:", error);
    bizOptions.value = [];
  } finally {
    isLoadingBiz.value = false;
  }
});

watch(() => props.biz, async (newBiz) => {
  emit('update:table', '');
  tableOptions.value = [];

  if (newBiz) {
    isLoadingTables.value = true;
    try {
      const response = await apiClient.get(ENDPOINTS.TABLES, { params: { biz: newBiz } });
      tableOptions.value = response.data || [];
    } catch (error) {
      console.error("加载数据表失败:", error);
      tableOptions.value = [];
    } finally {
      isLoadingTables.value = false;
    }
  }
});

const onBizChange = (event) => {
  emit('update:biz', event.target.value);
};
</script>

<style scoped>
.selectors-wrapper {
  display: flex;
  flex-wrap: wrap; /* 在小屏幕上允许换行 */
  gap: 1.5rem;
  padding: 1rem 1.5rem;
  background-color: #ffffff;
  border: 1px solid #e9ecef;
  border-radius: 8px;
  box-shadow: 0 1px 3px rgba(0,0,0,0.04);
  margin-bottom: 1.5rem;
}

.selector-item {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

label {
  font-weight: 500;
  color: #495057;
  font-size: 0.95em;
}

/* 对 select 元素进行美化 */
.select-container {
  position: relative;
}

select {
  /* 重置默认外观 */
  -webkit-appearance: none;
  -moz-appearance: none;
  appearance: none;

  /* 自定义样式 */
  background-color: #fff;
  border: 1px solid #ced4da;
  border-radius: 6px;
  padding: 0.6rem 2.5rem 0.6rem 1rem; /* 右侧留出箭头空间 */
  font-size: 1em;
  color: #495057;
  cursor: pointer;
  min-width: 200px;
  transition: border-color 0.2s, box-shadow 0.2s;

  /* 自定义下拉箭头 */
  background-image: url("data:image/svg+xml,%3csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'%3e%3cpath fill='none' stroke='%23343a40' stroke-linecap='round' stroke-linejoin='round' stroke-width='2' d='M2 5l6 6 6-6'/%3e%3c/svg%3e");
  background-repeat: no-repeat;
  background-position: right 0.75rem center;
  background-size: 16px 12px;
}

select:hover {
  border-color: #adb5bd;
}

select:focus {
  border-color: #80bdff;
  outline: 0;
  box-shadow: 0 0 0 0.2rem rgba(0, 123, 255, 0.25);
}

select:disabled {
  background-color: #e9ecef;
  color: #6c757d;
  cursor: not-allowed;
  opacity: 0.7;
}
</style>