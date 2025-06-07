<!--src/components/CardView.vue-->
<template>
  <div class="card-grid">
    <div v-for="(item, index) in results" :key="index" class="card">
      <div v-if="config.imageUrl && item[config.imageUrl]" class="card-image-wrapper">
        <img :src="item[config.imageUrl]" alt="card-image" class="card-image"/>
      </div>
      <div class="card-content">
        <h3 v-if="config.title" class="card-title">{{ item[config.title] }}</h3>
        <p v-if="config.subtitle" class="card-subtitle">{{ item[config.subtitle] }}</p>
        <p v-if="config.description" class="card-description">{{ item[config.description] }}</p>
        <span v-if="config.tag" class="card-tag">{{ item[config.tag] }}</span>
      </div>
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
    required: true, // 接收 card 的 binding 配置
  },
});
</script>

<style scoped>
.card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 1.5rem;
}
.card {
  border: 1px solid #e0e0e0;
  border-radius: 8px;
  background-color: #fff;
  overflow: hidden;
  box-shadow: 0 4px 12px rgba(0,0,0,0.08);
  transition: transform 0.2s ease-in-out, box-shadow 0.2s ease-in-out;
}
.card:hover {
  transform: translateY(-5px);
  box-shadow: 0 6px 16px rgba(0,0,0,0.12);
}
.card-image-wrapper {
    width: 100%;
    height: 180px;
    background-color: #f5f5f5;
}
.card-image {
  width: 100%;
  height: 100%;
  object-fit: cover;
}
.card-content {
  padding: 1rem 1.25rem;
}
.card-title {
  margin: 0 0 0.5rem;
  font-size: 1.2rem;
  font-weight: 600;
  color: #333;
}
.card-subtitle {
  margin: 0 0 1rem;
  color: #555;
  font-size: 0.9rem;
}
.card-description {
  font-size: 1rem;
  color: #444;
  line-height: 1.5;
}
.card-tag {
    display: inline-block;
    padding: 0.25rem 0.6rem;
    background-color: #e7f3ff;
    color: #007bff;
    border-radius: 12px;
    font-size: 0.8rem;
    margin-top: 1rem;
    font-weight: 500;
}
</style>