<template>
  <section class="config-section">
    <h4 class="section-title">3. 视图字段绑定</h4>
    <div v-if="returnableFields.length === 0" class="status-message info">
      请先在“2. 字段属性配置”中勾选至少一个“可返回”的字段。
    </div>
    <div v-else class="binding-container">
      <div class="binding-forms">
        <div v-if="config.view_type === 'cards'" class="bind-fields-grid">
          <div v-for="key in ['title', 'subtitle', 'description', 'tag']" :key="key" class="form-block">
            <label>{{ cardFieldLabels[key] }}</label>
            <select v-model="config.binding.card[key]">
              <option value="">— 请选择字段 —</option>
              <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
          <div class="form-block details-block">
            <label>详细字段（最多3项）</label>
            <div v-for="(item, idx) in config.binding.card.details" :key="idx" class="detail-row">
              <select v-model="config.binding.card.details[idx]">
                <option value="">— 请选择字段 —</option>
                <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
              </select>
              <button @click="removeDetailField(idx)" class="btn-icon danger" aria-label="移除此字段">&times;</button>
            </div>
            <button @click="addDetailField" :disabled="config.binding.card.details.length >= 3" class="button-tertiary">+ 添加字段</button>
          </div>
        </div>

        <div v-else-if="config.view_type === 'table'" class="table-config-wrapper">
          <div class="column-row-header">
            <span>源字段</span>
            <span>显示名称 (可选)</span>
          </div>
          <div class="fields-config-list">
            <div v-for="(col, index) in config.binding.table.columns" :key="index" class="column-row-redesigned">
              <svg class="drag-handle" xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16" aria-label="拖拽排序">
                <path d="M7 2a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0M7 5a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0M7 8a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0m-3 3a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0m-3 3a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0"/>
              </svg>
              <select v-model="col.field" class="field-select">
                <option value="">— 选择字段 —</option>
                <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
              </select>
              <span class="separator-arrow">→</span>
              <input v-model.trim="col.displayName" class="display-name-input" placeholder="自定义显示名称" />
              <button @click="removeTableColumn(index)" class="btn-icon-redesigned danger" aria-label="移除此列">
                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16">
                  <path d="M5.5 5.5A.5.5 0 0 1 6 6v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m2.5 0a.5.5 0 0 1 .5.5v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m3 .5a.5.5 0 0 0-1 0v6a.5.5 0 0 0 1 0z"/>
                  <path d="M14.5 3a1 1 0 0 1-1 1H13v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V4h-.5a1 1 0 0 1-1-1V2a1 1 0 0 1 1-1H6a1 1 0 0 1 1-1h2a1 1 0 0 1 1 1h3.5a1 1 0 0 1 1 1zM4.118 4 4 4.059V13a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1V4.059L11.882 4zM2.5 3h11V2h-11z"/>
                </svg>
              </button>
            </div>
          </div>
          <button @click="addTableColumn" class="button-tertiary">+ 添加一列</button>
        </div>

        <div v-else-if="config.view_type === 'list'" class="table-fields-list">
            </div>

        <div v-else-if="config.view_type === 'kanban'" class="bind-fields-grid">
            </div>

        <div v-else-if="config.view_type === 'calendar'" class="bind-fields-grid">
            </div>
      </div>

      <div class="preview-section">
        <h4 class="section-title preview-title">预览</h4>
        </div>
    </div>
  </section>
</template>


<script setup>
import { defineModel, computed, watch } from 'vue';

const config = defineModel('config', { required: true });
const props = defineProps({
  returnableFields: { type: Array, required: true }
});


watch(() => config.value.view_type, (viewType) => {
  if (!config.value.binding) {
    config.value.binding = {};
  }
  switch (viewType) {
    case 'cards':
      if (!config.value.binding.card) {
        config.value.binding.card = { title: '', subtitle: '', description: '', tag: '', details: [] };
      }
      break;
    case 'table':
      if (!config.value.binding.table) {
        config.value.binding.table = { columns: [] };
      }
      break;
    case 'list':
      if (!config.value.binding.list) {
        config.value.binding.list = { columns: [] };
      }
      break;
    case 'kanban':
      if (!config.value.binding.kanban) {
        config.value.binding.kanban = { groupBy: '', cardFields: { title: '', tag: '' } };
      }
      break;
    case 'calendar':
      if (!config.value.binding.calendar) {
        config.value.binding.calendar = { dateField: '', titleField: '' };
      }
      break;
  }
}, {
  immediate: true,
  deep: false
});


const cardFieldLabels = { title: '卡片标题 (Title)', subtitle: '卡片副标题 (Subtitle)', description: '卡片描述 (Description)', tag: '标签 (Tag)' };
const kanbanFieldLabels = { title: '卡片标题 (Title)', tag: '标签 (Tag)' };
const addDetailField = () => { if (config.value.binding.card.details.length < 3) { config.value.binding.card.details.push(''); } };
const removeDetailField = i => { config.value.binding.card.details.splice(i, 1); };
const addTableColumn = () => { config.value.binding.table.columns.push({ field: '', displayName: '' }); };
const removeTableColumn = i => { config.value.binding.table.columns.splice(i, 1); };
const addListColumn = () => { config.value.binding.list.columns.push({ field: '', displayName: '' }); };
const removeListColumn = i => { config.value.binding.list.columns.splice(i, 1); };

const mockData = computed(() => {
    if (props.returnableFields.length === 0) return [];
    return Array.from({ length: 10 }).map((_, i) => {
        const obj = {};
        props.returnableFields.forEach(f => {
            if (f.toLowerCase().includes('date')) obj[f] = `2025-06-1${i+1}`;
            else if (f.toLowerCase().includes('status')) obj[f] = i % 2 === 0 ? '已完成' : '进行中';
            else obj[f] = `${f.replace(/_/g, ' ')} 示例 ${i + 1}`;
        });
        return obj;
    });
});

const groupedKanban = computed(() => {
  // 增加安全检查
  const binding = config.value.binding.kanban;
  if (!binding || !binding.groupBy || !mockData.value.length) return {};
  const groups = {};
  mockData.value.forEach(item => {
    const key = item[binding.groupBy] || '未分组';
    if (!groups[key]) groups[key] = [];
    groups[key].push(item);
  });
  return groups;
});

const groupedCalendar = computed(() => {
  // 增加安全检查
  const binding = config.value.binding.calendar;
  if (!binding || !binding.dateField || !mockData.value.length) return {};
  const groups = {};
  mockData.value.forEach(item => {
    const date = item[binding.dateField] || '未知日期';
    if (!groups[date]) groups[date] = [];
    groups[date].push(item);
  });
  return groups;
});
</script>


<style scoped>
/* --- 布局及滚动优化 --- */
.binding-container {
  /* 从 grid 改为 flex 布局 */
  display: flex;
  gap: 2rem;
  /* 设定一个最大高度，可以是视口高度减去上下边距，或者一个固定值 */
  /* 这有助于内联滚动容器计算其高度 */
  max-height: 60vh;
  min-height: 400px;
}

.binding-forms {
  flex: 1;
  min-width: 300px;
  display: flex;
  flex-direction: column;
}

.preview-section {
  flex: 1;
  min-width: 300px;
  background: #f9f9f9;
  padding: 1.5rem;
  border: 1px solid #e9ecef;
  border-radius: 8px;
  /* 预览区本身也可以有独立的滚动 */
  overflow: auto;
}

.table-config-wrapper {
  display: flex;
  flex-direction: column;
  /* 让其填满父容器的可用高度 */
  flex-grow: 1;
  overflow: hidden; /* 防止子元素溢出 */
}

.fields-config-list {
  /* 关键：让这个列表在超出时滚动 */
  overflow-y: auto;
  flex-grow: 1;
  padding-right: 8px; /* 为滚动条留出空间，防止内容紧贴 */
}

/* 优化滚动条样式 (可选) */
.fields-config-list::-webkit-scrollbar {
  width: 6px;
}
.fields-config-list::-webkit-scrollbar-track {
  background: #f1f1f1;
  border-radius: 3px;
}
.fields-config-list::-webkit-scrollbar-thumb {
  background: #ccc;
  border-radius: 3px;
}
.fields-config-list::-webkit-scrollbar-thumb:hover {
  background: #aaa;
}


/* --- 原始样式 (部分保留和调整) --- */
.config-section { margin-bottom: 2.5rem; }
.section-title { font-size: 1.15rem; font-weight: 600; margin-bottom: 1rem; border-bottom: 1px solid #e9ecef; padding-bottom: 0.5rem; }
.form-block { display: flex; flex-direction: column; gap: 0.5rem; margin-bottom: 1.25rem; }
.form-block label { font-weight: 500; color: #343a40; }
.form-block select, .column-input { width: 100%; padding: 0.6rem; border-radius: 4px; border: 1px solid #ced4da; background-color: #fff; }
.bind-fields-grid { display: grid; grid-template-columns: 1fr; gap: 0.5rem; }
@media (min-width: 500px) {
  .bind-fields-grid { grid-template-columns: 1fr 1fr; column-gap: 1.25rem; }
  .details-block { grid-column: 1 / -1; }
}
.detail-row { display: grid; grid-template-columns: 1fr auto; gap: 0.75rem; align-items: center; margin-bottom: 0.5rem; }
.column-row { display: grid; grid-template-columns: 1fr 1fr auto; gap: 0.75rem; align-items: center; margin-bottom: 0.5rem; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; padding: 0.5rem 1rem; border-radius: 5px; cursor: pointer; transition: background-color 0.2s; width: fit-content; margin-top: 0.5rem; }
.button-tertiary:hover { background-color: #e2e6ea; }
.button-tertiary:disabled { background-color: #f8f9fa; color: #adb5bd; cursor: not-allowed; }
.btn-icon.danger { color: #dc3545; border: none; background: transparent; cursor: pointer; font-size: 1.75rem; line-height: 1; padding: 0 0.25rem; }
.status-message { padding: 1rem; margin-top: 1rem; border-radius: 5px; border: 1px solid; text-align: center; }
.status-message.info { background-color: #f8f9fa; border-color: #dee2e6; color: #383d41; }
.section-title.preview-title { border: none; margin-bottom: 1rem; padding-bottom: 0; }
.table-wrapper { max-height: calc(60vh - 80px); /* 动态计算预览区表格的最大高度 */ overflow: auto; border: 1px solid #dee2e6; border-radius: 4px; }
.preview-cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 1rem; }
.preview-card { background: #fff; border: 1px solid #ddd; padding: 1rem; border-radius: 6px; box-shadow: 0 1px 3px rgba(0,0,0,0.05); }
.preview-card .card-title { font-size: 1rem; font-weight: bold; margin: 0 0 0.25rem; }
.preview-card .card-subtitle, .preview-card .card-description { font-size: 0.85rem; color: #6c757d; margin: 0 0 0.5rem; }
.preview-card .card-details { font-size: 0.8rem; margin: 0.75rem 0; }
.preview-card .detail-label { font-weight: 500; }
.preview-card .card-tag { background-color: #e6f7ff; color: #004085; padding: 0.2rem 0.5rem; border-radius: 4px; font-size: 0.75rem; display: inline-block; }
.preview-table { width: 100%; border-collapse: collapse; }
.preview-table th, .preview-table td { border: 1px solid #ccc; padding: 0.6rem; text-align: left; font-size: 0.9rem; }
.preview-table th { background-color: #f2f2f2; }
.preview-list { list-style: none; padding: 0; }
.preview-list li { padding: 0.75rem 0; border-bottom: 1px solid #eee; }
.preview-list li:last-child { border: none; }
.preview-list .list-item-extra::before { content: "•"; margin: 0 0.5rem; color: #adb5bd; }
.preview-kanban { display: flex; gap: 1rem; overflow-x: auto; padding-bottom: 1rem; }
.kanban-column { background: #f0f0f0; padding: 0.75rem; border-radius: 6px; width: 200px; flex-shrink: 0; }
.kanban-group-title { margin: 0 0 0.75rem; font-size: 0.9rem; font-weight: bold; }
.kanban-card { background: #fff; padding: 0.75rem; margin-top: 0.5rem; border-radius: 4px; border: 1px solid #ccc; box-shadow: 0 1px 2px rgba(0,0,0,0.05); }
.kanban-card-title { font-size: 0.9rem; }
.kanban-card-tag { font-size: 0.75rem; color: #dc3545; margin-top: 0.5rem; display: block; }
.preview-calendar { display: grid; grid-template-columns: repeat(auto-fill, minmax(130px, 1fr)); gap: 1rem; }
.calendar-day { background: #fafafa; padding: 0.75rem; border: 1px solid #eee; border-radius: 4px; }
.calendar-day-title { font-size: 0.9rem; font-weight: bold; margin: 0 0 0.5rem; }
.calendar-day ul { list-style: none; padding-left: 0; margin: 0; }
.calendar-day li { background-color: #e9ecef; padding: 0.25rem 0.5rem; border-radius: 3px; font-size: 0.8rem; margin-top: 0.25rem; }


/* --- 新设计的表格行样式 (包含图标变形修复) --- */
.table-fields-list-redesigned .column-row-header {
  display: flex;
  gap: 0.75rem;
  padding: 0 38px 0 38px; /* 左右预留图标和箭头的位置 */
  font-size: 0.85rem;
  font-weight: 500;
  color: #6c757d;
  margin-bottom: 0.5rem;
}
.table-fields-list-redesigned .column-row-header span {
  flex: 1;
}

.column-row-redesigned {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.5rem;
  border-radius: 6px;
  margin-bottom: 0.5rem;
  background-color: #ffffff;
  border: 1px solid #e9ecef;
  transition: box-shadow 0.2s, border-color 0.2s;
}

.column-row-redesigned:hover {
  border-color: #ced4da;
  box-shadow: 0 2px 4px rgba(0,0,0,0.06);
}

.drag-handle {
  cursor: grab;
  color: #adb5bd;
  flex-shrink: 0; /* 禁止压缩 */
}
.drag-handle:active {
  cursor: grabbing;
}

.field-select, .display-name-input {
  border: 1px solid #ced4da;
  padding: 0.5rem;
  border-radius: 4px;
  flex: 1; /* 占据主要空间 */
  min-width: 80px; /* 给输入框一个最小宽度 */
  background-color: #fff;
}
.field-select:focus, .display-name-input:focus {
  outline: none;
  border-color: #86b7fe;
  box-shadow: 0 0 0 0.25rem rgba(13, 110, 253, 0.25);
}

.separator-arrow {
  color: #6c757d;
  font-size: 1.2rem;
}

.btn-icon-redesigned {
  border: none;
  background: transparent;
  cursor: pointer;
  color: #6c757d;
  padding: 0.25rem;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  width: 32px;
  height: 32px;
  transition: background-color 0.2s, color 0.2s;
  flex-shrink: 0; /* 关键：禁止按钮被压缩变形 */
}

.btn-icon-redesigned:hover {
  background-color: #f8d7da;
  color: #842029;
}
</style>