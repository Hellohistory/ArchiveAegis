// src/router/index.js
// src/router/index.js

import { createRouter, createWebHistory } from 'vue-router';
import { authService } from '@/services/apiClient';
import { systemStatus } from '@/services/systemStatus';

// --- 现有组件导入保持不变 ---
const SearchView = () => import('../views/SearchView.vue');
const SetupAdminPage = () => import('../views/SetupAdminPage.vue');
const LoginPage = () => import('../views/LoginPage.vue');
const NotFoundPage = () => import('../views/NotFoundPage.vue');

// ========== 开始修改: 调整 Admin 区域的组件导入 ==========

// 保持 AdminLayout 的导入方式
const AdminLayout = () => import('../views/admin/AdminLayout.vue');

// 旧的页面组件，我们将替换它们
// const DashboardPage = () => import('../views/DashboardPage.vue');
// const AdminBizConfigPage = () => import('../views/admin/AdminBizConfigPage.vue');
// const AdminRateLimitSettingsPage = () => import('../views/admin/AdminRateLimitSettingsPage.vue');

// 为新页面创建导入（您可以先创建空的 .vue 文件）
const AdminDashboard = () => import('../views/admin/refactored/AdminDashboard.vue');
const AdminBizManagement = () => import('../views/admin/refactored/AdminBizManagement.vue');
const AdminRateControl = () => import('../views/admin/refactored/AdminRateControl.vue');
const BizDetailConfig = () => import('../views/admin/refactored/BizDetailConfig.vue');

// ========== 修改结束 ==========


const routes = [
  // --- '/' , '/setup-admin', '/login' 路由保持不变 ---
  {
    path: '/',
    name: 'Search',
    component: SearchView,
  },
  {
    path: '/setup-admin',
    name: 'SetupAdmin',
    component: SetupAdminPage,
  },
  {
    path: '/login',
    name: 'Login',
    component: LoginPage,
  },
  {
    path: '/admin',
    component: AdminLayout, // 使用我们修改后的 AdminLayout
    meta: { requiresAuth: true, isAdminRoute: true },
    children: [
      // ========== 开始修改: 重新定义 Admin 子路由 ==========

      // 1. 默认重定向到新的仪表盘
      {
        path: '',
        redirect: { name: 'AdminDashboard' }
      },
      // 2. 新的仪表盘路由
      {
        path: 'dashboard',
        name: 'AdminDashboard',
        component: AdminDashboard,
      },
      // 3. 新的业务管理页路由 (卡片列表页)
      {
        path: 'biz-management',
        name: 'AdminBizManagement',
        component: AdminBizManagement,
      },
      // 4. 新的业务详情配置页路由
      {
        path: 'biz/config/:bizName',
        name: 'AdminBizDetailConfig', // 新的路由名称
        component: BizDetailConfig,    // 指向新的详情页组件
        props: true,
      },
      // 5. 新的速率控制页路由
      {
        path: 'rate-control',
        name: 'AdminRateControl',
        component: AdminRateControl,
      }

      // ========== 修改结束 ==========
    ],
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: NotFoundPage,
  }
];

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
});


// --- 您的 router.beforeEach 守卫逻辑非常完善，保持不变 ---
router.beforeEach((to, from, next) => {
  if (systemStatus.value === 'pending') {
    // 如果仍在等待初始化，可以显示加载或稍后重试
    // 更好的做法是在 App.vue 中处理，这里可以暂时跳过
    // 这里假设 App.vue/main.js 会确保 status 不是 pending
  } else if (systemStatus.value === 'needs_setup') {
    if (to.name !== 'SetupAdmin') { // 允许访问安装页
      return next({ name: 'SetupAdmin' });
    }
  } else if (systemStatus.value === 'ready_for_login') {
    if (to.name === 'SetupAdmin') {
      return next({ name: 'Login' });
    }
  }

  const isAuthenticated = authService.isAuthenticated();
  const userRole = authService.getRole();

  if (isAuthenticated && (to.name === 'Login' || to.name === 'SetupAdmin')) {
      return next({ name: 'AdminDashboard' });
  }

  const needsAuth = to.matched.some(record => record.meta.requiresAuth);
  const needsAdmin = to.matched.some(record => record.meta.isAdminRoute);

  if (needsAuth && !isAuthenticated) {
    return next({ name: 'Login', query: { redirect: to.fullPath } });
  }

  if (needsAdmin && isAuthenticated && userRole !== 'admin') {
    return next({ name: 'Search' });
  }

  next();
});

export default router;