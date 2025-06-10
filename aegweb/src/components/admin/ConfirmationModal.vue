<template>
  <Teleport to="body">
    <Transition name="modal-fade">
      <div v-if="visible" class="modal-overlay" @click.self="handleCancel">
        <div class="modal-content">
          <h3 class="modal-title">{{ title }}</h3>
          <p class="modal-message whitespace-pre-line">{{ message }}</p>
          <div class="modal-actions">
            <button @click="handleCancel" class="button-secondary">取消</button>
            <button @click="handleConfirm" class="button-danger">确认</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
const props = defineProps({
  visible: {
    type: Boolean,
    required: true,
  },
  title: {
    type: String,
    default: '请确认',
  },
  message: {
    type: String,
    required: true,
  },
});

const emit = defineEmits(['update:visible', 'confirm', 'cancel']);

const handleConfirm = () => {
  emit('confirm');
  emit('update:visible', false);
};

const handleCancel = () => {
  emit('cancel');
  emit('update:visible', false);
};
</script>

<style scoped>
.modal-overlay {
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 1000;
}

.modal-content {
  background: white;
  padding: 2rem;
  border-radius: 8px;
  box-shadow: 0 5px 15px rgba(0, 0, 0, 0.3);
  width: 90%;
  max-width: 450px;
}

.modal-title {
  margin-top: 0;
  margin-bottom: 1rem;
  font-size: 1.5em;
  color: #2c3e50;
}

.modal-message {
  margin-bottom: 2rem;
  color: #333;
  font-size: 1.1em;
  line-height: 1.6;
}

/* 让 \n 换行符生效 */
.whitespace-pre-line {
  white-space: pre-line;
}

.modal-actions {
  display: flex;
  justify-content: flex-end;
  gap: 1rem;
}

.button-secondary, .button-danger {
  padding: 0.6rem 1.2rem;
  border-radius: 5px;
  border: none;
  cursor: pointer;
  font-weight: 500;
  transition: all 0.2s;
}

.button-secondary {
  background-color: #f0f0f0;
  border: 1px solid #ccc;
  color: #333;
}
.button-secondary:hover {
  background-color: #e0e0e0;
}

.button-danger {
  background-color: #dc3545;
  color: white;
}
.button-danger:hover {
  background-color: #c82333;
}

/* 动画效果 */
.modal-fade-enter-active, .modal-fade-leave-active {
  transition: opacity 0.3s ease;
}
.modal-fade-enter-from, .modal-fade-leave-to {
  opacity: 0;
}
</style>