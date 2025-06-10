<template>
  <div class="biz-detail-container">
    <div class="page-header">
      <router-link :to="{ name: 'AdminBizManagement' }" class="back-link">
        &larr; 返回业务管理
      </router-link>
      <h1 class="page-title">
        配置: <span class="biz-name-highlight">{{ bizName }}</span>
      </h1>
    </div>

    <div v-if="isLoading" class="status-message loading">正在加载配置信息...</div>

    <div v-if="!isLoading" class="config-content">

      <div v-if="loadError" class="status-message error">加载失败: {{ loadError }}</div>

      <div v-if="isNewConfiguration && !loadError" class="status-message info">
        这是一个新发现的业务组，请点击下方的 "编辑" 按钮开始进行初始配置。
      </div>

      <div v-if="saveMessage" :class="['status-message', isSaveError ? 'error' : 'success']">
        {{ saveMessage }}
      </div>

      <template v-if="!loadError">
        <div class="form-section">
          <h3 class="section-header">基本设置</h3>
          <div class="setting-item">
            <label for="is-public">允许公开查询:</label>
            <div class="setting-value">
              <input type="checkbox" id="is-public" v-model="formState.is_publicly_searchable" :disabled="!isEditing" class="toggle-switch" />
              <span v-if="!isEditing" :class="formState.is_publicly_searchable ? 'text-success' : 'text-danger'">
                {{ formState.is_publicly_searchable ? '是' : '否' }}
              </span>
            </div>
          </div>
        </div>

        <div class="form-section">
          <h3 class="section-header">数据表管理</h3>
          <p class="section-description">勾选允许前端进行查询的数据表，并为已勾选的表进行统一配置。</p>

          <div class="table-list">
            <div v-for="tableName in allPhysicalTables" :key="tableName" class="table-item">
              <div class="table-selector">
                <input
                  type="checkbox"
                  :id="`chk-${tableName}`"
                  :value="tableName"
                  v-model="formState.searchable_tables"
                  :disabled="!isEditing"
                />
                <label :for="`chk-${tableName}`">{{ tableName }}</label>
              </div>
              <div class="table-actions">
                <button
                  @click="openConfigModal(tableName)"
                  :disabled="!formState.searchable_tables.includes(tableName) || !isEditing"
                  class="button-tertiary"
                >
                  配置
                </button>
              </div>
              </div>
          </div>
        </div>

        <div class="page-actions">
          <button v-if="!isEditing" @click="enterEditMode" class="button-primary">编辑</button>
          <template v-else>
            <button @click="cancelEdit" class="button-secondary">取消</button>
            <button @click="saveAllChanges" :disabled="isSaving" class="button-primary">
              {{ isSaving ? '保存中...' : '保存所有更改' }}
            </button>
          </template>
        </div>
      </template>
    </div>

    <UnifiedTableConfigModal
      v-if="isUnifiedModalVisible"
      :visible="isUnifiedModalVisible"
      :bizName="bizName"
      :tableName="tableToConfigure"
      @update:visible="isUnifiedModalVisible = $event"
      @saved="handleUnifiedSave"
    />
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import { cloneDeep } from 'lodash';
import UnifiedTableConfigModal from '@/components/admin/UnifiedTableConfigModal.vue';

// --- Props & State ---
// 使用 useRoute 获取动态参数，不再需要 defineProps
const route = useRoute();
const bizName = route.params.bizName;

const isLoading = ref(true);
const isSaving = ref(false);
const isEditing = ref(false);
const loadError = ref('');
const saveMessage = ref('');
const isSaveError = ref(false);

// 新增状态，用于标记是否为全新配置
const isNewConfiguration = ref(false);

const allPhysicalTables = ref([]);
const formState = reactive({
  is_publicly_searchable: false,
  searchable_tables: [],
  field_configs: {}, // 注意：此状态在当前组件中不再直接保存，而是通过模态框处理
  view_configs: {}    // 注意：同上
});
let originalFormState = null;

const isUnifiedModalVisible = ref(false);
const tableToConfigure = ref('');

// --- Data Loading (已重构) ---
const loadData = async () => {
  isLoading.value = true;
  loadError.value = '';
  isNewConfiguration.value = false;

  try {
    // 并行获取所有需要的数据，并单独处理每个请求的失败情况
    const [configRes, tablesRes] = await Promise.all([
      apiClient.get(ENDPOINTS.GET_BIZ_CONFIG(bizName)).catch(e => e),
      apiClient.get(ENDPOINTS.TABLES, { params: { biz: bizName } }).catch(e => e)
    ]);

    // 1. 检查获取物理表列表的请求，如果失败，则是致命错误
    if (tablesRes.response?.status === 404 || tablesRes instanceof Error) {
        throw new Error(tablesRes.response?.data?.error || "获取物理表列表失败，无法进行配置。");
    }
    allPhysicalTables.value = tablesRes.data || [];

    if (configRes.response?.status === 404) {
      isNewConfiguration.value = true;
      formState.is_publicly_searchable = false;
      formState.searchable_tables = [];
    } else if (configRes instanceof Error) {
      throw configRes;
    } else {

      const config = configRes.data;
      formState.is_publicly_searchable = config.is_publicly_searchable ?? false;
      formState.searchable_tables = config.tables ? Object.keys(config.tables) : [];
    }

    originalFormState = cloneDeep(formState);

  } catch (error) {
    loadError.value = error.message || '加载业务组配置时发生未知错误';
    console.error("加载业务组详情失败:", error);
  } finally {
    isLoading.value = false;
  }
};

onMounted(loadData);


const enterEditMode = () => { isEditing.value = true; };
const cancelEdit = () => {
  Object.assign(formState, cloneDeep(originalFormState));
  isEditing.value = false;
};

const openConfigModal = (tableName) => {
  tableToConfigure.value = tableName;
  isUnifiedModalVisible.value = true;
};

const saveAllChanges = async () => {
  isSaving.value = true;
  saveMessage.value = '';
  isSaveError.value = false;
  try {

    await apiClient.put(ENDPOINTS.UPDATE_BIZ_SETTINGS(bizName), {
      is_publicly_searchable: formState.is_publicly_searchable
    });

    await apiClient.put(ENDPOINTS.UPDATE_BIZ_TABLES(bizName), {
      searchable_tables: formState.searchable_tables
    });

    saveMessage.value = '所有配置已成功保存！';
    isEditing.value = false;

    // 重新加载数据以反映最新状态，并标记为“已配置”
    isNewConfiguration.value = false;
    await loadData();

  } catch (error) {
    isSaveError.value = true;
    saveMessage.value = `保存失败: ${error.response?.data?.error || error.message}`;
  } finally {
    isSaving.value = false;
    setTimeout(() => saveMessage.value = '', 5000);
  }
};
</script>


<style scoped>
/* 样式与之前相同，此处省略以保持简洁 */
.biz-detail-container { max-width: 900px; }
.page-header { margin-bottom: 2rem; }
.back-link { color: #007bff; text-decoration: none; font-weight: 500; display: block; margin-bottom: 1rem; }
.page-title { font-size: 1.8em; color: #2c3e50; margin: 0; font-weight: 600; }
.biz-name-highlight { color: #007bff; }
.status-message { padding: 1rem; margin-bottom: 1.5rem; border-radius: 5px; border: 1px solid; }
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; color: #0050b3; text-align: center; }
.status-message.error { background-color: #fff1f0; border-color: #ffa39e; color: #cf1322; }
.status-message.info { background-color: #f0f9ff; border-color: #abdcff; color: #005a9e; }
.status-message.success { background-color: #d4edda; border-color: #c3e6cb; color: #155724; }
.form-section { background-color: #ffffff; padding: 1.5rem 2rem; border-radius: 8px; margin-bottom: 2rem; border: 1px solid #e9ecef; }
.section-header { margin-top: 0; margin-bottom: 1.5rem; font-size: 1.25em; padding-bottom: 0.75rem; border-bottom: 1px solid #dee2e6;}
.section-description { font-size: 0.9em; color: #6c757d; margin-top: -1rem; margin-bottom: 1.5rem; }
.setting-item { display: flex; align-items: center; gap: 1rem; }
.setting-item label { font-weight: 500; width: 120px; }
.text-success { color: #155724; font-weight: 500; }
.text-danger { color: #721c24; font-weight: 500; }
.table-list { display: flex; flex-direction: column; gap: 0.75rem; }
.table-item { display: flex; align-items: center; justify-content: space-between; padding: 0.75rem 1rem; background-color: #f8f9fa; border: 1px solid #dee2e6; border-radius: 6px; }
.table-selector { display: flex; align-items: center; gap: 0.75rem; }
.table-selector label { font-weight: 500; }
.table-selector input[type="checkbox"] { transform: scale(1.2); }
.table-actions { display: flex; gap: 0.75rem; }
.page-actions { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 1rem; }
.button-primary, .button-secondary, .button-tertiary { padding: 0.6rem 1.2rem; border: none; border-radius: 5px; cursor: pointer; font-weight: 500; transition: all 0.2s; }
.button-primary { background-color: #007bff; color: white; }
.button-primary:disabled { background-color: #a0cfff; cursor: not-allowed; }
.button-secondary { background-color: #6c757d; color: white; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; }
.button-tertiary:disabled { opacity: 0.5; cursor: not-allowed; }
.toggle-switch { position: relative; width: 40px; height: 22px; -webkit-appearance: none; appearance: none; background: #ccc; border-radius: 22px; cursor: pointer; transition: background 0.3s; }
.toggle-switch:checked { background: #007bff; }
.toggle-switch::before { content: ''; position: absolute; width: 18px; height: 18px; border-radius: 50%; background: white; top: 2px; left: 2px; transition: 0.3s; }
.toggle-switch:checked::before { transform: translateX(18px); }

.biz-detail-container { max-width: 900px; }
.page-header { margin-bottom: 2rem; }
.back-link { color: #007bff; text-decoration: none; font-weight: 500; display: block; margin-bottom: 1rem; }
.page-title { font-size: 1.8em; color: #2c3e50; margin: 0; font-weight: 600; }
.biz-name-highlight { color: #007bff; }
.status-message { padding: 1rem; margin-bottom: 1.5rem; border-radius: 5px; border: 1px solid; }
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; color: #0050b3; text-align: center; }
.status-message.error { background-color: #fff1f0; border-color: #ffa39e; color: #cf1322; }
.status-message.success { background-color: #d4edda; border-color: #c3e6cb; color: #155724; }
.form-section { background-color: #ffffff; padding: 1.5rem 2rem; border-radius: 8px; margin-bottom: 2rem; border: 1px solid #e9ecef; }
.section-header { margin-top: 0; margin-bottom: 1.5rem; font-size: 1.25em; padding-bottom: 0.75rem; border-bottom: 1px solid #dee2e6;}
.section-description { font-size: 0.9em; color: #6c757d; margin-top: -1rem; margin-bottom: 1.5rem; }
.setting-item { display: flex; align-items: center; gap: 1rem; }
.setting-item label { font-weight: 500; width: 120px; }
.text-success { color: #155724; font-weight: 500; }
.text-danger { color: #721c24; font-weight: 500; }
.table-list { display: flex; flex-direction: column; gap: 0.75rem; }
.table-item { display: flex; align-items: center; justify-content: space-between; padding: 0.75rem 1rem; background-color: #f8f9fa; border: 1px solid #dee2e6; border-radius: 6px; }
.table-selector { display: flex; align-items: center; gap: 0.75rem; }
.table-selector label { font-weight: 500; }
.table-selector input[type="checkbox"] { transform: scale(1.2); }
.table-actions { display: flex; gap: 0.75rem; }
.page-actions { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 1rem; }
.button-primary, .button-secondary, .button-tertiary { padding: 0.6rem 1.2rem; border: none; border-radius: 5px; cursor: pointer; font-weight: 500; transition: all 0.2s; }
.button-primary { background-color: #007bff; color: white; }
.button-primary:disabled { background-color: #a0cfff; cursor: not-allowed; }
.button-secondary { background-color: #6c757d; color: white; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; }
.button-tertiary:disabled { opacity: 0.5; cursor: not-allowed; }
.toggle-switch { position: relative; width: 40px; height: 22px; -webkit-appearance: none; appearance: none; background: #ccc; border-radius: 22px; cursor: pointer; transition: background 0.3s; }
.toggle-switch:checked { background: #007bff; }
.toggle-switch::before { content: ''; position: absolute; width: 18px; height: 18px; border-radius: 50%; background: white; top: 2px; left: 2px; transition: 0.3s; }
.toggle-switch:checked::before { transform: translateX(18px); }
</style>