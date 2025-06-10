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
        <div v-else-if="config.view_type === 'table'" class="table-fields-list">
          <div v-for="(col, index) in config.binding.table.columns" :key="index" class="column-row">
            <select v-model="col.field">
              <option value="">— 选择字段 —</option>
              <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
            <input v-model.trim="col.displayName" class="column-input" placeholder="列显示名 (可选)" />
            <button @click="removeTableColumn(index)" class="btn-icon danger" aria-label="移除此列">&times;</button>
          </div>
          <button @click="addTableColumn" class="button-tertiary">+ 添加一列</button>
        </div>
        <div v-else-if="config.view_type === 'list'" class="table-fields-list">
          <div v-for="(col, i) in config.binding.list.columns" :key="i" class="column-row">
            <select v-model="col.field">
              <option value="">— 选择字段 —</option>
              <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
            <input v-model.trim="col.displayName" class="column-input" placeholder="列显示名 (可选)" />
            <button @click="removeListColumn(i)" class="btn-icon danger" aria-label="移除此列">&times;</button>
          </div>
          <button @click="addListColumn" class="button-tertiary">+ 添加一列</button>
        </div>
        <div v-else-if="config.view_type === 'kanban'" class="bind-fields-grid">
          <div class="form-block">
            <label>分组依据字段 (Group By)</label>
            <select v-model="config.binding.kanban.groupBy">
              <option value="">— 请选择字段 —</option>
              <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
          <div class="form-block" v-for="key in ['title', 'tag']" :key="key">
            <label>{{ kanbanFieldLabels[key] }}</label>
            <select v-model="config.binding.kanban.cardFields[key]">
              <option value="">— 请选择字段 —</option>
              <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
        </div>
        <div v-else-if="config.view_type === 'calendar'" class="bind-fields-grid">
          <div class="form-block">
            <label>日期字段 (Date Field)</label>
            <select v-model="config.binding.calendar.dateField">
              <option value="">— 请选择字段 —</option>
              <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
          <div class="form-block">
            <label>标题字段 (Title Field)</label>
            <select v-model="config.binding.calendar.titleField">
              <option value="">— 请选择字段 —</option>
              <option v-for="f in returnableFields" :key="f" :value="f">{{ f }}</option>
            </select>
          </div>
        </div>
      </div>
      <div class="preview-section">
        <h4 class="section-title preview-title">预览</h4>
        <div v-if="config.view_type === 'cards' && mockData.length" class="preview-cards">
          <div v-for="(item, idx) in mockData.slice(0, 2)" :key="idx" class="preview-card">
            <h5 class="card-title">{{ item[config.binding.card.title] || '标题未绑定' }}</h5>
            <p class="card-subtitle">{{ item[config.binding.card.subtitle] }}</p>
            <p class="card-description">{{ item[config.binding.card.description] }}</p>
            <div class="card-details">
              <div v-for="(detailBinding, j) in config.binding.card.details.filter(d => d)" :key="j">
                <span class="detail-label">{{ detailBinding }}:</span> {{ item[detailBinding] }}
              </div>
            </div>
            <small v-if="config.binding.card.tag" class="card-tag">{{ item[config.binding.card.tag] }}</small>
          </div>
        </div>
        <div v-else-if="config.view_type === 'table' && config.binding.table.columns.length" class="table-wrapper">
          <table class="preview-table">
            <thead>
              <tr>
                <th v-for="col in config.binding.table.columns.filter(c => c.field)" :key="col.field">
                  {{ col.displayName || col.field }}
                </th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="(item, idx) in mockData" :key="idx">
                <td v-for="col in config.binding.table.columns.filter(c => c.field)" :key="col.field">
                  {{ item[col.field] }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <ul v-else-if="config.view_type === 'list' && config.binding.list.columns.length" class="preview-list">
          <li v-for="(item, idx) in mockData" :key="idx">
            <strong v-if="config.binding.list.columns[0]?.field">{{ item[config.binding.list.columns[0]?.field] }}</strong>
            <template v-for="(col, i) in config.binding.list.columns.slice(1)" :key="i">
              <span v-if="col.field" class="list-item-extra">{{ item[col.field] }}</span>
            </template>
          </li>
        </ul>
        <div v-else-if="config.view_type === 'kanban' && config.binding.kanban.groupBy" class="preview-kanban">
          <div v-for="(groupItems, groupName) in groupedKanban" :key="groupName" class="kanban-column">
            <h5 class="kanban-group-title">{{ groupName }}</h5>
            <div v-for="(item, i) in groupItems" :key="i" class="kanban-card">
              <div class="kanban-card-title">{{ item[config.binding.kanban.cardFields.title] || '标题未绑定' }}</div>
              <small v-if="config.binding.kanban.cardFields.tag" class="kanban-card-tag">{{ item[config.binding.kanban.cardFields.tag] }}</small>
            </div>
          </div>
        </div>
         <div v-else-if="config.view_type === 'calendar' && config.binding.calendar.dateField" class="preview-calendar">
          <div v-for="(events, date) in groupedCalendar" :key="date" class="calendar-day">
            <h5 class="calendar-day-title">{{ date }}</h5>
            <ul><li v-for="(event, i) in events" :key="i">{{ event[config.binding.calendar.titleField] || '标题未绑定' }}</li></ul>
          </div>
        </div>
        <div v-else class="status-message info">请在左侧配置字段绑定以生成预览。</div>
      </div>
    </div>
  </section>
</template>

<script setup>
import { defineModel, computed } from 'vue';
const config = defineModel('config', { required: true });
const props = defineProps({
  returnableFields: { type: Array, required: true }
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
    return Array.from({ length: 3 }).map((_, i) => {
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
  const groupByField = config.value.binding.kanban.groupBy;
  if (!groupByField || !mockData.value.length) return {};
  const groups = {};
  mockData.value.forEach(item => {
    const key = item[groupByField] || '未分组';
    if (!groups[key]) groups[key] = [];
    groups[key].push(item);
  });
  return groups;
});
const groupedCalendar = computed(() => {
  const dateField = config.value.binding.calendar.dateField;
  if (!dateField || !mockData.value.length) return {};
  const groups = {};
  mockData.value.forEach(item => {
    const date = item[dateField] || '未知日期';
    if (!groups[date]) groups[date] = [];
    groups[date].push(item);
  });
  return groups;
});
</script>

<style scoped>
.config-section { margin-bottom: 2.5rem; }
.section-title { font-size: 1.15rem; font-weight: 600; margin-bottom: 1rem; border-bottom: 1px solid #e9ecef; padding-bottom: 0.5rem; }
.binding-container { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 2rem; }
.form-block { display: flex; flex-direction: column; gap: 0.5rem; margin-bottom: 1.25rem; }
.form-block label { font-weight: 500; color: #343a40; }
.form-block select, .column-input { width: 100%; padding: 0.6rem; border-radius: 4px; border: 1px solid #ced4da; background-color: #fff; }
.bind-fields-grid { display: grid; grid-template-columns: 1fr; gap: 0.5rem; }
@media (min-width: 500px) {
  .bind-fields-grid { grid-template-columns: 1fr 1fr; column-gap: 1.25rem; }
  .details-block { grid-column: 1 / -1; }
}
.detail-row, .column-row { display: grid; grid-template-columns: 1fr auto; gap: 0.75rem; align-items: center; margin-bottom: 0.5rem; }
.column-row { grid-template-columns: 1fr 1fr auto; }
.button-tertiary { background-color: #f8f9fa; color: #333; border: 1px solid #ced4da; padding: 0.5rem 1rem; border-radius: 5px; cursor: pointer; transition: background-color 0.2s; width: fit-content; }
.button-tertiary:hover { background-color: #e2e6ea; }
.button-tertiary:disabled { background-color: #f8f9fa; color: #adb5bd; cursor: not-allowed; }
.btn-icon.danger { color: #dc3545; border: none; background: transparent; cursor: pointer; font-size: 1.75rem; line-height: 1; padding: 0 0.25rem; }
.status-message { padding: 1rem; margin-top: 1rem; border-radius: 5px; border: 1px solid; text-align: center; }
.status-message.info { background-color: #f8f9fa; border-color: #dee2e6; color: #383d41; }
.preview-section { background: #f9f9f9; padding: 1.5rem; border: 1px solid #e9ecef; border-radius: 8px; overflow: hidden; }
.section-title.preview-title { border: none; margin-bottom: 1rem; padding-bottom: 0; }
.table-wrapper { max-height: 250px; overflow: auto; border: 1px solid #dee2e6; border-radius: 4px; }
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
.calendar-day { background: #fafafa; padding: 0.75rem; border-radius: 4px; border: 1px solid #eee; }
.calendar-day-title { font-size: 0.9rem; font-weight: bold; margin: 0 0 0.5rem; }
.calendar-day ul { list-style: none; padding-left: 0; margin: 0; }
.calendar-day li { background-color: #e9ecef; padding: 0.25rem 0.5rem; border-radius: 3px; font-size: 0.8rem; margin-top: 0.25rem; }
</style>