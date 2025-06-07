<!--src/views/admin/AdminBizConfigPage.vue-->
<template>
  <div class="admin-page-container">
    <header class="page-header">
      <router-link to="/dashboard" class="back-link">&laquo; 返回仪表盘</router-link>
      <h1>配置业务组: <span class="biz-name-highlight">{{ bizName }}</span></h1>
    </header>

    <div v-if="isLoading" class="loading-message">正在加载信息...</div>
    <div v-if="loadError" class="error-message">加载初始信息失败: {{ loadError }}</div>

    <div v-if="!isLoading && !loadError">
      <div v-if="saveMessage" :class="isSaveError ? 'error-message' : 'success-message'" class="save-message-box">{{ saveMessage }}</div>

      <div class="form-section">
        <h3 class="section-header">总体设置</h3>
        <div class="setting-item">
          <span class="setting-label">允许公开查询:</span>
          <div class="setting-value">
            <input v-if="isEditing" type="checkbox" v-model="formIsPubliclySearchable" />
            <span v-else :class="formIsPubliclySearchable ? 'text-success' : 'text-danger'">{{ formIsPubliclySearchable ? '是' : '否' }}</span>
            <p class="form-text">允许未经身份验证的用户通过 /api/search 查询此业务组的数据。</p>
          </div>
        </div>
      </div>

      <div class="form-section">
        <h3 class="section-header">可用表、字段与视图配置</h3>
        <div v-if="isEditing">
          <p class="section-description">
            使用复选框勾选所有可用于查询的表。在已勾选的表中，可使用单选框指定一个作为默认查询表，并分别配置其字段和展示视图。
          </p>
          <div v-if="isLoadingPhysicalTables" class="loading-message">正在加载物理表列表...</div>
          <div v-if="physicalTablesInBiz.length === 0 && !isLoadingPhysicalTables" class="info-message">未在此业务组下扫描到任何物理表。</div>
          <div class="table-config-list">
            <div v-for="tableName in physicalTablesInBiz" :key="tableName" class="table-config-item">
              <div class="table-selection">
                <input type="checkbox" :id="`chk-${tableName}`" :value="tableName" v-model="formSelectedSearchableTables" class="checkbox-enable" />
                <label :for="`chk-${tableName}`" class="table-name-label">{{ tableName }}</label>
              </div>
              <div class="table-actions">
                <label class="radio-default-label" :class="{ 'disabled': !formSelectedSearchableTables.includes(tableName) }">
                  <input type="radio" name="default-table-radio" :value="tableName" v-model="formDefaultQueryTable" :disabled="!formSelectedSearchableTables.includes(tableName)" class="radio-default" />
                  默认
                </label>
                <button v-if="formSelectedSearchableTables.includes(tableName)" @click.prevent="openFieldConfigModal(tableName)" type="button" class="config-fields-button">
                  配置字段
                  <span v-if="fieldConfigData[tableName] && fieldConfigData[tableName].length > 0" class="configured-badge" title="已配置">✓</span>
                </button>
                <button v-if="formSelectedSearchableTables.includes(tableName)" @click.prevent="openViewConfigModal(tableName)" type="button" class="config-views-button">
                  配置视图
                  <span v-if="viewConfigData[tableName] && viewConfigData[tableName].length > 0" class="configured-badge" title="已配置">✓</span>
                </button>
              </div>
            </div>
          </div>
        </div>

        <div v-else>
          <div class="setting-item">
            <span class="setting-label">默认查询表:</span>
            <div class="setting-value">
              <span v-if="formDefaultQueryTable" class="default-badge">{{ formDefaultQueryTable }}</span>
              <span v-else class="text-muted">未设置</span>
            </div>
          </div>
          <div class="setting-item">
            <span class="setting-label">已启用的表:</span>
            <div class="setting-value">
              <p v-if="formSelectedSearchableTables.length === 0" class="text-muted">无</p>
              <div v-else class="view-mode-accordion">
                <div v-for="tableName in formSelectedSearchableTables" :key="`view-${tableName}`" class="accordion-item">
                  <div class="accordion-header" @click="toggleTableExpansion(tableName)">
                    <span class="accordion-title">{{ tableName }}</span>
                    <span class="accordion-icon">{{ expandedTables.has(tableName) ? '−' : '+' }}</span>
                  </div>
                  <div v-if="expandedTables.has(tableName)" class="accordion-content">
                    <h4>字段配置:</h4>
                    <ul class="field-summary-list">
                      <li v-for="field in getFieldsForTable(tableName)" :key="field.field_name" class="field-summary-item">
                        <span class="field-name">{{ field.field_name }}</span>
                        <div class="field-attributes">
                          <span v-if="field.is_searchable" class="attr-badge searchable">可搜索</span>
                          <span v-if="field.is_returnable" class="attr-badge returnable">可返回</span>
                          <span v-if="field.dataType" class="attr-badge datatype">类型: {{ field.dataType }}</span>
                        </div>
                      </li>
                      <li v-if="getFieldsForTable(tableName).length === 0" class="text-muted">此表尚未配置任何字段。</li>
                    </ul>
                    <h4 class="mt-4">视图配置:</h4>
                    <ul class="field-summary-list">
                      <li v-for="view in getViewsForTable(tableName)" :key="view.view_name" class="field-summary-item">
                          <span class="field-name">{{ view.display_name }} ({{ view.view_type }})</span>
                          <div class="field-attributes">
                            <span class="attr-badge view-name" :title="view.view_name">ID: {{ view.view_name }}</span>
                            <span v-if="view.is_default" class="attr-badge default-view">默认视图</span>
                          </div>
                      </li>
                      <li v-if="getViewsForTable(tableName).length === 0" class="text-muted">此表尚未配置任何视图。</li>
                    </ul>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      <div class="form-actions">
        <div v-if="isEditing">
          <button type="button" @click="cancelEdit" class="button-secondary">取消</button>
          <button type="button" @click="saveAllChanges" :disabled="isSaving || !isDirty" class="submit-button">
            <span v-if="isSaving">正在保存...</span>
            <span v-else>保存更改</span>
          </button>
        </div>
        <div v-else>
          <button type="button" @click="enterEditMode" class="submit-button edit-button">编辑配置</button>
        </div>
      </div>
    </div>

    <div v-if="isFieldConfigModalVisible" class="modal-overlay" @click.self="closeFieldConfigModal">
      <div class="modal-content">
        <header class="modal-header">
          <h3>配置字段: <span class="biz-name-highlight">{{ tableToConfigure }}</span></h3>
          <button @click="closeFieldConfigModal" class="close-button">&times;</button>
        </header>
        <div class="modal-body">
          <p class="section-description">此处仅配置字段的基础属性，如是否可用于搜索、是否从数据库返回、以及其数据类型。字段的显示名称和顺序在“配置视图”中定义。</p>
          <div v-if="isLoadingFields" class="loading-message">正在加载物理列...</div>
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
                <tr v-for="(field, index) in currentFieldSettings" :key="field.field_name">
                  <td>{{ field.field_name }}</td>
                  <td><input type="checkbox" v-model="currentFieldSettings[index].is_searchable" /></td>
                  <td><input type="checkbox" v-model="currentFieldSettings[index].is_returnable" /></td>
                  <td>
                    <select v-model="currentFieldSettings[index].dataType">
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
          <button @click="closeFieldConfigModal" class="button-secondary">取消</button>
          <button @click="saveFieldConfigFromModal" class="button-primary">确定</button>
        </footer>
      </div>
    </div>

    <div v-if="isViewConfigModalVisible" class="modal-overlay" @click.self="closeViewConfigModal">
      <div class="modal-content large">
        <header class="modal-header">
          <h3>配置视图: <span class="biz-name-highlight">{{ tableToConfigure }}</span></h3>
          <button @click="closeViewConfigModal" class="close-button">&times;</button>
        </header>
        <div class="modal-body">
          <div class="view-config-container">
            <div class="view-list-panel">
              <h4>已有视图</h4>
              <ul class="view-list">
                <li v-for="(view, index) in viewConfigData[tableToConfigure]" :key="index" @click="editView(index)" :class="{ 'active': currentlyEditingViewIndex === index }">
                  {{ view.display_name || '(未命名视图)' }}
                  <span class="view-type-badge">{{ view.view_type }}</span>
                </li>
              </ul>
              <button @click="addNewView" class="button-full-width">+ 添加新视图</button>
            </div>
            <div class="view-editor-panel">
              <div v-if="currentlyEditingView">
                <h4>编辑视图</h4>
                <div class="form-grid">
                  <div class="form-group">
                    <label>视图显示名</label>
                    <input type="text" v-model="currentlyEditingView.display_name" placeholder="例如：用户卡片列表" />
                  </div>
                  <div class="form-group">
                    <label>视图ID (唯一)</label>
                    <input type="text" v-model="currentlyEditingView.view_name" placeholder="例如：user_cards_view" />
                  </div>
                  <div class="form-group full-width">
                     <label class="checkbox-label"><input type="checkbox" v-model="currentlyEditingView.is_default" @change="handleDefaultViewChange" /> 设为该表的默认视图</label>
                  </div>
                  <div class="form-group">
                    <label>视图类型</label>
                    <select v-model="currentlyEditingView.view_type">
                      <option disabled value="">请选择类型</option>
                      <option value="cards">卡片 (Cards)</option>
                      <option value="table">表格 (Table)</option>
                    </select>
                  </div>
                </div>
                <div class="binding-section">
                  <h5>字段绑定</h5>
                  <div v-if="currentlyEditingView.view_type === 'cards'" class="form-grid">
                      <div class="form-group" v-for="key in Object.keys(currentlyEditingView.binding.card)" :key="key">
                          <label>{{ cardFieldLabels[key] }}</label>
                          <select v-model="currentlyEditingView.binding.card[key]">
                              <option value="">-- 无 --</option>
                              <option v-for="field in physicalFieldsForTable[tableToConfigure]" :key="field" :value="field">{{ field }}</option>
                          </select>
                      </div>
                  </div>
                  <div v-if="currentlyEditingView.view_type === 'table'">
                      <p class="section-description">选择要在表格中显示的列，为它们指定显示名称，并可拖拽行来调整列的顺序。</p>
                      <div
                        v-for="(col, index) in currentlyEditingView.binding.table.columns"
                        :key="index"
                        class="table-column-config-row"
                        :draggable="true"
                        @dragstart="handleDragStart(index)"
                        @dragover.prevent
                        @drop="handleDrop(index)"
                        :class="{'dragging': draggingIndex === index}"
                      >
                          <span class="drag-handle">☰</span>
                          <select v-model="col.field">
                             <option value="">-- 选择字段 --</option>
                             <option v-for="f in physicalFieldsForTable[tableToConfigure]" :key="f" :value="f">{{ f }}</option>
                          </select>
                          <input type="text" v-model="col.displayName" placeholder="列显示名" />
                          <button @click="removeTableColumn(index)" class="button-danger-small">&times;</button>
                      </div>
                      <button @click="addTableColumn" class="button-secondary-small">+ 添加一列</button>
                  </div>
                </div>
                <div class="editor-actions">
                  <button @click="deleteView(currentlyEditingViewIndex)" class="button-danger">删除此视图</button>
                  <button @click="saveViewConfigFromModal" class="button-primary">确认更改</button>
                </div>
              </div>
              <div v-else class="placeholder-text">
                请从左侧选择一个视图进行编辑，或添加一个新视图。
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

  </div>
</template>

<script setup>
import { defineProps, onMounted, ref, watch, reactive } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import { cloneDeep } from 'lodash';

const props = defineProps({
  bizName: {
    type: String,
    required: true,
  },
});

const isEditing = ref(false);
const isDirty = ref(false);
const initialFormState = ref(null);
const isLoading = ref(true);
const loadError = ref('');
const isNewConfiguration = ref(false);
const currentConfig = ref(null);
const physicalTablesInBiz = ref([]);
const physicalFieldsForTable = ref({});
const isLoadingPhysicalTables = ref(false);
const isLoadingFields = ref(false);
const formIsPubliclySearchable = ref(true);
const formDefaultQueryTable = ref('');
const formSelectedSearchableTables = ref([]);
const fieldConfigData = ref({});
const isFieldConfigModalVisible = ref(false);
const tableToConfigure = ref('');
const currentFieldSettings = ref([]);
const isSaving = ref(false);
const saveMessage = ref('');
const isSaveError = ref(false);
const expandedTables = ref(new Set());
const viewConfigData = ref({});
const isViewConfigModalVisible = ref(false);
const currentlyEditingView = ref(null);
const currentlyEditingViewIndex = ref(null);
const cardFieldLabels = { title: '标题字段', subtitle: '副标题字段', description: '描述字段', imageUrl: '图片URL字段', tag: '标签字段' };
const draggingIndex = ref(null);

const createFormSnapshot = () => {
  return JSON.stringify({
    isPubliclySearchable: formIsPubliclySearchable.value,
    defaultQueryTable: formDefaultQueryTable.value,
    selectedSearchableTables: formSelectedSearchableTables.value.slice().sort(),
    fieldConfig: fieldConfigData.value,
    viewConfig: viewConfigData.value,
  });
};

const initializeFormData = () => {
  const cfg = currentConfig.value;
  isNewConfiguration.value = !cfg || !cfg.biz_name;

  if (!isNewConfiguration.value) {
    formIsPubliclySearchable.value = cfg.is_publicly_searchable;
    formDefaultQueryTable.value = cfg.default_query_table || '';
    formSelectedSearchableTables.value = cfg.tables ? Object.keys(cfg.tables) : [];
    if (cfg.tables) {
      fieldConfigData.value = {};
      for (const tableName in cfg.tables) {
        fieldConfigData.value[tableName] = Object.values(cfg.tables[tableName].fields || {});
      }
    }
  } else {
    formIsPubliclySearchable.value = true;
    formDefaultQueryTable.value = '';
    formSelectedSearchableTables.value = [];
    fieldConfigData.value = {};
  }
};

const fetchBizConfiguration = async () => {
  try {
    const response = await apiClient.get(ENDPOINTS.GET_BIZ_CONFIG(props.bizName));
    currentConfig.value = response.data;
    isNewConfiguration.value = false;
    initializeFormData();
  } catch (error) {
    if (error.response && error.response.status === 404) {
      isNewConfiguration.value = true;
      currentConfig.value = {};
      initializeFormData();
    } else {
      loadError.value = error.response?.data?.error || error.message;
    }
  }
};

const fetchPhysicalTables = async () => {
  isLoadingPhysicalTables.value = true;
  try {
    const response = await apiClient.get(ENDPOINTS.TABLES + `?biz=${props.bizName}`);
    physicalTablesInBiz.value = response.data || [];
    physicalTablesInBiz.value.sort();
  } catch (error) {
    if (!loadError.value) loadError.value = `获取物理表列表失败: ${error.message}`;
  } finally {
    isLoadingPhysicalTables.value = false;
  }
};

const fetchViewConfigs = async () => {
  try {
    const response = await apiClient.get(ENDPOINTS.GET_BIZ_VIEWS(props.bizName));
    viewConfigData.value = response.data || {};
  } catch (error) {
     if (error.response && error.response.status !== 404) {
        if (!loadError.value) loadError.value = `获取视图配置失败: ${error.message}`;
     }
     viewConfigData.value = {};
  }
};

const openFieldConfigModal = async (tableName) => {
  tableToConfigure.value = tableName;
  isLoadingFields.value = true;
  isFieldConfigModalVisible.value = true;
  if (!physicalFieldsForTable.value[tableName]) {
    try {
      const response = await apiClient.get(ENDPOINTS.GET_PHYSICAL_COLUMNS(props.bizName, tableName));
      physicalFieldsForTable.value[tableName] = response.data || [];
    } catch (error) {
      loadError.value = `获取表'${tableName}'的物理列失败: ` + error.message;
      closeFieldConfigModal();
      return;
    }
  }
  const physicalFields = physicalFieldsForTable.value[tableName];
  const existingSettings = fieldConfigData.value[tableName] || [];
  const settingsMap = new Map(existingSettings.map(s => [s.field_name, s]));
  currentFieldSettings.value = physicalFields.map(fieldName => {
    const savedSetting = settingsMap.get(fieldName);
    return {
      field_name: fieldName,
      is_searchable: savedSetting?.is_searchable ?? false,
      is_returnable: savedSetting?.is_returnable ?? true,
      dataType: savedSetting?.dataType ?? 'string',
    };
  });
  isLoadingFields.value = false;
};

const closeFieldConfigModal = () => {
  isFieldConfigModalVisible.value = false;
  tableToConfigure.value = '';
  currentFieldSettings.value = [];
};

const saveFieldConfigFromModal = () => {
  fieldConfigData.value[tableToConfigure.value] = cloneDeep(currentFieldSettings.value);
  closeFieldConfigModal();
};

const openViewConfigModal = async (tableName) => {
  tableToConfigure.value = tableName;
  if (!viewConfigData.value[tableName]) {
    viewConfigData.value[tableName] = [];
  }
  isViewConfigModalVisible.value = true;
  if (!physicalFieldsForTable.value[tableName]) {
    isLoadingFields.value = true;
    try {
      const response = await apiClient.get(ENDPOINTS.GET_PHYSICAL_COLUMNS(props.bizName, tableName));
      physicalFieldsForTable.value[tableName] = response.data || [];
    } catch (error) {
       loadError.value = `获取表'${tableName}'的物理列失败: ` + error.message;
       closeViewConfigModal();

    } finally {
        isLoadingFields.value = false;
    }
  }
};

const closeViewConfigModal = () => {
  isViewConfigModalVisible.value = false;
  currentlyEditingView.value = null;
  currentlyEditingViewIndex.value = null;
};

const addNewView = () => {
  currentlyEditingViewIndex.value = -1;
  currentlyEditingView.value = reactive({
    view_name: '',
    view_type: '',
    display_name: '',
    is_default: false,
    binding: {
      card: { title: '', subtitle: '', description: '', imageUrl: '', tag: '' },
      table: { columns: [] },
    },
  });
};

const editView = (index) => {
  currentlyEditingViewIndex.value = index;
  currentlyEditingView.value = reactive(cloneDeep(viewConfigData.value[tableToConfigure.value][index]));
};

const saveViewConfigFromModal = () => {
  if (!currentlyEditingView.value || !currentlyEditingView.value.view_name) {
    alert('视图ID不能为空');
    return;
  }
  const viewToSave = cloneDeep(currentlyEditingView.value);
  if (currentlyEditingViewIndex.value === -1) {
    viewConfigData.value[tableToConfigure.value].push(viewToSave);
  } else {
    viewConfigData.value[tableToConfigure.value][currentlyEditingViewIndex.value] = viewToSave;
  }
  currentlyEditingView.value = null;
  currentlyEditingViewIndex.value = null;
};

const deleteView = (index) => {
  if (confirm('确定要删除这个视图吗？')) {
    viewConfigData.value[tableToConfigure.value].splice(index, 1);
    currentlyEditingView.value = null;
    currentlyEditingViewIndex.value = null;
  }
}

const handleDefaultViewChange = () => {
  if (currentlyEditingView.value.is_default) {
    viewConfigData.value[tableToConfigure.value].forEach((view, index) => {
      if (index !== currentlyEditingViewIndex.value) {
        view.is_default = false;
      }
    });
  }
}

const addTableColumn = () => {
    if (currentlyEditingView.value && currentlyEditingView.value.binding.table) {
        currentlyEditingView.value.binding.table.columns.push({ field: '', displayName: ''});
    }
}

const removeTableColumn = (index) => {
    if (currentlyEditingView.value && currentlyEditingView.value.binding.table) {
        currentlyEditingView.value.binding.table.columns.splice(index, 1);
    }
}

const handleDragStart = (index) => {
  draggingIndex.value = index;
};

const handleDragOver = (event) => {
  event.preventDefault();
};

const handleDrop = (targetIndex) => {
  if (draggingIndex.value === null || draggingIndex.value === targetIndex) {
    draggingIndex.value = null;
    return;
  }

  const columns = currentlyEditingView.value.binding.table.columns;
  const draggedItem = columns.splice(draggingIndex.value, 1)[0];
  columns.splice(targetIndex, 0, draggedItem);

  draggingIndex.value = null;
};

const enterEditMode = () => {
  initialFormState.value = createFormSnapshot();
  isDirty.value = false;
  isEditing.value = true;
  saveMessage.value = '';
};

const cancelEdit = () => {
  initializeFormData();
  isEditing.value = false;
  isDirty.value = false;
  saveMessage.value = '';
};

const saveAllChanges = async () => {
  isSaving.value = true;
  saveMessage.value = '';
  isSaveError.value = false;
  try {
    await apiClient.put(ENDPOINTS.UPDATE_BIZ_SETTINGS(props.bizName), {
      is_publicly_searchable: formIsPubliclySearchable.value,
      default_query_table: formDefaultQueryTable.value || null,
    });
    await apiClient.put(ENDPOINTS.UPDATE_BIZ_TABLES(props.bizName), {
      searchable_tables: formSelectedSearchableTables.value,
    });
    for (const tableName of formSelectedSearchableTables.value) {
      if (fieldConfigData.value[tableName]) {
        const payload = fieldConfigData.value[tableName].map(f => ({
          field_name: f.field_name,
          is_searchable: f.is_searchable,
          is_returnable: f.is_returnable,
          dataType: f.dataType,
        }));
        await apiClient.put(ENDPOINTS.UPDATE_TABLE_FIELDS(props.bizName, tableName), payload);
      }
    }
    await apiClient.put(ENDPOINTS.UPDATE_BIZ_VIEWS(props.bizName), viewConfigData.value);

    saveMessage.value = '业务组配置已成功保存！';
    isSaveError.value = false;
    await Promise.all([fetchBizConfiguration(), fetchViewConfigs()]);
    isEditing.value = false;
    isDirty.value = false;
  } catch (error) {
    isSaveError.value = true;
    saveMessage.value = `保存失败: ${error.response?.data?.error || error.message}`;
  } finally {
    isSaving.value = false;
  }
};

onMounted(async () => {
  isLoading.value = true;
  await Promise.all([fetchPhysicalTables(), fetchBizConfiguration(), fetchViewConfigs()]);
  if (isNewConfiguration.value) {
    enterEditMode();
  }
  isLoading.value = false;
});

watch(formSelectedSearchableTables, (newSelectedTables) => {
  if (formDefaultQueryTable.value && !newSelectedTables.includes(formDefaultQueryTable.value)) {
    formDefaultQueryTable.value = '';
  }
}, { deep: true });

watch([formIsPubliclySearchable, formDefaultQueryTable, formSelectedSearchableTables, fieldConfigData, viewConfigData], () => {
  if (isEditing.value) {
    isDirty.value = createFormSnapshot() !== initialFormState.value;
  }
}, { deep: true });

const toggleTableExpansion = (tableName) => {
  if (expandedTables.value.has(tableName)) {
    expandedTables.value.delete(tableName);
  } else {
    expandedTables.value.add(tableName);
  }
};

const getFieldsForTable = (tableName) => {
  return fieldConfigData.value[tableName] || [];
};

const getViewsForTable = (tableName) => {
  return viewConfigData.value[tableName] || [];
}
</script>

<style scoped>
.admin-page-container { padding: 2rem; max-width: 900px; margin: 2rem auto; background-color: #ffffff; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.08); font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif; }
.page-header { margin-bottom: 2rem; padding-bottom: 1rem; border-bottom: 1px solid #e9ecef; }
.back-link { display: inline-block; margin-bottom: 0.75rem; color: #007bff; text-decoration: none; font-size: 0.95em; }
.page-header h1 { font-size: 1.8em; color: #2c3e50; margin: 0; font-weight: 600; }
.biz-name-highlight { color: #007bff; }
.loading-message, .error-message, .info-message, .success-message { padding: 1rem 1.25rem; margin-bottom: 1.5rem; border-radius: 5px; border: 1px solid; }
.save-message-box { transition: opacity 0.3s ease; }
.error-message { background-color: #f8d7da; border-color: #f5c6cb; color: #721c24; }
.success-message { background-color: #d4edda; border-color: #c3e6cb; color: #155724; }
.info-message { background-color: #d1ecf1; border-color: #bee5eb; color: #0c5460; }
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
.text-muted { color: #6c757d; }
.table-config-list { display: flex; flex-direction: column; gap: 0.75rem; }
.table-config-item { display: flex; align-items: center; justify-content: space-between; padding: 0.75rem 1.25rem; background-color: #fff; border: 1px solid #dee2e6; border-radius: 6px; transition: box-shadow 0.2s ease; }
.table-config-item:hover { box-shadow: 0 2px 8px rgba(0,0,0,0.06); }
.table-selection { display: flex; align-items: center; gap: 0.75rem; }
.checkbox-enable { transform: scale(1.3); cursor: pointer; }
.table-name-label { font-weight: 500; color: #343a40; cursor: pointer; }
.table-actions { display: flex; align-items: center; gap: 1.5rem; }
.radio-default-label { display: flex; align-items: center; gap: 0.4rem; font-size: 0.9em; color: #495057; cursor: pointer; padding: 0.25rem 0.5rem; border-radius: 4px; transition: background-color 0.2s; }
.radio-default-label.disabled { color: #adb5bd; cursor: not-allowed; }
.radio-default-label:not(.disabled):hover { background-color: #e9ecef; }
.radio-default { transform: scale(1.2); cursor: pointer; }
.config-fields-button { padding: 0.4rem 0.8rem; font-size: 0.85em; background-color: #6c757d; color: white; border: none; border-radius: 4px; cursor: pointer; display: inline-flex; align-items: center; gap: 0.5rem; }
.config-views-button { padding: 0.4rem 0.8rem; font-size: 0.85em; background-color: #17a2b8; color: white; border: none; border-radius: 4px; cursor: pointer; display: inline-flex; align-items: center; gap: 0.5rem; }
.configured-badge { background-color: #28a745; color: white; width: 16px; height: 16px; border-radius: 50%; display: inline-flex; align-items: center; justify-content: center; font-size: 0.7em; font-weight: bold; }
.form-actions { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 1rem; }
.submit-button { padding: 0.75rem 1.5rem; background-color: #28a745; color: white; border: none; border-radius: 5px; cursor: pointer; font-size: 1.05em; font-weight: 500; }
.submit-button:disabled { background-color: #a3d9a5; cursor: not-allowed; }
.edit-button { background-color: #007bff; }
.edit-button:hover:not(:disabled) { background-color: #0069d9; }
.button-secondary { background-color: #6c757d; color: white; padding: 0.75rem 1.5rem; border-radius: 5px; border: none; cursor: pointer; }
.default-badge { background-color: #007bff; color: white; font-size: 0.9em; padding: 0.3rem 0.8rem; border-radius: 12px; font-weight: 500; }

.view-mode-accordion { width: 100%; }
.accordion-item { border: 1px solid #dee2e6; border-radius: 6px; margin-bottom: 0.75rem; background-color: #fff; overflow: hidden; }
.accordion-header { display: flex; justify-content: space-between; align-items: center; padding: 0.75rem 1.25rem; cursor: pointer; transition: background-color 0.2s ease; }
.accordion-header:hover { background-color: #f8f9fa; }
.accordion-title { font-weight: 500; color: #343a40; }
.accordion-icon { font-weight: bold; font-size: 1.2em; color: #6c757d; }
.accordion-content { padding: 1rem 1.5rem; border-top: 1px solid #e9ecef; }
.field-summary-list { list-style-type: none; padding: 0; margin: 0; }
.field-summary-item { display: flex; align-items: center; justify-content: space-between; padding: 0.5rem 0; border-bottom: 1px solid #f1f3f5; }
.field-summary-item:last-child { border-bottom: none; }
.field-name { font-family: 'Courier New', Courier, monospace; color: #212529; }
.field-attributes { display: flex; flex-wrap: wrap; gap: 0.5rem; }
.attr-badge { font-size: 0.75em; padding: 0.2em 0.6em; border-radius: 10px; font-weight: 500; color: #fff; }
.attr-badge.searchable { background-color: #20c997; }
.attr-badge.returnable { background-color: #17a2b8; }
.attr-badge.alias { background-color: #fd7e14; }
.attr-badge.order { background-color: #6f42c1; }
.attr-badge.view-name { background-color: #ffc107; color: #212529; }
.attr-badge.default-view { background-color: #28a745; }
.mt-4 { margin-top: 1.5rem; }

.modal-overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background-color: rgba(0, 0, 0, 0.6); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-content { background-color: #fff; border-radius: 8px; box-shadow: 0 5px 15px rgba(0,0,0,0.3); width: 90%; max-width: 800px; display: flex; flex-direction: column; max-height: 90vh; }
.modal-content.large { max-width: 1000px; }
.modal-header { padding: 1.5rem; border-bottom: 1px solid #e9ecef; display: flex; justify-content: space-between; align-items: center; }
.modal-header h3 { margin: 0; font-size: 1.5em; }
.close-button { background: none; border: none; font-size: 2rem; cursor: pointer; color: #6c757d; line-height: 1; padding: 0; }
.modal-body { padding: 1.5rem; overflow-y: auto; }
.modal-footer { padding: 1rem 1.5rem; border-top: 1px solid #e9ecef; text-align: right; }
.button-primary { background-color: #007bff; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.fields-table { width: 100%; border-collapse: collapse; }
.fields-table th, .fields-table td { padding: 0.75rem; text-align: left; border-bottom: 1px solid #dee2e6; vertical-align: middle; }
.fields-table th { background-color: #f8f9fa; font-weight: 600; }
.fields-table input[type="text"], .fields-table input[type="number"] { width: 100%; max-width: 150px; padding: 0.4rem; border: 1px solid #ced4da; border-radius: 4px; box-sizing: border-box; }
.fields-table input[type="checkbox"] { transform: scale(1.2); }

.view-config-container { display: flex; gap: 1.5rem; min-height: 50vh; }
.view-list-panel { width: 250px; flex-shrink: 0; border-right: 1px solid #e9ecef; padding-right: 1.5rem; }
.view-list-panel h4 { margin-top: 0; }
.view-list { list-style: none; padding: 0; margin: 0 0 1rem 0; }
.view-list li { padding: 0.75rem 1rem; margin-bottom: 0.5rem; border-radius: 5px; cursor: pointer; transition: background-color 0.2s ease; border: 1px solid transparent; display: flex; justify-content: space-between; align-items: center; }
.view-list li:hover { background-color: #f8f9fa; }
.view-list li.active { background-color: #e7f3ff; border-color: #007bff; font-weight: 500; }
.view-type-badge { font-size: 0.7em; background-color: #6c757d; color: white; padding: 0.2rem 0.5rem; border-radius: 8px; }
.button-full-width { width: 100%; padding: 0.6rem; background-color: #e9ecef; border: 1px dashed #ced4da; cursor: pointer; }
.view-editor-panel { flex-grow: 1; }
.view-editor-panel h4 { margin-top: 0; }
.placeholder-text { text-align: center; color: #6c757d; padding-top: 4rem; }
.form-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; margin-bottom: 1.5rem; }
.form-group.full-width { grid-column: 1 / -1; }
.form-group { display: flex; flex-direction: column; }
.form-group label { margin-bottom: 0.5rem; font-weight: 500; font-size: 0.9em; }
.form-group input[type="text"], .form-group select { padding: 0.6rem; border: 1px solid #ced4da; border-radius: 4px; width: 100%; }
.checkbox-label { display: flex; align-items: center; gap: 0.5rem; }
.binding-section { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; }
.editor-actions { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: space-between; }
.button-danger { background-color: #dc3545; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: none; cursor: pointer; }
.table-column-config-row { display: flex; gap: 0.5rem; align-items: center; margin-bottom: 0.5rem; }
.button-danger-small, .button-secondary-small { padding: 0.2rem 0.5rem; font-size: 0.9em; border: 1px solid #ced4da; background-color: #fff; cursor: pointer; }
.button-danger-small { color: #dc3545; }
.attr-badge.datatype { background-color: #6610f2; }
.table-column-config-row {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  margin-bottom: 0.5rem;
  padding: 0.5rem;
  border: 1px solid #e9ecef;
  border-radius: 4px;
  background-color: #fff;
}
.drag-handle {
  cursor: grab;
  color: #adb5bd;
  padding: 0 0.5rem;
}
.table-column-config-row.dragging {
  opacity: 0.5;
  background: #d4edda;
  border-style: dashed;
}
</style>