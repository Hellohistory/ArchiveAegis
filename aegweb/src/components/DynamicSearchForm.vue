<!--src/components/DynamicSearchForm.vue-->
<template>
  <div class="dynamic-form-container">
    <div v-for="(col, index) in searchableColumns" :key="col.original_name" class="form-row">
      <select v-if="index > 0" v-model="formState[col.original_name].logic" class="logic-operator">
        <option value="AND">并且 (AND)</option>
        <option value="OR">或者 (OR)</option>
      </select>

      <label :for="col.original_name">{{ col.name }}:</label>

      <input
        v-if="col.dataType === 'date'"
        type="date"
        :id="col.original_name"
        v-model="formState[col.original_name].value"
      />
      <input
        v-else-if="col.dataType === 'number'"
        type="number"
        :id="col.original_name"
        v-model="formState[col.original_name].value"
        :placeholder="`输入${col.name}`"
      />
      <input
        v-else
        type="text"
        :id="col.original_name"
        v-model="formState[col.original_name].value"
        :placeholder="`输入${col.name}`"
      />
      <div class="fuzzy-checkbox" v-if="col.dataType === 'string' || !col.dataType">
        <input type="checkbox" :id="`fuzzy-${col.original_name}`" v-model="formState[col.original_name].fuzzy" />
        <label :for="`fuzzy-${col.original_name}`">模糊</label>
      </div>
    </div>

    <div class="form-actions">
      <button @click="triggerSearch" :disabled="isLoading">搜索</button>
      <button @click="resetForm" type="button" class="secondary">重置</button>
    </div>
  </div>
</template>

<script setup>
import { ref, watch, computed } from 'vue';

const props = defineProps({
  columns: Array,
  isLoading: Boolean,
});
const emit = defineEmits(['search']);

const formState = ref({});

const searchableColumns = computed(() => {
  // 这里的过滤逻辑也保持不变
  return props.columns.filter(c => c.is_searchable);
});

watch(searchableColumns, (newColumns) => {
  const newFormState = {};
  newColumns.forEach(col => {
    newFormState[col.original_name] = {
      value: '',
      fuzzy: false,
      logic: 'AND',
    };
  });
  formState.value = newFormState;
}, { immediate: true });

const resetForm = () => {
    const newColumns = props.columns.filter(c => c.is_searchable);
    const newFormState = {};
    newColumns.forEach(col => {
        newFormState[col.original_name] = { value: '', fuzzy: false, logic: 'AND' };
    });
    formState.value = newFormState;
}

const triggerSearch = () => {
  const activeFields = searchableColumns.value
    .map(col => col.original_name)
    .filter(fieldName => formState.value[fieldName] && formState.value[fieldName].value.toString().trim() !== '');

  if (activeFields.length === 0) {
    alert("请输入至少一个查询条件。");
    return;
  }

  const searchParams = {
    fields: activeFields.map(fieldName => fieldName),
    values: activeFields.map(fieldName => formState.value[fieldName].value),
    fuzzy: activeFields.map(fieldName => formState.value[fieldName].fuzzy),
    logic: activeFields.slice(1).map(fieldName => formState.value[fieldName].logic),
  };

  emit('search', searchParams);
};
</script>

<style scoped>
/* 样式部分保持不变 */
.dynamic-form-container {
  padding: 1rem;
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  margin-bottom: 1.5rem;
}
.form-row {
  display: flex;
  align-items: center;
  gap: 1rem;
  margin-bottom: 1rem;
}
.form-row label {
  width: 120px;
  text-align: right;
}
.form-row input[type="text"],
.form-row input[type="date"],
.form-row input[type="number"] {
  flex-grow: 1;
  padding: 0.5rem;
  border-radius: 4px;
  border: 1px solid #ccc;
}
.logic-operator {
  width: 120px;
}
.form-actions {
  padding-top: 1rem;
  border-top: 1px solid #e0e0e0;
  text-align: center;
}
button {
  padding: 0.6rem 1.5rem;
  border: none;
  background-color: #007bff;
  color: white;
  border-radius: 4px;
  cursor: pointer;
}
button.secondary {
    background-color: #6c757d;
    margin-left: 1rem;
}
button:disabled {
  background-color: #ccc;
  cursor: not-allowed;
}
.fuzzy-checkbox {
    display: flex;
    align-items: center;
    gap: 0.25rem;
}
</style>