<template>
  <section class="config-section">
    <h4 class="section-title">1. 视图基本设置</h4>
    <form class="form-grid" @submit.prevent>
      <div class="form-block">
        <label for="viewDisplayName">视图显示名</label>
        <input
          id="viewDisplayName"
          v-model.trim="config.display_name"
          placeholder="如：用户列表"
        />
      </div>
      <div class="form-block">
        <label for="viewId">视图ID (英文/数字/下划线)</label>
        <input
          id="viewId"
          v-model.trim="config.view_name"
          placeholder="如：user_list"
        />
      </div>
      <div class="form-block">
        <label for="viewType">视图类型</label>
        <select id="viewType" v-model="config.view_type">
          <option value="cards">卡片 (Cards)</option>
          <option value="table">表格 (Table)</option>
          <option value="list">列表 (List)</option>
          <option value="kanban">看板 (Kanban)</option>
          <option value="calendar">日历 (Calendar)</option>
        </select>
      </div>
    </form>
  </section>
</template>

<script setup>
import { defineModel } from 'vue';
const config = defineModel('config', { required: true });
</script>

<style scoped>
.config-section { margin-bottom: 2.5rem; }
.section-title { font-size: 1.15rem; font-weight: 600; margin-bottom: 1rem; border-bottom: 1px solid #e9ecef; padding-bottom: 0.5rem; }
.form-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(250px, 1fr)); gap: 1.5rem; }
.form-block { display: flex; flex-direction: column; gap: 0.5rem; }
.form-block label { font-weight: 500; color: #343a40; }
.form-block input, .form-block select {
  width: 100%; padding: 0.6rem; border-radius: 4px; border: 1px solid #ced4da;
  transition: border-color 0.2s, box-shadow 0.2s;
}
.form-block input:focus, .form-block select:focus {
  border-color: #80bdff; outline: 0; box-shadow: 0 0 0 0.2rem rgba(0,123,255,.25);
}
</style>