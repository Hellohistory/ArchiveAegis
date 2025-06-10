// src/services/systemStatus.js
import { ref } from 'vue';

/**
 * 全局系统状态
 * - 'pending': 初始状态，正在查询
 * - 'needs_setup': 系统需要安装初始管理员
 * - 'ready_for_login': 系统已安装，准备好登录
 */
export const systemStatus = ref('pending');