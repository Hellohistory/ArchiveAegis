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
          <div class="bulk-select-section">
            <label class="bulk-select-label">勾选要显示的列</label>
            <div class="checkbox-grid">
              <div v-for="field in returnableFields" :key="field" class="checkbox-item">
                <input type="checkbox" :id="`field-${field}`" :value="field" v-model="selectedTableFields">
                <label :for="`field-${field}`">{{ field }}</label>
              </div>
            </div>
          </div>

          <div v-if="config.binding.table.columns && config.binding.table.columns.length > 0">
            <div class="column-row-header">
              <span>源字段</span>
              <span>显示名称 (可拖拽排序)</span>
            </div>
            <draggable
              v-model="config.binding.table.columns"
              item-key="field"
              handle=".drag-handle"
              class="fields-config-list"
            >
              <template #item="{element: col}">
                <div class="column-row-redesigned">
                  <svg class="drag-handle" xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16" aria-label="拖拽排序"><path d="M7 2a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0M7 5a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0M7 8a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0m-3 3a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0m-3 3a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0"/></svg>
                  <input :value="col.field" class="field-select" readonly disabled title="通过上方勾选框移除"/>
                  <span class="separator-arrow">→</span>
                  <input v-model.trim="col.displayName" class="display-name-input" placeholder="自定义显示名称" />
                </div>
              </template>
            </draggable>
          </div>
        </div>

        <div v-else-if="config.view_type === 'list'" class="table-config-wrapper">
          <div class="column-row-header"><span>源字段</span><span>显示名称 (可选)</span></div>
          <div class="fields-config-list">
            <div v-for="(col, index) in config.binding.list.columns" :key="index" class="column-row-redesigned">
              <svg class="drag-handle" xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16" aria-label="拖拽排序"><path d="M7 2a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0M7 5a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0m-3 3a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0m-3 6a1 1 0 1 1-2 0 1 1 0 0 1 2 0m3 0a1 1 0 1 1-2 0 1 1 0 0 1 2 0"/></svg>
              <select v-model="col.field" class="field-select"><option value="">— 选择字段 —</option><option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option></select>
              <span class="separator-arrow">→</span>
              <input v-model.trim="col.displayName" class="display-name-input" placeholder="自定义显示名称" />
              <button @click="removeListColumn(index)" class="btn-icon-redesigned danger" aria-label="移除此列"><svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path d="M5.5 5.5A.5.5 0 0 1 6 6v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m2.5 0a.5.5 0 0 1 .5.5v6a.5.5 0 0 1-1 0V6a.5.5 0 0 1 .5-.5m3 .5a.5.5 0 0 0-1 0v6a.5.5 0 0 0 1 0z"/><path d="M14.5 3a1 1 0 0 1-1 1H13v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V4h-.5a1 1 0 0 1-1-1V2a1 1 0 0 1 1-1H6a1 1 0 0 1 1-1h2a1 1 0 0 1 1 1h3.5a1 1 0 0 1 1 1zM4.118 4 4 4.059V13a1 1 0 0 0 1 1h6a1 1 0 0 0 1-1V4.059L11.882 4zM2.5 3h11V2h-11z"/></svg></button>
            </div>
          </div>
          <button @click="addListColumn" class="button-tertiary">+ 添加一项</button>
        </div>

        <div v-else-if="config.view_type === 'kanban'" class="bind-fields-grid">
          <div class="form-block">
            <label>分组依据字段 (Group By)</label>
            <select v-model="config.binding.kanban.groupBy">
              <option value="">— 请选择字段 —</option><option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
          <div v-for="key in ['title', 'tag']" :key="key" class="form-block">
            <label>{{ kanbanFieldLabels[key] }}</label>
            <select v-model="config.binding.kanban.cardFields[key]">
              <option value="">— 请选择字段 —</option><option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
        </div>

        <div v-else-if="config.view_type === 'calendar'" class="bind-fields-grid">
           <div class="form-block">
            <label>日期字段 (Date Field)</label>
            <select v-model="config.binding.calendar.dateField">
              <option value="">— 请选择字段 —</option><option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
           <div class="form-block">
            <label>事件标题字段 (Title Field)</label>
            <select v-model="config.binding.calendar.titleField">
              <option value="">— 请选择字段 —</option><option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
        </div>

      </div>

      <div class="preview-section">
        <h4 class="section-title preview-title">预览</h4>

        <div v-if="config.view_type === 'cards'" class="preview-cards">
          <div v-for="(item, index) in mockData.slice(0, 4)" :key="index" class="preview-card">
            <div v-if="config.binding.card.title" class="card-title">{{ item[config.binding.card.title] }}</div>
            <div v-if="config.binding.card.subtitle" class="card-subtitle">{{ item[config.binding.card.subtitle] }}</div>
            <div v-if="config.binding.card.description" class="card-description">{{ item[config.binding.card.description] }}</div>
            <div v-if="config.binding.card.details.length" class="card-details">
              <div v-for="detailField in config.binding.card.details" :key="detailField"><template v-if="detailField"><span class="detail-label">{{ detailField }}: </span><span>{{ item[detailField] }}</span></template></div>
            </div>
            <div v-if="config.binding.card.tag" class="card-tag-wrapper"><span class="card-tag">{{ item[config.binding.card.tag] }}</span></div>
          </div>
        </div>

        <div v-else-if="config.view_type === 'table' && config.binding.table.columns.length > 0" class="table-wrapper">
          <table class="preview-table">
            <thead><tr><th v-for="(col, index) in config.binding.table.columns" :key="index">{{ col.displayName || col.field }}</th></tr></thead>
            <tbody><tr v-for="(item, itemIndex) in mockData" :key="itemIndex"><td v-for="(col, colIndex) in config.binding.table.columns" :key="colIndex">{{ item[col.field] }}</td></tr></tbody>
          </table>
        </div>

        <ul v-else-if="config.view_type === 'list' && config.binding.list.columns.length > 0" class="preview-list">
          <li v-for="(item, itemIndex) in mockData" :key="itemIndex">
            <span class="list-item-main">{{ item[config.binding.list.columns[0].field] }}</span>
            <template v-for="(col, colIndex) in config.binding.list.columns.slice(1)" :key="colIndex">
              <span class="list-item-extra">{{ col.displayName || col.field }}: {{ item[col.field] }}</span>
            </template>
          </li>
        </ul>

        <div v-else-if="config.view_type === 'kanban' && config.binding.kanban.groupBy" class="preview-kanban">
          <div v-for="(groupItems, groupName) in groupedKanban" :key="groupName" class="kanban-column">
            <h4 class="kanban-group-title">{{ groupName }}</h4>
            <div v-for="(item, itemIndex) in groupItems" :key="itemIndex" class="kanban-card">
              <div v-if="config.binding.kanban.cardFields.title" class="kanban-card-title">{{ item[config.binding.kanban.cardFields.title] }}</div>
              <div v-if="config.binding.kanban.cardFields.tag" class="kanban-card-tag">{{ item[config.binding.kanban.cardFields.tag] }}</div>
            </div>
          </div>
        </div>

        <div v-else-if="config.view_type === 'calendar' && config.binding.calendar.dateField" class="preview-calendar">
          <div v-for="(dayItems, date) in groupedCalendar" :key="date" class="calendar-day">
            <h4 class="calendar-day-title">{{ date }}</h4>
            <ul><li v-for="(item, itemIndex) in dayItems" :key="itemIndex">{{ item[config.binding.calendar.titleField] }}</li></ul>
          </div>
        </div>

      </div>
    </div>
  </section>
</template>

<script setup>
import { computed, ref, watch } from 'vue';
import draggable from 'vuedraggable';

const config = defineModel('config', { required: true });
const props = defineProps({
  returnableFields: { type: Array, required: true }
});

const selectedTableFields = ref([]);
const allTableColumns = ref([]);

watch(() => config.value.view_type, (viewType) => {
  if (viewType === 'table') {
    if (config.value.binding.table && config.value.binding.table.columns) {
      selectedTableFields.value = config.value.binding.table.columns.map(c => c.field);
    }
  }
}, { immediate: true });

watch(selectedTableFields, (newSelection) => {
  if (config.value.view_type !== 'table') return;

  const currentColumns = config.value.binding.table.columns;

  currentColumns.forEach(col => {
    const match = allTableColumns.value.find(c => c.field === col.field);
    if (match) {
      match.displayName = col.displayName;
    } else {
      allTableColumns.value.push({ ...col });
    }
  });

  const newColumns = newSelection.map(field => {
    const currentCol = currentColumns.find(c => c.field === field);
    if (currentCol) return currentCol;

    const existingCol = allTableColumns.value.find(c => c.field === field);
    if (existingCol) return existingCol;

    const newCol = { field: field, displayName: field };
    allTableColumns.value.push(newCol);
    return newCol;
  });

  config.value.binding.table.columns = newColumns.filter(col => newSelection.includes(col.field));

}, { deep: true });

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

      config.value.binding.table.columns.forEach(col => {
          if (!allTableColumns.value.find(c => c.field === col.field)) {
              allTableColumns.value.push({ ...col });
          }
      });
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
const addListColumn = () => { config.value.binding.list.columns.push({ field: '', displayName: '' }); };
const removeListColumn = i => { config.value.binding.list.columns.splice(i, 1); };

const mockData = computed(() => {
    if (props.returnableFields.length === 0) return [];
    return Array.from({ length: 10 }).map((_, i) => {
        const obj = {};
        props.returnableFields.forEach(f => {
            if (f.toLowerCase().includes('date')) obj[f] = `2025-06-1${i % 4 + 1}`;
            else if (f.toLowerCase().includes('status')) obj[f] = i % 3 === 0 ? '已完成' : (i % 3 === 1 ? '进行中' : '待办');
            else if (f.toLowerCase().includes('count')) obj[f] = (i + 1) * 100;
            else if (f.toLowerCase().includes('code')) obj[f] = `CODE-00${i+1}`;
            else if (f.toLowerCase().includes('size')) obj[f] = `${(i+1)*2}M`;
            else obj[f] = `${f.replace(/_/g, ' ')} 示例 ${i + 1}`;
        });
        return obj;
    });
});

const groupedKanban = computed(() => {
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
.binding-container { display: flex; gap: 2rem; max-height: 60vh; min-height: 400px; }
.binding-forms { flex: 1; min-width: 300px; display: flex; flex-direction: column; overflow-y: auto; padding-right: 12px; }
.preview-section { flex: 1; min-width: 300px; background: #f9f9f9; padding: 1.5rem; border: 1px solid #e9ecef; border-radius: 8px; overflow: auto; }

.table-config-wrapper {
  display: flex;
  flex-direction: column;
}

.fields-config-list {
  padding-right: 8px;
}

.fields-config-list::-webkit-scrollbar { width: 6px; }
.fields-config-list::-webkit-scrollbar-track { background: #f1f1f1; border-radius: 3px; }
.fields-config-list::-webkit-scrollbar-thumb { background: #ccc; border-radius: 3px; }
.fields-config-list::-webkit-scrollbar-thumb:hover { background: #aaa; }

.config-section { margin-bottom: 2.5rem; }
.section-title { font-size: 1.15rem; font-weight: 600; margin-bottom: 1rem; border-bottom: 1px solid #e9ecef; padding-bottom: 0.5rem; }
.form-block { display: flex; flex-direction: column; gap: 0.5rem; margin-bottom: 1.25rem; }
.form-block label { font-weight: 500; color: #343a40; }
.bind-fields-grid { display: grid; grid-template-columns: 1fr; gap: 0.5rem; }
@media (min-width: 500px) {
  .bind-fields-grid { grid-template-columns: 1fr 1fr; column-gap: 1.25rem; }
  .details-block { grid-column: 1 / -1; }
}
.detail-row { display: grid; grid-template-columns: 1fr auto; gap: 0.75rem; align-items: center; margin-bottom: 0.5rem; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; padding: 0.5rem 1rem; border-radius: 5px; cursor: pointer; transition: background-color 0.2s; width: fit-content; margin-top: 0.5rem; }
.button-tertiary:hover { background-color: #e2e6ea; }
.button-tertiary:disabled { background-color: #f8f9fa; color: #adb5bd; cursor: not-allowed; }
.btn-icon.danger { color: #dc3545; border: none; background: transparent; cursor: pointer; font-size: 1.75rem; line-height: 1; padding: 0 0.25rem; }
.status-message { padding: 1rem; margin-top: 1rem; border-radius: 5px; border: 1px solid; text-align: center; }
.status-message.info { background-color: #f8f9fa; border-color: #dee2e6; color: #383d41; }
.section-title.preview-title { border: none; margin-bottom: 1rem; padding-bottom: 0; }
.table-wrapper { max-height: calc(60vh - 80px); overflow: auto; border: 1px solid #dee2e6; border-radius: 4px; }
.preview-cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 1rem; }
.preview-card { background: #fff; border: 1px solid #ddd; padding: 1rem; border-radius: 6px; box-shadow: 0 1px 3px rgba(0,0,0,0.05); display: flex; flex-direction: column; gap: 0.5rem; }
.preview-card .card-title { font-size: 1rem; font-weight: bold; margin: 0; }
.preview-card .card-subtitle { font-size: 0.85rem; color: #6c757d; margin: 0; }
.preview-card .card-description { font-size: 0.85rem; color: #343a40; flex-grow: 1; }
.preview-card .card-details { font-size: 0.8rem; border-top: 1px solid #eee; padding-top: 0.5rem; }
.preview-card .detail-label { font-weight: 500; }
.preview-card .card-tag-wrapper { margin-top: auto; }
.preview-card .card-tag { background-color: #e6f7ff; color: #004085; padding: 0.2rem 0.5rem; border-radius: 4px; font-size: 0.75rem; display: inline-block; }
.preview-table { width: 100%; border-collapse: collapse; }
.preview-table th, .preview-table td { border: 1px solid #ccc; padding: 0.6rem; text-align: left; font-size: 0.9rem; }
.preview-table th { background-color: #f2f2f2; }
.preview-list { list-style: none; padding: 0; }
.preview-list li { padding: 0.75rem; border-bottom: 1px solid #eee; display: flex; align-items: center; flex-wrap: wrap; gap: 0 1rem; }
.preview-list li:last-child { border: none; }
.preview-list .list-item-main { font-weight: 500; }
.preview-list .list-item-extra::before { content: "•"; margin-right: 0.5rem; color: #adb5bd; }
.preview-kanban { display: flex; gap: 1rem; overflow-x: auto; padding-bottom: 1rem; min-height: 200px;}
.kanban-column { background: #f0f0f0; padding: 0.75rem; border-radius: 6px; width: 220px; flex-shrink: 0; }
.kanban-group-title { margin: 0 0 0.75rem; font-size: 0.9rem; font-weight: bold; }
.kanban-card { background: #fff; padding: 0.75rem; margin-top: 0.5rem; border-radius: 4px; border: 1px solid #ccc; box-shadow: 0 1px 2px rgba(0,0,0,0.05); }
.kanban-card-title { font-size: 0.9rem; font-weight: 500; }
.kanban-card-tag { font-size: 0.75rem; color: #dc3545; margin-top: 0.5rem; display: block; }
.preview-calendar { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 0.5rem; }
.calendar-day { background: #fafafa; padding: 0.75rem; border: 1px solid #eee; border-radius: 4px; }
.calendar-day-title { font-size: 0.9rem; font-weight: bold; margin: 0 0 0.5rem; }
.calendar-day ul { list-style: none; padding-left: 0; margin: 0; }
.calendar-day li { background-color: #e9ecef; padding: 0.25rem 0.5rem; border-radius: 3px; font-size: 0.8rem; margin-top: 0.25rem; }
.column-row-redesigned { display: flex; align-items: center; gap: 0.75rem; padding: 0.5rem; border-radius: 6px; margin-bottom: 0.5rem; background-color: #ffffff; border: 1px solid #e9ecef; transition: box-shadow 0.2s, border-color 0.2s; }
.column-row-redesigned:hover { border-color: #ced4da; box-shadow: 0 2px 4px rgba(0,0,0,0.06); }
.drag-handle { cursor: grab; color: #adb5bd; flex-shrink: 0; }
.drag-handle:active { cursor: grabbing; }
.field-select, .display-name-input { border: 1px solid #ced4da; padding: 0.5rem; border-radius: 4px; flex: 1; min-width: 80px; background-color: #fff; }
.field-select:focus, .display-name-input:focus { outline: none; border-color: #86b7fe; box-shadow: 0 0 0 0.25rem rgba(13, 110, 253, 0.25); }
.separator-arrow { color: #6c757d; font-size: 1.2rem; }
.btn-icon-redesigned { border: none; background: transparent; cursor: pointer; color: #6c757d; padding: 0.25rem; display: flex; align-items: center; justify-content: center; border-radius: 50%; width: 32px; height: 32px; transition: background-color 0.2s, color 0.2s; flex-shrink: 0; }
.btn-icon-redesigned:hover { background-color: #f8d7da; color: #842029; }

.bulk-select-section {
  margin-bottom: 1.5rem;
  padding: 1rem;
  background-color: #f8f9fa;
  border: 1px solid #e9ecef;
  border-radius: 6px;
}
.bulk-select-label {
  font-weight: 600;
  display: block;
  margin-bottom: 0.75rem;
}
.checkbox-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
  gap: 0.75rem;
}
.checkbox-item {
  display: flex;
  align-items: center;
}
.checkbox-item input[type="checkbox"] {
  width: auto;
  margin-right: 0.5rem;
}
.checkbox-item label {
  font-weight: 400;
  cursor: pointer;
  margin-bottom: 0;
}
.field-select[readonly] {
  background-color: #e9ecef;
  cursor: default;
  border: 1px solid #ced4da;
}
</style>