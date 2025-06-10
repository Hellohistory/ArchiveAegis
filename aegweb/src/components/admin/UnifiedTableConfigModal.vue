<!--src/components/admin/UnifiedTableConfigModal.vue-->
<template>
  <div v-if="visible" class="modal-overlay" @click.self="close">
    <div class="modal-content">
      <header class="modal-header">
        <h3>配置表: <span class="table-name-highlight">{{ tableName }}</span></h3>
        <button @click="close" class="close-button" aria-label="关闭">&times;</button>
      </header>

      <main class="modal-body">
        <div v-if="isLoading" class="status-message loading">加载中...</div>
        <div v-if="loadError" class="status-message error">{{ loadError }}</div>

        <div v-if="!isLoading && !loadError">
          <div class="wizard-steps">
            <button
              v-for="n in maxStep"
              :key="n"
              :class="{ active: step === n }"
              @click="step = n"
            >
              {{ n }}. {{ stepLabels[n] }}
            </button>
          </div>

          <Step1BasicSettings v-show="step === 1" v-model:config="viewConfig" />

          <Step2FieldConfig v-show="step === 2" v-model:fields="fieldSettings" />

          <Step3ViewBinding
            v-show="step === 3"
            v-model:config="viewConfig"
            :returnable-fields="returnableFields"
          />
        </div>
      </main>

      <footer class="modal-footer">
        <button @click="prevStep" class="button-secondary" :disabled="step === 1">
          上一步
        </button>
        <button v-if="step < maxStep" @click="nextStep" class="button-primary">
          下一步
        </button>
        <button v-else @click="save" class="button-primary" :disabled="isLoading">
          保存配置
        </button>
      </footer>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, computed, watch } from 'vue';
import apiClient from '../../services/apiClient';
import { ENDPOINTS } from '@/services/apiEndpoints.js';

import Step1BasicSettings from './UnifiedTableConfigModalStep/Step1BasicSettings.vue';
import Step2FieldConfig from './UnifiedTableConfigModalStep/Step2FieldConfig.vue';
import Step3ViewBinding from './UnifiedTableConfigModalStep/Step3ViewBinding.vue';


const props = defineProps({
  visible: Boolean,
  bizName: String,
  tableName: String,
});
const emit = defineEmits(['update:visible', 'saved']);

const step = ref(1);
const maxStep = 3;
const stepLabels = { 1: '基本设置', 2: '字段属性', 3: '字段绑定' };
const nextStep = () => { if (step.value < maxStep) step.value++; };
const prevStep = () => { if (step.value > 1) step.value--; };

const isLoading = ref(false);
const loadError = ref(null);

const fieldSettings = ref([]);
const viewConfig = reactive({
  view_name: '',
  view_type: 'cards',
  display_name: '',
  is_default: true,
  binding: {
    card: { title: '', subtitle: '', description: '', tag: '', details: [] },
    table: { columns: [] },
    list: { columns: [] },
    kanban: { groupBy: '', cardFields: { title: '', tag: '' } },
    calendar: { dateField: '', titleField: '' }
  }
});
let allBizViews = {};

const returnableFields = computed(() =>
  fieldSettings.value.filter(f => f.is_returnable).map(f => f.field_name)
);

const close = () => emit('update:visible', false);

const createDefaultView = tableName => ({
  view_name: `${tableName}_default_view`,
  view_type: 'cards',
  display_name: `${tableName} 默认视图`,
  is_default: true,
  binding: {
    card: { title: '', subtitle: '', description: '', tag: '', details: [] },
    table: { columns: [] },
    list: { columns: [] },
    kanban: { groupBy: '', cardFields: { title: '', tag: '' } },
    calendar: { dateField: '', titleField: '' }
  }
});

const fetchData = async () => {
  if (!props.visible) return;
  isLoading.value = true; loadError.value = null;
  try {
    const [colsRes, cfgRes, viewsRes] = await Promise.all([
      apiClient.get(ENDPOINTS.GET_PHYSICAL_COLUMNS(props.bizName, props.tableName)),
      apiClient.get(ENDPOINTS.GET_BIZ_CONFIG(props.bizName)).catch(e => e),
      apiClient.get(ENDPOINTS.GET_BIZ_VIEWS(props.bizName)).catch(e => e)
    ]);

    if (colsRes.response || colsRes instanceof Error) {
      throw new Error(`无法获取表 '${props.tableName}' 的物理列`);
    }
    const cols = colsRes.data;
    const bizCfg = (cfgRes.response?.status === 404 || cfgRes instanceof Error) ? {} : cfgRes.data;
    allBizViews = (viewsRes.response?.status === 404 || viewsRes instanceof Error) ? {} : viewsRes.data;

    const existingFields = bizCfg.tables?.[props.tableName]?.fields || {};
    const suggestType = n => {
      const l = n.toLowerCase();
      if (l.includes('date') || l.includes('time') || l.endsWith('_at')) return 'date';
      if (l.includes('id') || l.includes('num') || l.includes('count') || l.includes('size')) return 'number';
      return 'string';
    };
    fieldSettings.value = cols.map(name => ({
      field_name: name,
      is_searchable: existingFields[name]?.is_searchable || false,
      is_returnable: existingFields[name]?.is_returnable || false,
      dataType: existingFields[name]?.dataType || suggestType(name),
    }));

    const tableViews = allBizViews[props.tableName] || [];
    const target = tableViews.find(v => v.is_default) || tableViews[0] || createDefaultView(props.tableName);
    Object.assign(viewConfig, createDefaultView(props.tableName), target);

  } catch (err) {
    console.error(err);
    loadError.value = `加载配置失败: ${err.message}`;
  } finally {
    isLoading.value = false;
  }
};
watch(() => props.visible, v => v && fetchData(), { immediate: true });

const save = async () => {
  isLoading.value = true;
  try {
    const clean = JSON.parse(JSON.stringify(viewConfig));
    Object.keys(clean.binding).forEach(k => {
      if (k !== viewConfig.view_type) delete clean.binding[k];
    });
    const views = allBizViews[props.tableName] || [];
    const idx = views.findIndex(v => v.view_name === clean.view_name);
    if (idx > -1) views[idx] = clean; else views.push(clean);
    allBizViews[props.tableName] = views;

    await apiClient.put(ENDPOINTS.UPDATE_TABLE_FIELDS(props.bizName, props.tableName), fieldSettings.value);
    await apiClient.put(ENDPOINTS.UPDATE_BIZ_VIEWS(props.bizName), allBizViews);

    emit('saved');
    close();
  } catch (err) {
    console.error(err);
    alert(`保存失败: ${err.response?.data?.error || err.message}`);
  } finally {
    isLoading.value = false;
  }
};
</script>

<style scoped>
.modal-overlay { position: fixed; top: 0; left: 0; width: 100%; height: 100%; background: rgba(0,0,0,0.6); display: flex; align-items: center; justify-content: center; z-index: 1000; }
.modal-content { background: #fff; border-radius: 8px; width: 90%; max-width: 900px; display: flex; flex-direction: column; max-height: 90vh; box-shadow: 0 5px 15px rgba(0,0,0,0.3); }
.modal-header { padding: 1rem 1.5rem; border-bottom: 1px solid #e9ecef; display: flex; justify-content: space-between; align-items: center; flex-shrink: 0; }
.modal-header h3 { margin: 0; font-size: 1.25rem; }
.table-name-highlight { color: #007bff; font-weight: bold; }
.close-button { background: none; border: none; font-size: 1.75rem; cursor: pointer; color: #6c757d; line-height: 1; padding: 0.5rem; }
.modal-body { padding: 1.5rem; overflow-y: auto; }
.modal-footer { padding: 1rem 1.5rem; border-top: 1px solid #e9ecef; display: flex; justify-content: space-between; align-items: center; background-color: #f8f9fa; flex-shrink: 0; }
.modal-footer button { margin-left: 0.75rem; }

.wizard-steps { display: flex; gap: 0.5rem; margin-bottom: 1.5rem; }
.wizard-steps button { flex: 1; padding: 0.75rem; border: 1px solid #dee2e6; background: #e9ecef; cursor: pointer; transition: background-color 0.2s, color 0.2s; border-radius: 4px; }
.wizard-steps button.active { background: #007bff; color: #fff; border-color: #007bff; }
.status-message { padding: 1rem; margin: 1rem 0; border-radius: 5px; border: 1px solid; text-align: center; }
.status-message.loading { background-color: #e6f7ff; border-color: #91d5ff; color: #004085; }
.status-message.error { background-color: #f8d7da; border-color: #f5c6cb; color: #721c24; }
.status-message.info { background-color: #f8f9fa; border-color: #dee2e6; color: #383d41; }

.button-primary { background-color: #007bff; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: 1px solid #007bff; cursor: pointer; transition: background-color 0.2s; }
.button-primary:hover:not(:disabled) { background-color: #0056b3; }
.button-primary:disabled { background-color: #a0cfff; border-color: #a0cfff; cursor: not-allowed; }
.button-secondary { background-color: #6c757d; color: white; padding: 0.6rem 1.2rem; border-radius: 5px; border: 1px solid #6c757d; cursor: pointer; transition: background-color 0.2s; }
.button-secondary:hover:not(:disabled) { background-color: #5a6268; }
.button-secondary:disabled { background-color: #e2e6ea; border-color: #e2e6ea; cursor: not-allowed; }
</style>