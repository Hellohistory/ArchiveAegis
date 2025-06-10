<template>
  <section class="config-section">
    <h4 class="section-title">2. 字段属性配置</h4>
    <p class="section-description">
      为所有物理字段配置基础属性。“数据类型”已根据字段名智能设定，仅在需要时修改。<br/>
      勾选“可返回”的字段，才能在下方的“视图字段绑定”步骤中使用。
    </p>
    <input
      v-model="filterKeyword"
      placeholder="🔍 搜索字段名…"
      class="field-search"
    />
    <div class="table-wrapper">
      <table class="fields-table">
        <thead>
          <tr>
            <th>物理字段名</th>
            <th>
              <input
                type="checkbox"
                title="全选/全不选 可搜索"
                @change="toggleAll('is_searchable', $event.target.checked)"
              />
              可搜索
            </th>
            <th>
              <input
                type="checkbox"
                title="全选/全不选 可返回"
                @change="toggleAll('is_returnable', $event.target.checked)"
              />
              可返回
            </th>
            <th>数据类型</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="field in filteredFields" :key="field.field_name">
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
</template>

<script setup>
import { ref, computed, defineModel } from 'vue';
const fields = defineModel('fields', { required: true });
const filterKeyword = ref('');
const filteredFields = computed(() =>
  fields.value.filter(f => f.field_name.toLowerCase().includes(filterKeyword.value.toLowerCase()))
);
const toggleAll = (prop, checked) => {
  fields.value.forEach(f => f[prop] = checked);
};
</script>

<style scoped>
.config-section { margin-bottom: 2.5rem; }
.section-title { font-size: 1.15rem; font-weight: 600; margin-bottom: 0.75rem; border-bottom: 1px solid #e9ecef; padding-bottom: 0.5rem; }
.section-description { font-size: 0.9em; color: #6c757d; margin-bottom: 1rem; line-height: 1.5; }
.field-search { margin-bottom: 1rem; padding: 0.6rem; width: 100%; border: 1px solid #ced4da; border-radius: 4px; }
.table-wrapper { max-height: 350px; overflow-y: auto; border: 1px solid #dee2e6; border-radius: 4px; }
.fields-table { width: 100%; border-collapse: collapse; }
.fields-table th, .fields-table td { padding: 0.8rem; text-align: left; border-bottom: 1px solid #dee2e6; vertical-align: middle; }
.fields-table th { background-color: #f8f9fa; position: sticky; top: 0; z-index: 1; font-weight: 600; }
.fields-table tr:last-child td { border-bottom: none; }
.fields-table input[type="checkbox"] { transform: scale(1.2); cursor: pointer; margin-right: 0.5rem; }
.fields-table select { width: 100%; padding: 0.5rem; border-radius: 4px; border: 1px solid #ced4da; }
</style>