<!--src/components/TableView.vue-->
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
    required: true,
  },
});
</script>

<style scoped>
.table-results-container {
  overflow-x: auto;
  margin-top: 1.5rem;
}
table {
  width: 100%;
  border-collapse: collapse;
  text-align: left;
  background-color: #fff;
}
th, td {
  border: 1px solid #ddd;
  padding: 12px 15px;
  vertical-align: middle;
}
th {
  background-color: #f8f9fa;
  font-weight: 600;
  color: #343a40;
}
tbody tr:nth-of-type(even) {
  background-color: #f9f9f9;
}
tbody tr:hover {
    background-color: #f1f1f1;
}
</style>