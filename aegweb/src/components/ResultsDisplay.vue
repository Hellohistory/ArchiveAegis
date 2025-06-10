<!--src/components/ResultsDisplay.vue-->
<template>
  <div class="results-display-wrapper">
    <div class="table-container">
      <table>
        <thead>
          <tr>
            <th v-for="header in config.columns" :key="header.field">
              {{ header.displayName || header.field }}
            </th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="results.length === 0">
            <td :colspan="config.columns.length" class="no-data-cell">
              没有找到匹配的结果。
            </td>
          </tr>
          <tr v-for="(row, rowIndex) in results" :key="rowIndex">
            <td v-for="header in config.columns" :key="`${rowIndex}-${header.field}`">
              {{ row[header.field] }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
defineProps({
  results: {
    type: Array,
    required: true,
  },
  config: {
    type: Object,
    required: true, // 接收 table 的 binding 配置
  },
});
</script>

<style scoped>
.results-display-wrapper {
  background-color: #ffffff;
  border-radius: 8px;
  border: 1px solid #e9ecef;
  box-shadow: 0 1px 3px rgba(0,0,0,0.04);
  overflow: hidden; /* 确保内部元素的圆角生效 */
}

.table-container {
  /* 当表格内容超出容器宽度时，出现水平滚动条 */
  overflow-x: auto;
  -webkit-overflow-scrolling: touch; /* 在移动设备上优化滚动体验 */
}

table {
  width: 100%;
  border-collapse: collapse; /* 合并边框 */
  text-align: left;
  font-size: 0.95em;
}

th, td {
  /* 移除旧的四周边框，只保留底部边框 */
  border: none;
  border-bottom: 1px solid #e9ecef;
  padding: 1rem 1.25rem; /* 增加内边距，使其更疏朗 */
  vertical-align: middle;
}

thead th {
  background-color: #f8f9fa; /* 使用更柔和的表头背景色 */
  color: #495057;
  font-weight: 600;
  border-bottom-width: 2px; /* 加粗表头下边框，以示区分 */
  border-color: #dee2e6;
  white-space: nowrap; /* 防止表头文字换行 */
}

tbody tr {
  transition: background-color 0.15s ease-in-out;
}

/* 使用 hover 效果代替斑马条纹 */
tbody tr:hover {
  background-color: #f1f3f5;
}

/* 移除最后一行数据的下边框，使表格底部更整洁 */
tbody tr:last-of-type td {
  border-bottom: none;
}

.no-data-cell {
  text-align: center;
  color: #6c757d;
  padding: 2rem;
  font-style: italic;
}
</style>