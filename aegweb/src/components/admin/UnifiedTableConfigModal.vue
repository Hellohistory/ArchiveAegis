<template>
  <div v-if="visible" class="modal-overlay" @click.self="close">
    <div class="modal-content">
      <header class="modal-header">
        <h3>配置表: <span class="table-name-highlight">{{ tableName }}</span></h3>
        <button @click="close" class="close-button" aria-label="关闭">&times;</button>
      </header>

      <main class="modal-body">
        <div v-if="isLoading" class="status-message loading">加载中...</div>
        <div v-if="loadError" class="status-message error">{{ loadError }}</div>

        <div v-if="!isLoading && !loadError">
          <section class="config-section">
            <h4 class="section-title">1. 视图基本设置</h4>
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
          </section>

          <section class="config-section">
            <h4 class="section-title">2. 字段属性配置</h4>
            <p class="section-description">
              为所有物理字段配置基础属性。“数据类型”已根据字段名智能设定，仅在需要时修改。<br/>
              勾选“可返回”的字段，才能在下方的“视图字段绑定”步骤中使用。
            </p>
            <div class="table-wrapper">
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
          </section>

          <section class="config-section">
            <h4 class="section-title">3. 视图字段绑定</h4>
            <div v-if="returnableFields.length === 0" class="status-message info">
              请先在“2. 字段属性配置”中勾选至少一个“可返回”的字段。
            </div>

            <div v-else>
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
                  <button @click="removeTableColumn(index)" class="btn-icon danger" aria-label="移除此列">&times;</button>
                </div>
                <button @click="addTableColumn" class="button-tertiary">添加一列</button>
              </div>
            </div>
          </section>
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

// 确保在切换视图类型时，binding对象结构正确
const ensureBindingStructure = (viewType) => {
    if (!viewConfig.binding) {
        viewConfig.binding = {};
    }
    if (viewType === 'cards' && !viewConfig.binding.card) {
        viewConfig.binding.card = { title: '', subtitle: '', description: '', imageUrl: '', tag: '' };
    } else if (viewType === 'table' && !viewConfig.binding.table) {
        viewConfig.binding.table = { columns: [] };
    }
};

watch(() => viewConfig.view_type, (newType) => {
    ensureBindingStructure(newType);
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

    // 重置并安全地合并视图配置
    Object.assign(viewConfig, createDefaultView(props.tableName), targetView);
    ensureBindingStructure(viewConfig.view_type);


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
    const fieldsPayload = fieldSettings.value;

    const tableViews = allBizViews[props.tableName] || [];
    const viewIndex = tableViews.findIndex(v => v.view_name === viewConfig.view_name);

    // 创建一个干净的副本用于保存
    const viewConfigClean = JSON.parse(JSON.stringify(viewConfig));

    if (viewConfig.view_type === 'cards') {
        delete viewConfigClean.binding.table;
    } else {
        delete viewConfigClean.binding.card;
    }

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
/* --- 基本模态框样式 --- */
.modal-overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-content { background: #fff; border-radius: 8px; width: 90%; max-width: 800px; display: flex; flex-direction: column; max-height: 90vh; box-shadow: 0 5px 15px rgba(0,0,0,0.3); }
.modal-header { padding: 1rem 1.5rem; border-bottom: 1px solid #e9ecef; display: flex; justify-content: space-between; align-items: center; flex-shrink: 0; }
.modal-header h3 { margin: 0; font-size: 1.25rem; }
.table-name-highlight { color: #007bff; font-weight: bold; }
.close-button { background: none; border: none; font-size: 1.75rem; cursor: pointer; color: #6c757d; line-height: 1; padding: 0.5rem; }
.modal-body { padding: 1rem 1.5rem; overflow-y: auto; }
.modal-footer { padding: 1rem 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 0.75rem; background-color: #f8f9fa; flex-shrink: 0; }

/* --- 按钮样式 --- */
.button-primary { background-color: #007bff; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: 1px solid #007bff; cursor: pointer; transition: background-color 0.2s; }
.button-primary:hover { background-color: #0056b3; }
.button-primary:disabled { background-color: #a0cfff; border-color: #a0cfff; cursor: not-allowed; }
.button-secondary { background-color: #6c757d; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: 1px solid #6c757d; cursor: pointer; transition: background-color 0.2s; }
.button-secondary:hover { background-color: #5a6268; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; padding: 0.4rem 0.8rem; border-radius: 5px; cursor: pointer; transition: background-color 0.2s; }
.button-tertiary:hover { background-color: #e2e6ea; }
.btn-icon.danger { color: #dc3545; border: none; background: transparent; cursor: pointer; font-size: 1.5rem; line-height: 1; }

/* --- 布局与表单元素 --- */
.config-section { margin-bottom: 2.5rem; }
.section-title { font-size: 1.15rem; font-weight: 600; margin-bottom: 0.75rem; border-bottom: 1px solid #e9ecef; padding-bottom: 0.5rem; }
.section-description { font-size: 0.9em; color: #6c757d; margin-bottom: 1rem; line-height: 1.5; }
.form-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 1.25rem; }
.form-block { display: flex; flex-direction: column; gap: 0.5rem; }
.form-block label { font-weight: 500; }
.form-block input, .form-block select, .column-input { width: 100%; padding: 0.6rem; border-radius: 4px; border: 1px solid #ced4da; transition: border-color 0.2s, box-shadow 0.2s; }
.form-block input:focus, .form-block select:focus, .column-input:focus { border-color: #80bdff; outline: 0; box-shadow: 0 0 0 0.2rem rgba(0,123,255,.25); }

/* --- 字段配置表格 --- */
.table-wrapper { max-height: 300px; overflow-y: auto; border: 1px solid #dee2e6; border-radius: 4px; }
.fields-table { width: 100%; border-collapse: collapse; }
.fields-table th, .fields-table td { padding: 0.8rem; text-align: left; border-bottom: 1px solid #dee2e6; }
.fields-table th { background-color: #f8f9fa; position: sticky; top: 0; z-index: 1; }
.fields-table tr:last-child td { border-bottom: none; }
.fields-table input[type="checkbox"] { transform: scale(1.2); cursor: pointer; }

/* --- 字段绑定 --- */
.bind-fields-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 1.25rem; }
.table-fields-list .column-row { display: grid; grid-template-columns: 1fr 1fr auto; gap: 0.75rem; align-items: center; margin-bottom: 0.75rem; }

/* --- 状态消息 --- */
.status-message { padding: 1rem; margin: 1rem 0; border-radius: 5px; border: 1px solid; text-align: center; }
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; color: #004085; }
.status-message.error { background-color: #f8d7da; border-color: #f5c6cb; color: #721c24; }
.status-message.info { background-color: #f8f9fa; border-color: #dee2e6; color: #383d41; }
</style>