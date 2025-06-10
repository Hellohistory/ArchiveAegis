<template>
  <div class="dashboard-container">
    <h1 class="page-title">仪表盘</h1>
    <p class="page-description">
      欢迎来到 ArchiveAegis 管理后台。
    </p>

    <div class="placeholder-panel">
      <div class="placeholder-icon">📈</div>
      <h2 class="placeholder-title">系统监控模块</h2>
      <p class="placeholder-text">
        此区域将用于展示系统实时性能、API 调用频率、查询热点等关键指标。
        <br />
        该功能正在开发中，敬请期待。
      </p>
    </div>
  </div>
</template>

<script setup>

</script>

<style scoped>
.dashboard-container {
  max-width: 1200px;
}

.page-title {
  font-size: 1.8em;
  color: #2c3e50;
  margin: 0 0 0.5rem 0;
  font-weight: 600;
}

.page-description {
  font-size: 1em;
  color: #6c757d;
  margin-top: 0;
  margin-bottom: 2.5rem;
}

.placeholder-panel {
  background-color: #ffffff;
  border: 1px dashed #d0d7de;
  border-radius: 12px;
  padding: 3rem 2rem;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.placeholder-icon {
  font-size: 3.5rem;
  line-height: 1;
  margin-bottom: 1.5rem;
  opacity: 0.7;
}

.placeholder-title {
  font-size: 1.5em;
  font-weight: 600;
  color: #34495e;
  margin: 0 0 0.75rem 0;
}

.placeholder-text {
  font-size: 1em;
  color: #6c757d;
  line-height: 1.6;
  max-width: 500px;
  margin: 0;
}
</style>