<!--src/components/ResultsDisplay.vue-->
<template>
  <div class="table-results-container">
    <table>
      <thead>
        <tr>
          <th v-for="header in config.columns" :key="header.field">
            {{ header.displayName }}
          </th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="(row, rowIndex) in results" :key="rowIndex">
          <td v-for="header in config.columns" :key="`${rowIndex}-${header.field}`">
            {{ row[header.field] }}
          </td>
        </tr>
      </tbody>
    </table>
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
.table-results-container {
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
}
th, td {
  border: 1px solid #ddd;
  padding: 12px 15px;
}
th {
  background-color: #f2f2f2;
  font-weight: 600;
}
tbody tr:nth-of-type(even) {
  background-color: #f9f9f9;
}
</style>