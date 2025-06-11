// src/router/index.js

import { createRouter, createWebHistory } from 'vue-router';
import { authService } from '@/services/apiClient';
import { systemStatus } from '@/services/systemStatus';


const SetupAdminPage = () => import('../views/login/SetupAdminPage.vue');
const LoginPage = () => import('../views/login/LoginPage.vue');
const NotFoundPage = () => import('../views/NotFoundPage.vue');

const AdminLayout = () => import('../views/admin/AdminLayout.vue');

const AdminDashboard = () => import('../views/admin/refactored/AdminDashboard.vue');
const AdminBizManagement = () => import('../views/admin/refactored/AdminBizManagement.vue');
const AdminRateControl = () => import('../views/admin/refactored/AdminRateControl.vue');
const BizDetailConfig = () => import('../views/admin/refactored/BizDetailConfig.vue');

const SearchPage = () => import('../views/searchbase/SearchPage.vue');
const ResultsPage = () => import('../views/searchbase/ResultsPage.vue');


const routes = [
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
    path: '/',
    redirect: () => {
      if (authService.isAuthenticated()) {
        return authService.getRole() === 'admin' ? '/admin' : '/search';
      }
      return '/login';
    },
  },
  {
    path: '/search',
    name: 'Search',
    component: SearchPage,
    meta: { requiresAuth: true }
  },
  {
    path: '/search/results',
    name: 'Results',
    component: ResultsPage,
    meta: { requiresAuth: true }
  },
  {
    path: '/admin',
    component: AdminLayout,
    meta: { requiresAuth: true, isAdminRoute: true },
    children: [
      {
        path: '',
        redirect: { name: 'AdminDashboard' }
      },
      // 仪表盘路由
      {
        path: 'dashboard',
        name: 'AdminDashboard',
        component: AdminDashboard,
      },
      // 业务管理页路由
      {
        path: 'biz-management',
        name: 'AdminBizManagement',
        component: AdminBizManagement,
      },
      // 业务详情配置页
      {
        path: 'biz/config/:bizName',
        name: 'AdminBizDetailConfig',
        component: BizDetailConfig,
        props: true,
      },
      // 速率控制页
      {
        path: 'rate-control',
        name: 'AdminRateControl',
        component: AdminRateControl,
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
  if (systemStatus.value === 'pending') {

  } else if (systemStatus.value === 'needs_setup') {
    if (to.name !== 'SetupAdmin') {
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
      const destination = userRole === 'admin' ? { name: 'AdminDashboard' } : { name: 'Search' };
      return next(destination);
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