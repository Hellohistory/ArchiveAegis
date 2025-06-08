// src/services/apiClient.js
import axios from 'axios';

const apiClient = axios.create({
  baseURL: '/api',
  timeout: 10000,
});

const authService = {
  login(token, user) {
    localStorage.setItem('authToken', token);
    if (user) {
      localStorage.setItem('username', user.username);
      localStorage.setItem('userRole', user.role);
    }
  },
  logout() {
    localStorage.removeItem('authToken');
    localStorage.removeItem('username');
    localStorage.removeItem('userRole');
    if (window.location.pathname !== '/login') {
      window.location.href = '/login';
    }
  },
  getToken() {
    return localStorage.getItem('authToken');
  },
  isAuthenticated() {
    return !!this.getToken();
  },
  getUsername() {
    return localStorage.getItem('username');
  },
  getRole() {
    return localStorage.getItem('userRole');
  }
};

apiClient.interceptors.request.use(
  (config) => {
    const token = authService.getToken();
    const url = config.url || '';
    const isAuthRoute = url.includes('/setup') || url.includes('/login');

    if (token && !isAuthRoute) {
      config.headers.Authorization = `Bearer ${token}`;
    }

    if (isAuthRoute && config.method === 'post') {
      config.headers['Content-Type'] = 'application/x-www-form-urlencoded';
    } else {
      config.headers['Content-Type'] = 'application/json';
    }
    return config;
  },
  (error) => Promise.reject(error),
);

apiClient.interceptors.response.use(
  (response) => response,
  (error) => {
    const { response } = error;
    if (response && (response.status === 401 || response.status === 403)) {
      authService.logout();
    }
    return Promise.reject(error);
  },
);

export default apiClient;
export { authService };