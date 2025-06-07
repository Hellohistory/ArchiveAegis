<!--src/views/NotFoundPage.vue-->
<template>
  <div class="not-found-container">
    <div class="status-code">404</div>
    <div class="message">哎呀！页面飞走了...</div>
    <div class="description">
      您访问的页面不存在，或者已经被移动到其他地方。
    </div>
    <router-link to="/dashboard" class="home-button">返回仪表盘</router-link>
  </div>
</template>

<script setup>
</script>

<style scoped>
.not-found-container {
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  height: 80vh;
  text-align: center;
  color: #333;
}

.status-code {
  font-size: 10rem;
  font-weight: 700;
  color: #e0e0e0;
  line-height: 1;
  letter-spacing: 0.1em;
  text-shadow: 2px 2px 0 #fff, 4px 4px 0 #ddd;
}

.message {
  font-size: 2rem;
  font-weight: 500;
  margin-top: 20px;
  color: #555;
}

.description {
  font-size: 1.1rem;
  color: #777;
  margin-top: 10px;
  max-width: 400px;
}

.home-button {
  display: inline-block;
  margin-top: 40px;
  padding: 12px 25px;
  background-color: #007bff;
  color: white;
  border-radius: 50px;
  text-decoration: none;
  font-weight: 500;
  font-size: 1rem;
  transition: background-color 0.3s, transform 0.3s;
  box-shadow: 0 4px 10px rgba(0, 123, 255, 0.3);
}

.home-button:hover {
  background-color: #0056b3;
  transform: translateY(-2px);
}
</style>