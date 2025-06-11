<template>
  <div class="search-page-container">
    <div class="search-box">
      <header class="search-header">
        <h1>数据检索中心</h1>
        <p>选择业务组，构建查询条件，开始您的数据探索之旅。</p>
      </header>

      <div class="form-group">
        <label for="biz-select">业务组</label>
        <select id="biz-select" v-model="selectedBiz" @change="handleBizChange" :disabled="isLoading">
          <option disabled value="">请选择一个业务组...</option>
          <option v-for="bizName in bizList" :key="bizName" :value="bizName">
            {{ bizName }}
          </option>
        </select>
      </div>

      <div v-if="tableList.length > 0" class="form-group">
        <label for="table-select">数据表</label>
        <select id="table-select" v-model="selectedTable" :disabled="isLoading">
          <option v-for="tableName in tableList" :key="tableName" :value="tableName">
            {{ tableName }}
          </option>
        </select>
      </div>

      <div v-if="selectedTable && searchableFields.length > 0" class="query-builder">
        <div v-for="(condition, index) in queryConditions" :key="condition.id" class="condition-row">
          <select v-if="index > 0" v-model="condition.logic" class="logic-select">
            <option value="AND">AND</option>
            <option value="OR">OR</option>
          </select>
          <div v-else class="logic-placeholder">WHERE</div>

          <select v-model="condition.field" class="field-select">
            <option value="">选择字段</option>
            <option v-for="field in searchableFields" :key="field.original_name" :value="field.original_name">
              {{ field.name }}
            </option>
          </select>

          <select v-model="condition.fuzzy" class="operator-select">
            <option :value="false">等于</option>
            <option :value="true">包含</option>
          </select>

          <input type="text" v-model.trim="condition.value" placeholder="输入查询值" class="value-input"/>

          <button @click="removeCondition(index)" class="remove-btn" title="删除此条件">×</button>
        </div>
        <button @click="addCondition" class="add-btn">+ 添加条件</button>
      </div>

      <div v-if="isLoading" class="status-info">正在加载配置...</div>
      <div v-if="error" class="status-info error">{{ error }}</div>

      <div class="form-group">
        <button @click="handleSearch" :disabled="!canSearch || isLoading" class="search-btn">
          <span v-if="!isLoading">开始检索</span>
          <span v-else>处理中...</span>
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, computed, watch } from 'vue';
import { useRouter } from 'vue-router';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';

const router = useRouter();

const bizList = ref([]);
const selectedBiz = ref('');
const tableList = ref([]);
const selectedTable = ref('');
const searchableFields = ref([]);
const queryConditions = reactive([{ id: 1, field: '', value: '', fuzzy: false, logic: 'AND' }]);
const isLoading = ref(false);
const error = ref('');

const canSearch = computed(() => {
  return selectedBiz.value && selectedTable.value && queryConditions.some(c => c.field && c.value);
});

onMounted(async () => {
  isLoading.value = true;
  try {
    const response = await apiClient.get(ENDPOINTS.ADMIN_CONFIGURED_BIZ_NAMES);

    bizList.value = response.data;
  } catch (err) {
    error.value = "获取业务组失败，请联系管理员。";
    console.error(err);
  } finally {
    isLoading.value = false;
  }
});

const handleBizChange = async () => {
  if (!selectedBiz.value) return;

  isLoading.value = true;
  error.value = '';
  tableList.value = [];
  selectedTable.value = '';
  searchableFields.value = [];
  queryConditions.splice(0, queryConditions.length, { id: 1, field: '', value: '', fuzzy: false, logic: 'AND' });

  try {
    const response = await apiClient.get(ENDPOINTS.TABLES, {
      params: { biz: selectedBiz.value }
    });

    if (response.data && response.data.length > 0) {
      tableList.value = response.data;
      selectedTable.value = tableList.value[0];
    } else {
      error.value = "该业务组下未找到可查询的数据表。";
    }
  } catch (err) {
    error.value = err.response?.data?.error || "加载数据表列表失败。";
    console.error(err);
  } finally {
    isLoading.value = false;
  }
};

watch(selectedTable, async (newTable, oldTable) => {
  if (!newTable) {
    searchableFields.value = [];
    return;
  }

  if(newTable === oldTable && searchableFields.value.length > 0) return;

  isLoading.value = true;
  error.value = '';
  queryConditions.splice(0, queryConditions.length, { id: 1, field: '', value: '', fuzzy: false, logic: 'AND' });

  try {
    const response = await apiClient.get(ENDPOINTS.COLUMNS, {
      params: { biz: selectedBiz.value, table: newTable }
    });
    searchableFields.value = response.data.filter(field => field.is_searchable);
    if (searchableFields.value.length === 0) {
      error.value = "该数据表没有可供查询的字段。";
    }
  } catch (err) {
    error.value = err.response?.data?.error || "加载字段信息失败。";
    searchableFields.value = [];
    console.error(err);
  } finally {
    isLoading.value = false;
  }
});


const addCondition = () => {
  queryConditions.push({ id: Date.now(), field: '', value: '', fuzzy: false, logic: 'AND' });
};

const removeCondition = (index) => {
  if (queryConditions.length > 1) {
    queryConditions.splice(index, 1);
  }
};

const handleSearch = () => {
  const validConditions = queryConditions.filter(c => c.field && c.value);
  if (validConditions.length === 0) {
    error.value = "请至少填写一个完整的查询条件。";
    return;
  }

  const queryParams = {
    biz: selectedBiz.value,
    table: selectedTable.value,
    fields: validConditions.map(c => c.field),
    values: validConditions.map(c => c.value),
    fuzzy: validConditions.map(c => c.fuzzy),
    logic: validConditions.length > 1 ? validConditions.slice(1).map(c => c.logic) : []
  };

  router.push({ name: 'Results', query: queryParams });
};
</script>

<style scoped>
.search-page-container {
  display: flex;
  justify-content: center;
  align-items: flex-start;
  padding: 2rem;
  background-color: #f4f7f6;
  min-height: 100vh;
}
.search-box {
  width: 100%;
  max-width: 800px;
  background: white;
  padding: 2rem;
  border-radius: 8px;
  box-shadow: 0 4px 12px rgba(0,0,0,0.08);
}
.search-header { text-align: center; margin-bottom: 2rem; }
.form-group { margin-bottom: 1.5rem; }
label { display: block; margin-bottom: 0.5rem; font-weight: 600; }
select, input { width: 100%; padding: 0.75rem; border: 1px solid #ccc; border-radius: 4px; }
.query-builder { border-top: 1px solid #eee; padding-top: 1.5rem; }

.condition-row { display: grid; grid-template-columns: 80px 1fr 1fr 1fr auto; gap: 0.5rem; align-items: center; margin-bottom: 0.5rem; }
.logic-placeholder { text-align: center; color: #999; font-family: monospace; }
.remove-btn, .add-btn { border: none; background: none; cursor: pointer; padding: 0.5rem; }
.remove-btn { color: red; font-size: 1.5rem; line-height: 1; }
.add-btn { color: green; font-weight: bold; }
.search-btn { width: 100%; padding: 1rem; background-color: #007bff; color: white; border: none; border-radius: 4px; font-size: 1.1rem; cursor: pointer; }
.search-btn:disabled { background-color: #a0aec0; }
.status-info { text-align: center; margin: 1rem 0; padding: 0.75rem; border-radius: 4px; }
.error { background-color: #fef2f2; color: #ef4444; }
</style>