// src/router/index.js
import { createRouter, createWebHistory } from 'vue-router';
import SearchView from '../views/SearchView.vue';
import SetupAdminPage from '../views/SetupAdminPage.vue';
import LoginPage from '../views/LoginPage.vue';

const routes = [
  {
    path: '/',
    name: 'Search',
    component: SearchView,
  },

  // 首次安装管理员账户的页面。
  {
    path: '/setup-admin',
    name: 'SetupAdmin',
    component: SetupAdminPage,
    beforeEnter: (to, from, next) => {
      // 如果用户已登录，则不应再访问安装页面。
      if (localStorage.getItem('authToken')) {
        next({ name: 'AdminDashboard' }); // 已登录的管理员应被导向其仪表盘
      } else {
        next();
      }
    },
  },

  // 登录页面。
  {
    path: '/login',
    name: 'Login',
    component: LoginPage,
    beforeEnter: (to, from, next) => {
      // 如果用户已登录，则不应再访问登录页面。
      if (localStorage.getItem('authToken')) {
        next({ name: 'AdminDashboard' });
      } else {
        next();
      }
    },
  },

  // 为管理员创建一个分组路由 ('/admin')。
  // 所有子路由都会自动继承 meta 标记和相应的路由守卫逻辑。
  {
    path: '/admin',
    meta: { requiresAuth: true, isAdminRoute: true }, // 标记此组路由需要认证和管理员权限
    children: [
      {
        // 管理员仪表盘，路径为 /admin/dashboard
        path: '/admin/dashboard',
        name: 'AdminDashboard',
        component: () => import('../views/DashboardPage.vue'),
      },
      {
        // 业务配置页面，路径为 /admin/configure-biz/:bizName
        path: 'configure-biz/:bizName',
        name: 'AdminBizConfig',
        component: () => import('../views/admin/AdminBizConfigPage.vue'),
        props: true // 将路由参数 (bizName) 作为 props 传递给组件
      },
    ],
  },

  // 捕获所有未匹配路径的 404 页面。
  {
    path: '/:pathMatch(.*)*',
    name: 'NotFound',
    component: () => import('../views/NotFoundPage.vue'),
  }
];

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes,
});

router.beforeEach((to, from, next) => {
  const isAuthenticated = !!localStorage.getItem('authToken');
  const userRoleFromStorage = localStorage.getItem('userRole');

  console.log("-----------------------------------------");
  console.log(`[Router Guard] Navigating to: ${to.path}`);
  console.log(`[Router Guard] isAuthenticated: ${isAuthenticated}`);

  if (userRoleFromStorage === null) {
    console.log("[Router Guard] userRole from localStorage is: null");
  } else {
    console.log(`[Router Guard] userRole from localStorage: '${userRoleFromStorage}'`);
    console.log(`[Router Guard] Type of userRole: ${typeof userRoleFromStorage}`);
    console.log(`[Router Guard] Length of userRole: ${userRoleFromStorage.length}`);
    let charCodes = [];
    for (let i = 0; i < userRoleFromStorage.length; i++) {
      charCodes.push(userRoleFromStorage.charCodeAt(i));
    }
    console.log(`[Router Guard] Char codes of userRole: [${charCodes.join(', ')}]`);
  }

  const needsAdmin = to.matched.some(record => record.meta.isAdminRoute);
  console.log(`[Router Guard] Route requires admin (check matched): ${needsAdmin}`);

  if (to.matched.some(record => record.meta.requiresAuth) && !isAuthenticated) {
    console.log("[Router Guard] Decision: Not authenticated, redirecting to Login.");
    next({ name: 'Login', query: { redirect: to.fullPath } });

  } else if (needsAdmin) {
    const isAdmin = userRoleFromStorage === 'admin';
    console.log(`[Router Guard] Is admin check: (userRoleFromStorage === 'admin') is ${isAdmin}`);

    if (!isAdmin) {
      console.warn(`[Router Guard] 权限不足: 路由 '${to.path}' 需要管理员角色。实际角色: '${userRoleFromStorage}'`);
      // 如果非管理员尝试访问管理员页面，可以导向主页或显示一个“无权限”页面
      next({ name: 'Search' });
    } else {
      console.log("[Router Guard] Decision: Admin access granted.");
      next();
    }

  } else {
    console.log("[Router Guard] Decision: Proceeding with navigation.");
    next();
  }
  console.log("-----------------------------------------");
});

export default router;