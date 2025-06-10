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
    <div v-if="loadError" class="status-message error">加载失败: {{ loadError }}</div>

    <div v-if="!isLoading && !loadError" class="config-content">
      <div v-if="saveMessage" :class="['status-message', isSaveError ? 'error' : 'success']">
        {{ saveMessage }}
      </div>

      <div class="form-section">
        <h3 class="section-header">基本设置</h3>
        <div class="setting-item">
          <label for="is-public">允许公开查询:</label>
          <div class="setting-value">
            <input
              type="checkbox"
              id="is-public"
              v-model="formState.is_publicly_searchable"
              @change="togglePublicSearchable"
              class="toggle-switch"
            />
          </div>
        </div>
      </div>

      <div class="form-section">
        <h3 class="section-header">数据表管理</h3>
        <p class="section-description">
          勾选以启用数据表，启用后即可进行详细配置。所有操作都将立即保存。
        </p>
        <div class="table-list">
          <div v-for="tableName in allPhysicalTables" :key="tableName" class="table-item">
            <div class="table-selector">
              <input
                type="checkbox"
                :id="`chk-${tableName}`"
                :checked="formState.searchable_tables.includes(tableName)"
                @change="handleTableSelectionChange($event, tableName)"
              />
              <label :for="`chk-${tableName}`">{{ tableName }}</label>
            </div>
            <div class="table-actions">
              <button
                @click="openConfigModal(tableName)"
                :disabled="!formState.searchable_tables.includes(tableName)"
                class="button-tertiary"
              >
                配置
              </button>
            </div>
          </div>
        </div>
        <div v-if="allPhysicalTables.length === 0" class="status-message info">
          该业务组下未发现任何物理表。
        </div>
      </div>
    </div>

    <UnifiedTableConfigModal
      v-if="isUnifiedModalVisible"
      :visible="isUnifiedModalVisible"
      :bizName="bizName"
      :tableName="tableToConfigure"
      @update:visible="isUnifiedModalVisible = $event"
      @saved="handleModalSave"
    />

    <ConfirmationModal
      v-model:visible="isConfirmModalVisible"
      title="确认禁用"
      :message="confirmMessage"
      @confirm="executeTableDisabling"
    />
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue';
import { useRoute } from 'vue-router';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import UnifiedTableConfigModal from '@/components/admin/UnifiedTableConfigModal.vue';
import ConfirmationModal from '@/components/admin/ConfirmationModal.vue';

const route = useRoute();
const bizName = route.params.bizName;

const isLoading = ref(true);
const loadError = ref('');
const saveMessage = ref('');
const isSaveError = ref(false);

const allPhysicalTables = ref([]);
const formState = reactive({
  is_publicly_searchable: false,
  searchable_tables: [],
});

const isUnifiedModalVisible = ref(false);
const tableToConfigure = ref('');

const isConfirmModalVisible = ref(false);
const confirmMessage = ref('');
const tableToDisable = ref(null);

let saveMessageTimer = null;

const showSaveMessage = (message, isError = false) => {
  clearTimeout(saveMessageTimer);
  saveMessage.value = message;
  isSaveError.value = isError;
  saveMessageTimer = setTimeout(() => {
    saveMessage.value = '';
  }, 4000);
};

const loadData = async () => {
  isLoading.value = true;
  loadError.value = '';
  try {
    const [configRes, tablesRes] = await Promise.all([
      apiClient.get(ENDPOINTS.GET_BIZ_CONFIG(bizName)).catch(e => e),
      apiClient.get(ENDPOINTS.TABLES, { params: { biz: bizName } }).catch(e => e)
    ]);

    if (tablesRes.response?.status === 404 || tablesRes instanceof Error) {
      throw new Error(tablesRes.response?.data?.error || "获取物理表列表失败，无法进行配置。");
    }
    allPhysicalTables.value = tablesRes.data || [];

    if (configRes.response?.status !== 404 && !(configRes instanceof Error)) {
      const config = configRes.data;
      formState.is_publicly_searchable = config.is_publicly_searchable ?? false;
      formState.searchable_tables = config.tables ? Object.keys(config.tables) : [];
    }
  } catch (error) {
    loadError.value = error.message || '加载业务组配置时发生未知错误';
    console.error("加载业务组详情失败:", error);
  } finally {
    isLoading.value = false;
  }
};

onMounted(loadData);

const togglePublicSearchable = async () => {
  try {
    await apiClient.put(ENDPOINTS.UPDATE_BIZ_SETTINGS(bizName), {
      is_publicly_searchable: formState.is_publicly_searchable
    });
    showSaveMessage('公开查询设置已更新。');
  } catch (error) {
    formState.is_publicly_searchable = !formState.is_publicly_searchable;
    const msg = error.response?.data?.error || '更新失败';
    showSaveMessage(msg, true);
  }
};

const handleTableSelectionChange = (event, tableName) => {
  const isEnabling = event.target.checked;

  if (isEnabling) {
    updateTableSelection(tableName, true);
  } else {
    event.preventDefault();
    tableToDisable.value = tableName;
    confirmMessage.value = `您确定要禁用数据表 "${tableName}" 吗？\n相关的视图配置将保留，但前端将无法查询此表。`;
    isConfirmModalVisible.value = true;
  }
};

const executeTableDisabling = () => {
  if (tableToDisable.value) {
    updateTableSelection(tableToDisable.value, false);
    tableToDisable.value = null; // 清理状态
  }
};

const updateTableSelection = async (tableName, enable) => {
  const originalTables = [...formState.searchable_tables];
  const newSearchableTables = enable
    ? [...originalTables, tableName]
    : originalTables.filter(t => t !== tableName);

  // 乐观更新UI，让用户立即看到变化
  formState.searchable_tables = newSearchableTables;

  try {
    await apiClient.put(ENDPOINTS.UPDATE_BIZ_TABLES(bizName), {
      searchable_tables: newSearchableTables
    });
    showSaveMessage(`数据表 "${tableName}" 已${enable ? '启用' : '禁用'}。`);
  } catch (error) {
    // 如果API调用失败，则回滚状态
    formState.searchable_tables = originalTables;
    const msg = error.response?.data?.error || '更新数据表列表失败';
    showSaveMessage(msg, true);
  }
};

const openConfigModal = (tableName) => {
  tableToConfigure.value = tableName;
  isUnifiedModalVisible.value = true;
};


const handleModalSave = () => {
  showSaveMessage(`数据表 "${tableToConfigure.value}" 的配置已成功保存。`);
};
</script>

<style scoped>
.biz-detail-container { max-width: 900px; margin: 2rem auto; padding: 2rem; }
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
.table-list { display: flex; flex-direction: column; gap: 0.75rem; }
.table-item { display: flex; align-items: center; justify-content: space-between; padding: 0.75rem 1rem; background-color: #f8f9fa; border: 1px solid #dee2e6; border-radius: 6px; transition: box-shadow 0.2s; }
.table-item:hover { box-shadow: 0 2px 8px rgba(0,0,0,0.06); }
.table-selector { display: flex; align-items: center; gap: 0.75rem; }
.table-selector label { font-weight: 500; cursor: pointer; }
.table-selector input[type="checkbox"] { transform: scale(1.2); cursor: pointer; }
.table-actions { display: flex; gap: 0.75rem; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; padding: 0.5rem 1rem; border-radius: 5px; cursor: pointer; font-weight: 500; transition: all 0.2s; }
.button-tertiary:hover:not(:disabled) { background-color: #e2e6ea; border-color: #b9c1c9; }
.button-tertiary:disabled { opacity: 0.5; cursor: not-allowed; }
.toggle-switch { position: relative; width: 40px; height: 22px; -webkit-appearance: none; appearance: none; background: #ccc; border-radius: 22px; cursor: pointer; transition: background 0.3s; }
.toggle-switch:checked { background: #007bff; }
.toggle-switch::before { content: ''; position: absolute; width: 18px; height: 18px; border-radius: 50%; background: white; top: 2px; left: 2px; transition: 0.3s; }
.toggle-switch:checked::before { transform: translateX(18px); }
</style>