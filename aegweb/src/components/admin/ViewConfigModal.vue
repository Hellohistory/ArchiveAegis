<!--src/components/admin/ViewConfigModal.vue-->
<template>
  <div v-if="visible" class="modal-overlay" @click.self="close">
    <div class="modal-content large">
      <header class="modal-header">
        <h3>配置视图: <span class="biz-name-highlight">{{ tableName }}</span></h3>
        <button @click="close" class="close-button">&times;</button>
      </header>
      <div class="modal-body">
        <div class="view-config-container">
          <div class="view-list-panel">
            <h4>已有视图</h4>
            <ul class="view-list">
              <li v-for="(view, index) in localViews" :key="index" @click="selectViewToEdit(index)" :class="{ 'active': editingIndex === index }">
                {{ view.display_name || '(未命名视图)' }}
                <span class="view-type-badge">{{ view.view_type }}</span>
              </li>
            </ul>
            <button @click="addNewView" class="button-full-width">+ 添加新视图</button>
          </div>
          <div class="view-editor-panel">
            <div v-if="currentView">
              <h4>{{ editingIndex === -1 ? '创建新视图' : '编辑视图' }}</h4>
              <div class="form-grid">
                <div class="form-group"><label>视图显示名</label><input type="text" v-model="currentView.display_name" /></div>
                <div class="form-group"><label>视图ID (英文唯一)</label><input type="text" v-model="currentView.view_name" :disabled="editingIndex !== -1" /></div>
                <div class="form-group full-width"><label class="checkbox-label"><input type="checkbox" v-model="currentView.is_default" @change="handleDefaultChange" /> 设为默认视图</label></div>
                <div class="form-group">
                  <label>视图类型</label>
                  <select v-model="currentView.view_type">
                    <option value="cards">卡片 (Cards)</option>
                    <option value="table">表格 (Table)</option>
                  </select>
                </div>
              </div>
              <div class="binding-section">
                <h5>字段绑定</h5>
                <div v-if="currentView.view_type === 'cards'" class="form-grid">
                    <div class="form-group" v-for="key in Object.keys(currentView.binding.card)" :key="key">
                        <label>{{ cardFieldLabels[key] }}</label>
                        <select v-model="currentView.binding.card[key]">
                            <option value="">-- 无 --</option>
                            <option v-for="field in availableFields" :key="field" :value="field">{{ field }}</option>
                        </select>
                    </div>
                </div>
                <div v-if="currentView.view_type === 'table'">
                    <p class="section-description">拖拽 ☰ 调整列顺序。</p>
                    <div v-for="(col, index) in currentView.binding.table.columns" :key="index" class="table-column-config-row" draggable="true" @dragstart="dragStart(index)" @dragover.prevent @drop="drop(index)">
                        <span class="drag-handle">☰</span>
                        <select v-model="col.field"><option value="">-- 选择字段 --</option><option v-for="f in availableFields" :key="f" :value="f">{{ f }}</option></select>
                        <input type="text" v-model="col.displayName" placeholder="列显示名" />
                        <button @click="removeTableColumn(index)" class="button-danger-small">&times;</button>
                    </div>
                    <button @click="addTableColumn" class="button-secondary-small">+ 添加一列</button>
                </div>
              </div>
              <div class="editor-actions">
                <button v-if="editingIndex !== -1" @click="deleteSelectedView" class="button-danger">删除此视图</button>
                <button @click="confirmEdit" class="button-primary">确认</button>
              </div>
            </div>
            <div v-else class="placeholder-text">请从左侧选择或添加新视图。</div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
// ... (这是一个功能完整的版本，包含了所有必要的逻辑) ...
import { ref, watch, reactive, defineProps, defineEmits } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import { cloneDeep } from 'lodash';

const props = defineProps({
  visible: Boolean,
  bizName: String,
  tableName: String,
  initialViewData: Array,
  fieldSettings: Array,
});
const emit = defineEmits(['close', 'save']);

const localViews = ref([]);
const currentView = ref(null);
const editingIndex = ref(null);
const cardFieldLabels = { title: '标题', subtitle: '副标题', description: '描述', imageUrl: '图片URL', tag: '标签' };
const availableFields = ref([]);

watch(() => props.visible, (isVisible) => {
  if (isVisible) {
    localViews.value = cloneDeep(props.initialViewData || []);
    availableFields.value = (props.fieldSettings || []).filter(f => f.is_returnable).map(f => f.field_name);
    currentView.value = null;
    editingIndex.value = null;
  }
});

const createNewViewObject = () => ({
  view_name: '', view_type: 'table', display_name: '', is_default: false,
  binding: {
    card: { title: '', subtitle: '', description: '', imageUrl: '', tag: '' },
    table: { columns: [] },
  },
});

const selectViewToEdit = (index) => {
  editingIndex.value = index;
  currentView.value = reactive(cloneDeep(localViews.value[index]));
};

const addNewView = () => {
  editingIndex.value = -1; // -1 表示是新增
  currentView.value = reactive(createNewViewObject());
};

const handleDefaultChange = () => {
  if (currentView.value.is_default) {
    localViews.value.forEach((v, i) => {
      if (editingIndex.value === -1 || i !== editingIndex.value) {
        v.is_default = false;
      }
    });
  }
};

const confirmEdit = () => {
  if (!currentView.value.view_name) { alert('视图ID不能为空'); return; }
  if (editingIndex.value === -1) { // 新增
    localViews.value.push(cloneDeep(currentView.value));
  } else { // 修改
    localViews.value[editingIndex.value] = cloneDeep(currentView.value);
  }
  emit('save', cloneDeep(localViews.value));
  close();
};

const deleteSelectedView = () => {
  if (editingIndex.value !== null && editingIndex.value > -1) {
    localViews.value.splice(editingIndex.value, 1);
    emit('save', cloneDeep(localViews.value));
    close();
  }
};

const addTableColumn = () => currentView.value.binding.table.columns.push({ field: '', displayName: '' });
const removeTableColumn = (index) => currentView.value.binding.table.columns.splice(index, 1);

let dragIndex = null;
const dragStart = (index) => { dragIndex = index; };
const drop = (targetIndex) => {
  if (dragIndex !== null) {
    const columns = currentView.value.binding.table.columns;
    const item = columns.splice(dragIndex, 1)[0];
    columns.splice(targetIndex, 0, item);
    dragIndex = null;
  }
};

const close = () => emit('close');
</script>

<style scoped>
/* 粘贴或引入通用的 modal, form-grid, view-config-container 等样式 */
</style>