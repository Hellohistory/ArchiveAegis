<template>
  <div class="rate-control-container">
    <h1 class="page-title">速率控制</h1>
    <p class="page-description">
      管理系统的全局 API 请求速率以及为特定业务组设定的精细化速率。
    </p>

    <div class="tabs-nav">
      <button
        :class="{ active: activeTab === 'global' }"
        @click="activeTab = 'global'"
      >
        全局速率限制
      </button>
      <button
        :class="{ active: activeTab === 'biz' }"
        @click="activeTab = 'biz'"
      >
        业务组速率限制
      </button>
    </div>

    <div class="tabs-content">
      <div v-show="activeTab === 'global'" class="tab-panel">
        <div v-if="globalLoading" class="status-message loading">正在加载全局配置...</div>
        <div v-if="globalError" class="status-message error">{{ globalError }}</div>

        <form v-if="!globalLoading" @submit.prevent="saveGlobalSettings">
          <p class="section-description">为所有未经身份验证的请求设置默认的速率限制。</p>

          <div class="setting-item">
            <label for="rate-limit-minute">每分钟请求数:</label>
            <input id="rate-limit-minute" type="number" v-model.number="globalLimit.rate_limit_per_minute" :disabled="!isEditingGlobal" placeholder="例如: 60" />
          </div>

          <div class="setting-item">
            <label for="burst-size-global">瞬时请求峰值:</label>
            <input id="burst-size-global" type="number" v-model.number="globalLimit.burst_size" :disabled="!isEditingGlobal" placeholder="例如: 120" />
          </div>

          <div v-if="globalSaveMessage" class="status-message success">{{ globalSaveMessage }}</div>

          <div class="form-actions">
            <button v-if="!isEditingGlobal" type="button" @click="isEditingGlobal = true" class="button-primary">编辑</button>
            <template v-else>
              <button type="button" @click="cancelGlobalEdit" class="button-secondary">取消</button>
              <button type="submit" :disabled="isSavingGlobal" class="button-primary">
                {{ isSavingGlobal ? '保存中...' : '保存更改' }}
              </button>
            </template>
          </div>
        </form>
      </div>

      <div v-show="activeTab === 'biz'" class="tab-panel">
        <p class="section-description">为指定的业务组设置独立的速率，它将覆盖全局限制。</p>

        <div class="biz-selector">
          <label for="biz-select">选择要配置的业务组:</label>
          <select id="biz-select" v-model="selectedBiz">
            <option disabled value="">请选择...</option>
            <option v-for="biz in bizList" :key="biz.name" :value="biz.name">
              {{ biz.name }}
            </option>
          </select>
        </div>

        <form v-if="selectedBiz" @submit.prevent="saveBizSettings">
          <div class="setting-item">
            <label for="rate-limit-second-biz">每秒请求数:</label>
            <input id="rate-limit-second-biz" type="number" v-model.number="bizRateLimit.rate_limit_per_second" placeholder="例如: 10" />
          </div>
          <div class="setting-item">
            <label for="burst-size-biz">瞬时请求峰值:</label>
            <input id="burst-size-biz" type="number" v-model.number="bizRateLimit.burst_size" placeholder="例如: 20" />
          </div>

          <div v-if="bizSaveMessage" class="status-message success">{{ bizSaveMessage }}</div>
          <div v-if="bizSaveError" class="status-message error">{{ bizSaveError }}</div>

          <div class="form-actions">
            <button type="submit" :disabled="isSavingBiz" class="button-primary">
              {{ isSavingBiz ? '保存中...' : `保存对 ${selectedBiz} 的设置` }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, watch } from 'vue';
import apiClient from '@/services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints';
import { cloneDeep } from 'lodash';

const activeTab = ref('global');

// --- 全局限制面板的状态 ---
const isEditingGlobal = ref(false);
const globalLoading = ref(true);
const isSavingGlobal = ref(false);
const globalError = ref('');
const globalSaveMessage = ref('');
const globalLimit = reactive({ rate_limit_per_minute: null, burst_size: null });
const originalGlobalLimit = ref(null);

// --- 业务组限制面板的状态 ---
const bizList = ref([]);
const selectedBiz = ref('');
const isSavingBiz = ref(false);
const bizSaveMessage = ref('');
const bizSaveError = ref('');
const bizRateLimit = reactive({ rate_limit_per_second: null, burst_size: null });

// --- “全局限制”面板的逻辑 ---
const fetchGlobalSettings = async () => {
  globalLoading.value = true;
  globalError.value = '';
  try {
    const response = await apiClient.get(ENDPOINTS.IP_LIMIT);
    Object.assign(globalLimit, response.data);
    originalGlobalLimit.value = cloneDeep(response.data);
  } catch (err) {
    globalError.value = `加载全局配置失败: ${err.response?.data?.error || '请检查API'}`;
  } finally {
    globalLoading.value = false;
  }
};

const cancelGlobalEdit = () => {
  Object.assign(globalLimit, originalGlobalLimit.value);
  isEditingGlobal.value = false;
};

const saveGlobalSettings = async () => {
  isSavingGlobal.value = true;
  globalSaveMessage.value = '';
  globalError.value = '';
  try {
    await apiClient.put(ENDPOINTS.IP_LIMIT, globalLimit);
    originalGlobalLimit.value = cloneDeep(globalLimit);
    isEditingGlobal.value = false;
    globalSaveMessage.value = "全局配置已成功保存！";
    setTimeout(() => globalSaveMessage.value = '', 4000);
  } catch (err) {
    globalError.value = `保存失败: ${err.response?.data?.error || '请检查输入值'}`;
  } finally {
    isSavingGlobal.value = false;
  }
};

// --- “业务组限制”面板的逻辑 ---
const fetchBizList = async () => {
  try {
    const response = await apiClient.get(ENDPOINTS.BIZ_SUMMARY);
    bizList.value = Object.keys(response.data).map(name => ({ name }));
  } catch (err) {
    console.error("加载业务组列表失败:", err);
  }
};

watch(selectedBiz, () => {
  // 切换业务组时，清空表单和消息，防止误操作
  bizRateLimit.rate_limit_per_second = null;
  bizRateLimit.burst_size = null;
  bizSaveMessage.value = '';
  bizSaveError.value = '';
});

const saveBizSettings = async () => {
  if (!selectedBiz.value) return;

  isSavingBiz.value = true;
  bizSaveMessage.value = '';
  bizSaveError.value = '';

  // 准备提交的数据，如果用户未填写，则发送null，以便后端可以清除设置
  const payload = {
    rate_limit_per_second: bizRateLimit.rate_limit_per_second || null,
    burst_size: bizRateLimit.burst_size || null,
  };

  try {
    await apiClient.put(ENDPOINTS.BIZ_RATELIMIT(selectedBiz.value), payload);
    bizSaveMessage.value = `业务组 "${selectedBiz.value}" 的速率限制已成功更新！`;
    setTimeout(() => bizSaveMessage.value = '', 4000);
  } catch (err) {
    bizSaveError.value = `保存失败: ${err.response?.data?.error || '请检查输入值'}`;
  } finally {
    isSavingBiz.value = false;
  }
};

// --- 组件挂载时，获取初始数据 ---
onMounted(() => {
  fetchGlobalSettings();
  fetchBizList();
});
</script>

<style scoped>
.rate-control-container { max-width: 900px; }
.page-title { font-size: 1.8em; color: #2c3e50; margin: 0 0 0.5rem 0; font-weight: 600; }
.page-description, .section-description { font-size: 1em; color: #6c757d; margin-top: 0; margin-bottom: 2rem; }
.section-description { margin-bottom: 1.5rem; }

/* 标签页样式 */
.tabs-nav { border-bottom: 2px solid #dee2e6; margin-bottom: 1.5rem; }
.tabs-nav button {
  padding: 0.8rem 1.5rem;
  border: none;
  background-color: transparent;
  cursor: pointer;
  font-size: 1.05em;
  font-weight: 500;
  color: #6c757d;
  position: relative;
  bottom: -2px;
  border-bottom: 2px solid transparent;
}
.tabs-nav button.active { color: #007bff; border-bottom-color: #007bff; }

.tab-panel {
  background-color: #fff;
  padding: 2rem;
  border-radius: 8px;
  border: 1px solid #e9ecef;
}

/* 表单和通用样式 */
.setting-item { display: flex; align-items: center; gap: 1rem; margin-bottom: 1.25rem; }
.setting-item label { font-weight: 500; color: #495057; width: 140px; flex-shrink: 0; }
.setting-item input {
  padding: 0.6rem 0.75rem; border-radius: 6px;
  border: 1px solid #ced4da; width: 100%; max-width: 250px;
}
.setting-item input:disabled { background-color: #e9ecef; cursor: not-allowed; }
.form-actions { margin-top: 2rem; padding-top: 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: flex-end; gap: 1rem; }
.button-primary, .button-secondary { padding: 0.6rem 1.2rem; border: none; border-radius: 5px; cursor: pointer; font-weight: 500; transition: background-color 0.2s; }
.button-primary { background-color: #007bff; color: white; }
.button-primary:disabled { background-color: #a0cfff; cursor: not-allowed; }
.button-secondary { background-color: #6c757d; color: white; }

/* 业务组选择器 */
.biz-selector {
  display: flex; align-items: center; gap: 1rem;
  margin-bottom: 2rem; padding-bottom: 1.5rem;
  border-bottom: 1px solid #e9ecef;
}
.biz-selector label { font-weight: 500; }
.biz-selector select { min-width: 250px; padding: 0.6rem 0.75rem; border-radius: 6px; border: 1px solid #ced4da; }

/* 状态消息 */
.status-message { padding: 1rem; margin-bottom: 1.5rem; border-radius: 5px; border: 1px solid; }
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; color: #0050b3; text-align: center; }
.status-message.error { background-color: #fff1f0; border-color: #ffa39e; color: #cf1322; }
.status-message.success { background-color: #d4edda; border-color: #c3e6cb; color: #155724; }
</style>