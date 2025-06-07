<!--src/components/BizTableSelectors.vue-->
<template>
  <div class="selectors-wrapper">
    <div class="selector-item">
      <label for="biz-select">业务组:</label>
      <select id="biz-select" :value="biz" @change="onBizChange" :disabled="isLoadingBiz">
        <option disabled value="">{{ isLoadingBiz ? '加载中...' : '请选择业务组' }}</option>
        <option v-for="b in bizOptions" :key="b.name" :value="b.name">
          {{ b.display_name || b.name }}
        </option>
      </select>
    </div>

    <div class="selector-item">
      <label for="table-select">数据表:</label>
      <select id="table-select" :value="table" @change="$emit('update:table', $event.target.value)" :disabled="!biz || isLoadingTables">
        <option disabled value="">{{ isLoadingTables ? '加载中...' : '请选择数据表' }}</option>
        <option v-for="t in tableOptions" :key="t" :value="t">{{ t }}</option>
      </select>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, watch } from 'vue';
import apiClient from '../services/apiClient';
import { ENDPOINTS } from '../services/apiEndpoints';

// 定义 props 和 emits，保持不变
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
      display_name: rawData[key]
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
      tableOptions.value = response.data;
    } catch (error) {
      console.error("加载数据表失败:", error);
    } finally {
      isLoadingTables.value = false;
    }
  }
});

// onBizChange 方法，保持不变
const onBizChange = (event) => {
  emit('update:biz', event.target.value);
};
</script>

<style scoped>
.selectors-wrapper {
  display: flex;
  gap: 2rem;
  padding: 1rem;
  background-color: #f7f7f7;
  border-radius: 8px;
  margin-bottom: 1.5rem;
}
.selector-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
select {
  padding: 0.5rem;
  border-radius: 4px;
  border: 1px solid #ccc;
}
</style>