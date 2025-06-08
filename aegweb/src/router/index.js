// src/router/index.js
import { createRouter, createWebHistory } from 'vue-router';
import { authService } from '@/services/apiClient'; // 引入authService

const SearchView = () => import('../views/SearchView.vue');
const SetupAdminPage = () => import('../views/SetupAdminPage.vue');
const LoginPage = () => import('../views/LoginPage.vue');
const DashboardPage = () => import('../views/DashboardPage.vue');
const AdminBizConfigPage = () => import('../views/admin/AdminBizConfigPage.vue');
const AdminRateLimitSettingsPage = () => import('../views/admin/AdminRateLimitSettingsPage.vue');
const NotFoundPage = () => import('../views/NotFoundPage.vue');

const routes = [
  {
    path: '/',
    name: 'Search',
    component: SearchView,
  },
  {
    path: '/setup-admin',
    name: 'SetupAdmin',
    component: SetupAdminPage,
    beforeEnter: (to, from, next) => {
      if (authService.isAuthenticated()) {
        next({ name: 'AdminDashboard' });
      } else {
        next();
      }
    },
  },
  {
    path: '/login',
    name: 'Login',
    component: LoginPage,
    beforeEnter: (to, from, next) => {
      if (authService.isAuthenticated()) {
        next({ name: 'AdminDashboard' });
      } else {
        next();
      }
    },
  },
  {
    path: '/admin',
    meta: { requiresAuth: true, isAdminRoute: true }, // 标记此组路由需要认证和管理员权限
    children: [
      {
        path: 'dashboard', // 注意：子路由路径不以'/'开头
        name: 'AdminDashboard',
        component: DashboardPage,
      },
      {
        path: 'biz/:bizName', // 路径为 /admin/biz/:bizName
        name: 'AdminBizConfig',
        component: AdminBizConfigPage,
        props: true
      },
      {
        path: 'settings/rate-limit', // 路径为 /admin/settings/rate-limit
        name: 'AdminRateLimitSettings',
        component: AdminRateLimitSettingsPage,
      }
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

router.beforeEach((to, from, next) => {
  const isAuthenticated = authService.isAuthenticated();
  const userRole = authService.getRole();
  const needsAuth = to.matched.some(record => record.meta.requiresAuth);
  const needsAdmin = to.matched.some(record => record.meta.isAdminRoute);

  if (needsAuth && !isAuthenticated) {

    next({ name: 'Login', query: { redirect: to.fullPath } });
  } else if (needsAdmin && isAuthenticated) {
    const isAdmin = userRole && userRole.trim() === 'admin';

    if (isAdmin) {
      next();
    } else {
      console.warn(`[Router Guard] 权限不足: 路由 '${to.path}' 需要管理员角色。实际角色: '${userRole}'`);
      next({ name: 'Search' });
    }
  } else {
    next();
  }
});

export default router;