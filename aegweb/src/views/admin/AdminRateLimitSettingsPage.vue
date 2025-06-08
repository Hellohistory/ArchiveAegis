<!--src/views/admin/AdminRateLimitSettingsPage.vue-->
<template>
  <div class="admin-page-container">
    <header class="page-header">
      <router-link to="/admin/dashboard" class="back-link">&laquo; 返回仪表盘</router-link>
      <h1>全局速率限制配置</h1>
    </header>

    <div v-if="isLoading" class="loading-message">正在加载...</div>
    <div v-if="error" class="error-message">{{ error }}</div>
    <div v-if="saveMessage" class="success-message">{{ saveMessage }}</div>

    <div v-if="!isLoading" class="form-section">
      <h3 class="section-header">全局IP速率限制</h3>
      <p class="section-description">为所有未经身份验证的请求设置默认的速率限制。</p>

      <div class="setting-item">
        <span class="setting-label">每分钟请求数:</span>
        <div class="setting-value">
          <input type="number" v-model.number="ipLimit.rate_limit_per_minute" :disabled="!isEditing" />
        </div>
      </div>

      <div class="setting-item">
        <span class="setting-label">瞬时请求峰值:</span>
        <div class="setting-value">
          <input type="number" v-model.number="ipLimit.burst_size" :disabled="!isEditing" />
        </div>
      </div>
    </div>

    <div class="form-actions">
        <button v-if="!isEditing" @click="isEditing = true" class="submit-button edit-button">编辑</button>
        <template v-else>
          <button @click="cancel" class="button-secondary">取消</button>
          <button @click="save" :disabled="isSaving" class="submit-button">
            {{ isSaving ? '保存中...' : '保存更改' }}
          </button>
        </template>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import { cloneDeep } from 'lodash';

const isEditing = ref(false);
const isLoading = ref(true);
const isSaving = ref(false);
const error = ref('');
const saveMessage = ref('');

const ipLimit = ref({ rate_limit_per_minute: 60, burst_size: 20 });
const originalIpLimit = ref(null);

const fetchSettings = async () => {
  isLoading.value = true;
  error.value = '';
  try {

    originalIpLimit.value = cloneDeep(ipLimit.value);
  } catch (err) {
    error.value = err.response?.data?.error || "加载配置失败";
  } finally {
    isLoading.value = false;
  }
};

onMounted(fetchSettings);

const cancel = () => {
  ipLimit.value = cloneDeep(originalIpLimit.value);
  isEditing.value = false;
};

const save = async () => {
  isSaving.value = true;
  saveMessage.value = '';
  error.value = '';
  try {
    await apiClient.put(ENDPOINTS.UPDATE_IP_LIMIT, ipLimit.value);
    saveMessage.value = "配置已成功保存！";
    originalIpLimit.value = cloneDeep(ipLimit.value);
    isEditing.value = false;
  } catch (err) {
    error.value = err.response?.data?.error || "保存失败";
  } finally {
    isSaving.value = false;
  }
};
</script>

<style scoped>
/* 这里可以复用 AdminBizConfigPage 的样式，或者定义自己的样式 */
.admin-page-container { padding: 2rem; max-width: 900px; margin: 2rem auto; background-color: #ffffff; border-radius: 8px; box-shadow: 0 4px 12px rgba(0,0,0,0.08); }
.page-header { margin-bottom: 2rem; padding-bottom: 1rem; border-bottom: 1px solid #e9ecef; }
.back-link { display: inline-block; margin-bottom: 0.75rem; color: #007bff; text-decoration: none; font-size: 0.95em; }
.page-header h1 { font-size: 1.8em; color: #2c3e50; margin: 0; font-weight: 600; }
.form-section { background-color: #f8f9fa; padding: 1.5rem; border-radius: 6px; margin-bottom: 2rem; border: 1px solid #e9ecef; }
.section-header { margin-top: 0; margin-bottom: 1rem; }
.section-description { font-size: 0.9em; color: #6c757d; margin-top: -0.5rem; margin-bottom: 1.5rem; }
.setting-item { display: flex; align-items: center; gap: 1rem; margin-bottom: 1rem; }
.setting-item label { font-weight: 500; width: 150px; }
.setting-item input { flex-grow: 1; padding: 8px; border-radius: 4px; border: 1px solid #ced4da; }
.form-actions { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 1rem; }
.submit-button, .button-secondary { padding: 0.75rem 1.5rem; border: none; border-radius: 5px; cursor: pointer; font-weight: 500; }
.submit-button.edit-button { background-color: #007bff; color: white; }
.submit-button { background-color: #28a745; color: white; }
.button-secondary { background-color: #6c757d; color: white; }
/* ... 引入其他需要的消息框样式 ... */
</style>