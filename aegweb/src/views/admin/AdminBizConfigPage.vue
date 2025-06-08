<!--src/views/admin/AdminBizConfigPage.vue-->
<template>
  <div class="admin-page-container">
    <header class="page-header">
      <router-link to="/admin/dashboard" class="back-link">&laquo; 返回仪表盘</router-link>
      <h1>配置业务组: <span class="biz-name-highlight">{{ bizName }}</span></h1>
    </header>

    <div v-if="isLoading" class="loading-message">正在加载...</div>
    <div v-if="loadError" class="error-message">加载失败: {{ loadError }}</div>
    <div v-if="saveMessage" :class="isSaveError ? 'error-message' : 'success-message'">{{ saveMessage }}</div>

    <div v-if="!isLoading && !loadError">
      <div class="form-section">
        <h3 class="section-header">总体设置</h3>
        <div class="setting-item">
          <span class="setting-label">允许公开查询:</span>
          <div class="setting-value">
            <input v-if="isEditing" type="checkbox" v-model="formIsPubliclySearchable" />
            <span v-else :class="formIsPubliclySearchable ? 'text-success' : 'text-danger'">{{ formIsPubliclySearchable ? '是' : '否' }}</span>
          </div>
        </div>
      </div>

      <div class="form-section">
        <h3 class="section-header">速率限制</h3>
        <p class="section-description">为此业务组API请求设置特定速率。留空则使用默认值。</p>
         <div class="setting-item">
          <span class="setting-label">每秒请求数:</span>
          <div class="setting-value">
            <input v-if="isEditing" type="number" v-model.number="formBizRateLimit.rate_limit_per_second" placeholder="例如: 10" />
            <span v-else>{{ formBizRateLimit.rate_limit_per_second || '使用默认值' }}</span>
          </div>
        </div>
        <div class="setting-item">
          <span class="setting-label">瞬时峰值:</span>
          <div class="setting-value">
            <input v-if="isEditing" type="number" v-model.number="formBizRateLimit.burst_size" placeholder="例如: 20" />
            <span v-else>{{ formBizRateLimit.burst_size || '使用默认值' }}</span>
          </div>
        </div>
      </div>

      <div class="form-section">
        <h3 class="section-header">可用表、字段与视图</h3>
        <div v-if="isEditing" class="table-config-list">
            <div v-for="tableName in physicalTablesInBiz" :key="tableName" class="table-config-item">
                <div class="table-selection">
                    <input type="checkbox" :id="`chk-${tableName}`" :value="tableName" v-model="formSelectedSearchableTables" />
                    <label :for="`chk-${tableName}`">{{ tableName }}</label>
                </div>
                <div class="table-actions">
                    <button @click="openFieldModal(tableName)" :disabled="!formSelectedSearchableTables.includes(tableName)">配置字段</button>
                    <button @click="openViewModal(tableName)" :disabled="!formSelectedSearchableTables.includes(tableName)">配置视图</button>
                </div>
            </div>
        </div>
        <div v-else>
            </div>
      </div>

      <div class="form-actions">
        <button v-if="!isEditing" @click="enterEditMode" class="submit-button edit-button">编辑</button>
        <template v-else>
          <button @click="cancelEdit" class="button-secondary">取消</button>
          <button @click="saveAllChanges" :disabled="isSaving" class="submit-button">{{ isSaving ? '保存中...' : '保存更改' }}</button>
        </template>
      </div>
    </div>

    <FieldConfigModal
      :visible="isFieldConfigModalVisible"
      :bizName="bizName"
      :tableName="tableToConfigure"
      :initialFieldSettings="fieldConfigData[tableToConfigure] || []"
      @close="isFieldConfigModalVisible = false"
      @save="handleFieldConfigSave"
    />
    <ViewConfigModal
      :visible="isViewConfigModalVisible"
      :bizName="bizName"
      :tableName="tableToConfigure"
      :fieldSettings="fieldConfigData[tableToConfigure] || []"
      :initialViewData="viewConfigData[tableToConfigure] || []"
      @close="isViewConfigModalVisible = false"
      @save="handleViewConfigSave"
    />
  </div>
</template>

<script setup>
import { defineProps, onMounted, ref, watch } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import FieldConfigModal from '@/components/admin/FieldConfigModal.vue';
import ViewConfigModal from '@/components/admin/ViewConfigModal.vue';

const props = defineProps({ bizName: { type: String, required: true }});

const isEditing = ref(false);
const isLoading = ref(true);
const isSaving = ref(false);
const loadError = ref('');
const saveMessage = ref('');
const isSaveError = ref(false);
const physicalTablesInBiz = ref([]);

const formIsPubliclySearchable = ref(true);
const formDefaultQueryTable = ref('');
const formSelectedSearchableTables = ref([]);
const formBizRateLimit = ref({ rate_limit_per_second: null, burst_size: null });
const fieldConfigData = ref({});
const viewConfigData = ref({});

const isFieldConfigModalVisible = ref(false);
const isViewConfigModalVisible = ref(false);
const tableToConfigure = ref('');

const resetFormData = (config, views) => {
    formIsPubliclySearchable.value = config?.is_publicly_searchable ?? true;
    formDefaultQueryTable.value = config?.default_query_table || '';
    formSelectedSearchableTables.value = config?.tables ? Object.keys(config.tables) : [];
    fieldConfigData.value = {};
    if (config?.tables) {
        for (const tableName in config.tables) {
            fieldConfigData.value[tableName] = Object.values(config.tables[tableName].fields || {});
        }
    }
    viewConfigData.value = views || {};
};

onMounted(async () => {
  isLoading.value = true;
  try {
    const [configRes, viewsRes, tablesRes] = await Promise.all([
      apiClient.get(ENDPOINTS.GET_BIZ_CONFIG(props.bizName)).catch(e => e),
      apiClient.get(ENDPOINTS.GET_BIZ_VIEWS(props.bizName)).catch(e => e),
      apiClient.get(ENDPOINTS.TABLES + `?biz=${props.bizName}`)
    ]);

    physicalTablesInBiz.value = tablesRes.data || [];

    const config = (configRes.response?.status === 404) ? {} : configRes.data;
    const views = (viewsRes.response?.status === 404) ? {} : viewsRes.data;

    resetFormData(config, views);

  } catch (error) {
    loadError.value = error.response?.data?.error || error.message;
  } finally {
    isLoading.value = false;
  }
});

const enterEditMode = () => { isEditing.value = true; };
const cancelEdit = () => { isEditing.value = false; };

const saveAllChanges = async () => {
  isSaving.value = true;
  saveMessage.value = '';
  isSaveError.value = false;
  try {
    const promises = [];
    promises.push(apiClient.put(ENDPOINTS.UPDATE_BIZ_SETTINGS(props.bizName), {
      is_publicly_searchable: formIsPubliclySearchable.value,
      default_query_table: formDefaultQueryTable.value || null,
    }));
    promises.push(apiClient.put(ENDPOINTS.UPDATE_BIZ_TABLES(props.bizName), {
      searchable_tables: formSelectedSearchableTables.value,
    }));
    for (const tableName of formSelectedSearchableTables.value) {
      if (fieldConfigData.value[tableName]) {
        promises.push(apiClient.put(ENDPOINTS.UPDATE_TABLE_FIELDS(props.bizName, tableName), fieldConfigData.value[tableName]));
      }
    }
    promises.push(apiClient.put(ENDPOINTS.UPDATE_BIZ_VIEWS(props.bizName), viewConfigData.value));
    promises.push(apiClient.put(ENDPOINTS.UPDATE_BIZ_RATELIMIT(props.bizName), formBizRateLimit.value));

    await Promise.all(promises);

    saveMessage.value = '配置已成功保存！';
    isEditing.value = false;
  } catch (error) {
    isSaveError.value = true;
    saveMessage.value = `保存失败: ${error.response?.data?.error || error.message}`;
  } finally {
    isSaving.value = false;
  }
};

const openFieldModal = (tableName) => {
  tableToConfigure.value = tableName;
  isFieldConfigModalVisible.value = true;
};
const handleFieldConfigSave = (payload) => {
  fieldConfigData.value[payload.tableName] = payload.settings;
  isFieldConfigModalVisible.value = false;
};
const openViewModal = (tableName) => {
  tableToConfigure.value = tableName;
  isViewConfigModalVisible.value = true;
};
const handleViewConfigSave = (payload) => {
  viewConfigData.value[tableToConfigure.value] = payload;
  isViewConfigModalVisible.value = false;
};
</script>

<style scoped>
.admin-page-container { padding: 2rem; max-width: 900px; margin: 2rem auto; background-color: #ffffff; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.08); font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; }
.page-header { margin-bottom: 2rem; padding-bottom: 1rem; border-bottom: 1px solid #e9ecef; }
.back-link { display: inline-block; margin-bottom: 0.75rem; color: #007bff; text-decoration: none; font-size: 0.95em; }
.page-header h1 { font-size: 1.8em; color: #2c3e50; margin: 0; font-weight: 600; }
.biz-name-highlight { color: #007bff; }
.loading-message, .error-message, .info-message, .success-message { padding: 1rem 1.25rem; margin-bottom: 1.5rem; border-radius: 5px; border: 1px solid; }

.error-message { background-color: #f8d7da; border-color: #f5c6cb; color: #721c24; }
.success-message { background-color: #d4edda; border-color: #c3e6cb; color: #155724; }

.form-section { background-color: #f8f9fa; padding: 1.5rem; border-radius: 6px; margin-bottom: 2rem; border: 1px solid #e9ecef; }
.section-header { margin-top: 0; margin-bottom: 1.5rem; font-size: 1.25em; padding-bottom: 0.75rem; border-bottom: 1px solid #dee2e6;}
.section-description { font-size: 0.9em; color: #6c757d; margin-top: -1rem; margin-bottom: 1.5rem; }
.setting-item { display: flex; align-items: flex-start; gap: 1rem; margin-bottom: 1rem; }
.setting-item:last-child { margin-bottom: 0; }
.setting-label { font-weight: 500; color: #495057; width: 120px; flex-shrink: 0; padding-top: 5px; }
.setting-value { flex-grow: 1; }
.setting-value .form-text { margin-top: 0.5rem; font-size: 0.85em; color: #6c757d; }
.text-success { color: #155724; font-weight: 500; }
.text-danger { color: #721c24; font-weight: 500; }

.table-config-list { display: flex; flex-direction: column; gap: 0.75rem; }
.table-config-item { display: flex; align-items: center; justify-content: space-between; padding: 0.75rem 1.25rem; background-color: #fff; border: 1px solid #dee2e6; border-radius: 6px; transition: box-shadow 0.2s ease; }
.table-config-item:hover { box-shadow: 0 2px 8px rgba(0,0,0,0.06); }
.table-selection { display: flex; align-items: center; gap: 0.75rem; }

.table-actions { display: flex; align-items: center; gap: 1.5rem; }

.form-actions { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 1rem; }
.submit-button { padding: 0.75rem 1.5rem; background-color: #28a745; color: white; border: none; border-radius: 5px; cursor: pointer; font-size: 1.05em; font-weight: 500; }
.submit-button:disabled { background-color: #a3d9a5; cursor: not-allowed; }
.edit-button { background-color: #007bff; }
.edit-button:hover:not(:disabled) { background-color: #0069d9; }
.button-secondary { background-color: #6c757d; color: white; padding: 0.75rem 1.5rem; border-radius: 5px; border: none; cursor: pointer; }

.modal-header h3 { margin: 0; font-size: 1.5em; }

.fields-table th, .fields-table td { padding: 0.75rem; text-align: left; border-bottom: 1px solid #dee2e6; vertical-align: middle; }
.fields-table th { background-color: #f8f9fa; font-weight: 600; }
.fields-table input[type="text"], .fields-table input[type="number"] { width: 100%; max-width: 150px; padding: 0.4rem; border: 1px solid #ced4da; border-radius: 4px; box-sizing: border-box; }
.fields-table input[type="checkbox"] { transform: scale(1.2); }

.view-list-panel h4 { margin-top: 0; }

.view-list li { padding: 0.75rem 1rem; margin-bottom: 0.5rem; border-radius: 5px; cursor: pointer; transition: background-color 0.2s ease; border: 1px solid transparent; display: flex; justify-content: space-between; align-items: center; }
.view-list li:hover { background-color: #f8f9fa; }
.view-list li.active { background-color: #e7f3ff; border-color: #007bff; font-weight: 500; }

.view-editor-panel h4 { margin-top: 0; }

.form-group label { margin-bottom: 0.5rem; font-weight: 500; font-size: 0.9em; }
.form-group input[type="text"], .form-group select { padding: 0.6rem; border: 1px solid #ced4da; border-radius: 4px; width: 100%; }

</style>