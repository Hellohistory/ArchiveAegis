<!--src/components/admin/FieldConfigModal.vue-->
<template>
  <div v-if="visible" class="modal-overlay" @click.self="close">
    <div class="modal-content">
      <header class="modal-header">
        <h3>配置字段: <span class="biz-name-highlight">{{ tableName }}</span></h3>
        <button @click="close" class="close-button">&times;</button>
      </header>
      <div class="modal-body">
        <p class="section-description">配置字段的基础属性，如是否可搜索、返回，及其数据类型。</p>
        <div v-if="isLoading" class="loading-message">正在加载物理列...</div>
        <div v-else>
          <table class="fields-table">
            <thead>
              <tr>
                <th>物理字段名</th>
                <th>可搜索</th>
                <th>可返回</th>
                <th>数据类型</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(field, index) in localSettings" :key="field.field_name">
                <td>{{ field.field_name }}</td>
                <td><input type="checkbox" v-model="localSettings[index].is_searchable" /></td>
                <td><input type="checkbox" v-model="localSettings[index].is_returnable" /></td>
                <td>
                  <select v-model="localSettings[index].dataType">
                    <option value="string">文本 (string)</option>
                    <option value="number">数字 (number)</option>
                    <option value="date">日期 (date)</option>
                  </select>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <footer class="modal-footer">
        <button @click="close" class="button-secondary">取消</button>
        <button @click="save" class="button-primary">确定</button>
      </footer>
    </div>
  </div>
</template>

<script setup>
import { ref, watch, defineProps, defineEmits } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import { cloneDeep } from 'lodash';

const props = defineProps({
  visible: Boolean,
  bizName: String,
  tableName: String,
  initialFieldSettings: {
    type: Array,
    default: () => []
  },
});

const emit = defineEmits(['close', 'save']);

const isLoading = ref(false);
const localSettings = ref([]);
const physicalFields = ref([]);

watch(() => props.visible, async (isVisible) => {
  if (isVisible && props.tableName) {
    isLoading.value = true;
    try {
      const response = await apiClient.get(ENDPOINTS.GET_PHYSICAL_COLUMNS(props.bizName, props.tableName));
      physicalFields.value = response.data || [];

      const settingsMap = new Map(props.initialFieldSettings.map(s => [s.field_name, s]));
      localSettings.value = physicalFields.value.map(fieldName => {
        const savedSetting = settingsMap.get(fieldName);
        return {
          field_name: fieldName,
          is_searchable: savedSetting?.is_searchable ?? false,
          is_returnable: savedSetting?.is_returnable ?? true,
          dataType: savedSetting?.dataType ?? 'string',
        };
      });
    } catch (error) {
      console.error("加载物理列失败:", error);
      emit('close');
    } finally {
      isLoading.value = false;
    }
  }
});

const close = () => emit('close');
const save = () => emit('save', cloneDeep(localSettings.value));
</script>

<style scoped>
/* 粘贴或引入通用的 modal 和 table 样式 */
.modal-overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background-color: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-content { background-color: #fff; border-radius: 8px; box-shadow: 0 5px 15px rgba(0,0,0,0.3); width: 90%; max-width: 800px; display: flex; flex-direction: column; max-height: 90vh; }
.modal-header { padding: 1.5rem; border-bottom: 1px solid #e9ecef; display: flex; justify-content: space-between; align-items: center; }
.modal-body { padding: 1.5rem; overflow-y: auto; }
.modal-footer { padding: 1rem 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 1rem; }
.button-primary { background-color: #007bff; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.button-secondary { background-color: #6c757d; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.fields-table { width: 100%; border-collapse: collapse; }
.fields-table th, .fields-table td { padding: 0.75rem; text-align: left; border-bottom: 1px solid #dee2e6; }
/* ... etc */
</style>