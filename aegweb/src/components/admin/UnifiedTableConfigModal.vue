<template>
  <div v-if="visible" class="modal-overlay" @click.self="close">
    <div class="modal-content">
      <header class="modal-header">
        <h3>配置表: <span class="table-name-highlight">{{ tableName }}</span></h3>
        <button @click="close" class="close-button" aria-label="关闭">&times;</button>
      </header>

      <main class="modal-body">
        <div class="tabs-nav">
          <button :class="{ active: activeTab === 'fields' }" @click="activeTab = 'fields'">步骤1: 字段属性</button>
          <button :class="{ active: activeTab === 'view' }" @click="activeTab = 'view'">步骤2: 视图布局</button>
        </div>

        <div v-if="isLoading" class="status-message loading">加载中...</div>
        <div v-if="loadError" class="status-message error">{{ loadError }}</div>

        <div v-if="!isLoading && !loadError">
          <div v-show="activeTab === 'fields'" class="tab-panel">
            <p class="section-description">为所有物理字段配置基础属性。“数据类型”已根据字段名智能设定，仅在需要时修改。</p>
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
                <tr v-for="field in fieldSettings" :key="field.field_name">
                  <td><strong>{{ field.field_name }}</strong></td>
                  <td><input type="checkbox" v-model="field.is_searchable" /></td>
                  <td><input type="checkbox" v-model="field.is_returnable" /></td>
                  <td>
                    <select v-model="field.dataType">
                      <option value="string">文本 (string)</option>
                      <option value="number">数字 (number)</option>
                      <option value="date">日期 (date)</option>
                    </select>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>

          <div v-show="activeTab === 'view'" class="tab-panel">
            <form class="form-grid" @submit.prevent>
              <div class="form-block">
                <label for="viewDisplayName">视图显示名</label>
                <input id="viewDisplayName" v-model.trim="viewConfig.display_name" placeholder="如：用户列表" />
              </div>
              <div class="form-block">
                <label for="viewId">视图ID (英文/数字/下划线)</label>
                <input id="viewId" v-model.trim="viewConfig.view_name" placeholder="如：user_list" />
              </div>
              <div class="form-block">
                <label for="viewType">视图类型</label>
                <select id="viewType" v-model="viewConfig.view_type">
                  <option value="cards">卡片 (Cards)</option>
                  <option value="table">表格 (Table)</option>
                </select>
              </div>
            </form>
            <div class="binding-section">
              <h4 class="binding-header">字段绑定</h4>
              <div v-if="returnableFields.length === 0" class="status-message info">请先在“步骤1”中勾选至少一个“可返回”的字段</div>

              <div v-if="viewConfig.view_type === 'cards' && viewConfig.binding" class="bind-fields-grid">
                <div class="form-block" v-for="key in Object.keys(viewConfig.binding.card)" :key="key">
                  <label>{{ cardFieldLabels[key] }}</label>
                  <select v-model="viewConfig.binding.card[key]">
                    <option value="">— 请选择字段 —</option>
                    <option v-for="field in returnableFields" :key="field" :value="field">{{ field }}</option>
                  </select>
                </div>
              </div>

              <div v-if="viewConfig.view_type === 'table' && viewConfig.binding" class="table-fields-list">
                <div v-for="(col, index) in viewConfig.binding.table.columns" :key="index" class="column-row">
                  <select v-model="col.field">
                    <option value="">— 选择字段 —</option>
                    <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
                  </select>
                  <input v-model.trim="col.displayName" class="column-input" placeholder="列显示名 (可选)" />
                  <button @click="removeTableColumn(index)" class="btn-icon danger">&times;</button>
                </div>
                <button @click="addTableColumn" class="button-tertiary">添加一列</button>
              </div>
            </div>
          </div>
        </div>
      </main>

      <footer class="modal-footer">
        <button @click="close" class="button-secondary">取消</button>
        <button @click="save" class="button-primary" :disabled="isLoading">保存配置</button>
      </footer>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch } from 'vue';
import apiClient from '../../services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints.js';

const props = defineProps({
  visible: { type: Boolean, required: true },
  bizName: { type: String, required: true },
  tableName: { type: String, required: true },
});

const emit = defineEmits(['update:visible', 'saved']);

const activeTab = ref('fields');
const isLoading = ref(false);
const loadError = ref(null);

const fieldSettings = ref([]);
const viewConfig = reactive({
  view_name: '',
  view_type: 'cards',
  display_name: '',
  is_default: true,
  binding: {
    card: { title: '', subtitle: '', description: '', imageUrl: '', tag: '' },
    table: { columns: [] }
  }
});
let allBizViews = {};

const cardFieldLabels = {
  title: '卡片标题 (Title)',
  subtitle: '卡片副标题 (Subtitle)',
  description: '卡片描述 (Description)',
  imageUrl: '图片 URL (Image URL)',
  tag: '标签 (Tag)'
};

const returnableFields = computed(() => {
  return fieldSettings.value
    .filter(f => f.is_returnable)
    .map(f => f.field_name);
});

const close = () => { emit('update:visible', false); };

const createDefaultView = (tableName) => ({
  view_name: `${tableName}_default_view`,
  view_type: 'cards',
  display_name: `${tableName} 默认视图`,
  is_default: true,
  binding: {
    card: { title: '', subtitle: '', description: '', imageUrl: '', tag: '' },
    table: { columns: [] }
  }
});

const fetchData = async () => {
  if (!props.visible) return;
  isLoading.value = true;
  loadError.value = null;

  try {
    const [physColsRes, bizConfigRes, bizViewsRes] = await Promise.all([
      apiClient.get(ENDPOINTS.GET_PHYSICAL_COLUMNS(props.bizName, props.tableName)),
      apiClient.get(ENDPOINTS.GET_BIZ_CONFIG(props.bizName)).catch(e => e),
      apiClient.get(ENDPOINTS.GET_BIZ_VIEWS(props.bizName)).catch(e => e)
    ]);

    if (physColsRes.response || physColsRes instanceof Error) {
      throw new Error(physColsRes.response?.data?.error || `无法获取表 '${props.tableName}' 的物理列`);
    }
    const physicalColumns = physColsRes.data;

    const bizConfig = (bizConfigRes.response?.status === 404 || bizConfigRes instanceof Error) ? {} : bizConfigRes.data;
    allBizViews = (bizViewsRes.response?.status === 404 || bizViewsRes instanceof Error) ? {} : bizViewsRes.data;

    const existingTableConfig = bizConfig.tables?.[props.tableName];
    const existingFields = existingTableConfig?.fields || {};

    const suggestDataType = (fieldName) => {
        const lowerName = fieldName.toLowerCase();
        if (lowerName.includes('date') || lowerName.includes('time') || lowerName.endsWith('_at')) return 'date';
        if (lowerName.includes('id') || lowerName.includes('num') || lowerName.includes('count') || lowerName.includes('size')) return 'number';
        return 'string';
    };

    fieldSettings.value = physicalColumns.map(name => ({
      field_name: name,
      is_searchable: existingFields[name]?.is_searchable || false,
      is_returnable: existingFields[name]?.is_returnable || false,
      dataType: existingFields[name]?.dataType || suggestDataType(name),
    }));

    const tableViews = allBizViews[props.tableName] || [];
    let targetView = tableViews.find(v => v.is_default) || tableViews[0] || createDefaultView(props.tableName);
    Object.assign(viewConfig, targetView);
    if (!viewConfig.binding) viewConfig.binding = { card: {}, table: { columns: [] } };
    if (!viewConfig.binding.card) viewConfig.binding.card = { title: '', subtitle: '', description: '', imageUrl: '', tag: '' };
    if (!viewConfig.binding.table) viewConfig.binding.table = { columns: [] };

  } catch (error) {
    console.error('Failed to load table configuration:', error);
    loadError.value = `加载配置失败: ${error.message}`;
  } finally {
    isLoading.value = false;
  }
};

watch(() => props.visible, (newVal) => { if (newVal) fetchData(); }, { immediate: true });

const addTableColumn = () => {
  if (!viewConfig.binding.table.columns) viewConfig.binding.table.columns = [];
  viewConfig.binding.table.columns.push({ field: '', displayName: '' });
};

const removeTableColumn = (index) => {
  viewConfig.binding.table.columns.splice(index, 1);
};

const save = async () => {
  isLoading.value = true;
  try {
    // 准备字段和视图的数据 payload (这部分逻辑不变)
    const fieldsPayload = fieldSettings.value;

    const tableViews = allBizViews[props.tableName] || [];
    const viewIndex = tableViews.findIndex(v => v.view_name === viewConfig.view_name);
    const viewConfigClean = JSON.parse(JSON.stringify(viewConfig));
    if (viewIndex > -1) {
      tableViews[viewIndex] = viewConfigClean;
    } else {
      tableViews.push(viewConfigClean);
    }
    allBizViews[props.tableName] = tableViews;


    await apiClient.put(
      ENDPOINTS.UPDATE_TABLE_FIELDS(props.bizName, props.tableName),
      fieldsPayload
    );

    await apiClient.put(
      ENDPOINTS.UPDATE_BIZ_VIEWS(props.bizName),
      allBizViews
    );

    emit('saved');
    close();

  } catch (error) {
    console.error('Failed to save configuration:', error);
    alert(`保存失败: ${error.response?.data?.error || error.message}`);
  } finally {
    isLoading.value = false;
  }
};
</script>

<style scoped>
.modal-overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-content { background: #fff; border-radius: 8px; width: 90%; max-width: 800px; display: flex; flex-direction: column; max-height: 90vh; }
.modal-header { padding: 1rem 1.5rem; border-bottom: 1px solid #e9ecef; display: flex; justify-content: space-between; align-items: center; }
.modal-header h3 { margin: 0; }
.table-name-highlight { color: #007bff; }
.close-button { background: none; border: none; font-size: 1.75rem; cursor: pointer; color: #6c757d; }
.modal-body { padding: 0 1.5rem 1.5rem; overflow-y: auto; }
.modal-footer { padding: 1rem 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 0.75rem; }
.button-primary { background-color: #007bff; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.button-primary:disabled { background-color: #a0cfff; cursor: not-allowed; }
.button-secondary { background-color: #6c757d; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; padding: 0.4rem 0.8rem; border-radius: 5px; cursor: pointer; }
.tabs-nav { border-bottom: 2px solid #dee2e6; margin-bottom: 1.5rem; }
.tabs-nav button { padding: 0.8rem 1.5rem; border: none; background: transparent; cursor: pointer; font-size: 1em; font-weight: 500; color: #6c757d; position: relative; bottom: -2px; border-bottom: 2px solid transparent; }
.tabs-nav button.active { color: #007bff; border-bottom-color: #007bff; }
.tab-panel { animation: fadeIn 0.3s ease; }
@keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }
.section-description { font-size: 0.9em; color: #6c757d; margin-bottom: 1rem; }
.fields-table { width: 100%; border-collapse: collapse; }
.fields-table th, .fields-table td { padding: 0.8rem; text-align: left; border-bottom: 1px solid #dee2e6; }
.fields-table th { background-color: #f8f9fa; }
.fields-table input[type="checkbox"] { transform: scale(1.2); }
.fields-table select { padding: 0.5rem; border-radius: 4px; border: 1px solid #ced4da; }
.form-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 1rem; margin-bottom: 1.5rem; }
.form-block { display: flex; flex-direction: column; gap: 0.5rem; }
.binding-section { margin-top: 1.5rem; padding-top: 1.5rem; border-top: 1px dashed #e0e4e9; }
.binding-header { font-size: 1.1em; font-weight: 600; margin-bottom: 1rem; }
.bind-fields-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
.table-fields-list .column-row { display: grid; grid-template-columns: 1fr 1fr auto; gap: 0.5rem; align-items: center; margin-bottom: 0.5rem; }
.column-input { padding: 0.5rem; border: 1px solid #ced4da; border-radius: 4px; }
.btn-icon.danger { color: #dc3545; border: none; background: transparent; cursor: pointer; font-size: 1.5rem; }
.status-message { padding: 1rem; margin-bottom: 1rem; border-radius: 5px; border: 1px solid; text-align: center; }
.status-message.loading { background-color: #e6f7ff; }
.status-message.error { background-color: #fff1f0; }
.status-message.info { background-color: #f8f9fa; }

.modal-overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-content { background: #fff; border-radius: 8px; width: 90%; max-width: 800px; display: flex; flex-direction: column; max-height: 90vh; }
.modal-header { padding: 1rem 1.5rem; border-bottom: 1px solid #e9ecef; display: flex; justify-content: space-between; align-items: center; }
.modal-header h3 { margin: 0; }
.table-name-highlight { color: #007bff; }
.close-button { background: none; border: none; font-size: 1.75rem; cursor: pointer; color: #6c757d; }
.modal-body { padding: 0 1.5rem 1.5rem; overflow-y: auto; }
.modal-footer { padding: 1rem 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 0.75rem; }
.button-primary { background-color: #007bff; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.button-primary:disabled { background-color: #a0cfff; cursor: not-allowed; }
.button-secondary { background-color: #6c757d; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; padding: 0.4rem 0.8rem; border-radius: 5px; cursor: pointer; }

.tabs-nav { border-bottom: 2px solid #dee2e6; margin-bottom: 1.5rem; }
.tabs-nav button { padding: 0.8rem 1.5rem; border: none; background: transparent; cursor: pointer; font-size: 1em; font-weight: 500; color: #6c757d; position: relative; bottom: -2px; border-bottom: 2px solid transparent; }
.tabs-nav button.active { color: #007bff; border-bottom-color: #007bff; }
.tab-panel { animation: fadeIn 0.3s ease; }
@keyframes fadeIn { from { opacity: 0; } to { opacity: 1; } }

.section-description { font-size: 0.9em; color: #6c757d; margin-bottom: 1rem; }
.fields-table { width: 100%; border-collapse: collapse; }
.fields-table th, .fields-table td { padding: 0.8rem; text-align: left; border-bottom: 1px solid #dee2e6; }
.fields-table th { background-color: #f8f9fa; }
.fields-table input[type="checkbox"] { transform: scale(1.2); }
.fields-table select { padding: 0.5rem; border-radius: 4px; border: 1px solid #ced4da; }

.form-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 1rem; margin-bottom: 1.5rem; }
.form-block { display: flex; flex-direction: column; gap: 0.5rem; }
.binding-section { margin-top: 1.5rem; padding-top: 1.5rem; border-top: 1px dashed #e0e4e9; }
.binding-header { font-size: 1.1em; font-weight: 600; margin-bottom: 1rem; }
.bind-fields-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
.table-fields-list .column-row { display: grid; grid-template-columns: 1fr 1fr auto; gap: 0.5rem; align-items: center; margin-bottom: 0.5rem; }
.column-input { padding: 0.5rem; border: 1px solid #ced4da; border-radius: 4px; }
.btn-icon.danger { color: #dc3545; border: none; background: transparent; cursor: pointer; font-size: 1.5rem; }

.status-message { padding: 1rem; margin-bottom: 1rem; border-radius: 5px; border: 1px solid; text-align: center; }
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; }
.status-message.error { background-color: #fff1f0; border-color: #ffa39e; }
.status-message.info { background-color: #f8f9fa; border-color: #dee2e6; }
</style>