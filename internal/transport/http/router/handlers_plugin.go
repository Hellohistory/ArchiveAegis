// Package router file: internal/transport/http/router/handlers_plugin.go
package router

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/service/plugin_manager"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// registerPluginAdminRoutes 注册所有与插件管理相关的 API 端点。
func registerPluginAdminRoutes(group *gin.RouterGroup, deps Dependencies) {
	group.GET("/available", listAvailablePluginsHandler(deps.PluginManager))
	group.POST("/install", installPluginHandler(deps.PluginManager))
	group.POST("/instances", createInstanceHandler(deps.PluginManager))
	group.GET("/instances", listInstancesHandler(deps.PluginManager))
	group.DELETE("/instances/:instance_id", deleteInstanceHandler(deps.PluginManager))
	group.POST("/instances/:instance_id/start", startInstanceHandler(deps.PluginManager))
	group.POST("/instances/:instance_id/stop", stopInstanceHandler(deps.PluginManager))
	group.POST("/instances/:instance_id/reload", reloadInstanceHandler(deps.PluginManager))
}

// =============================================================================
//  插件管理处理器 (Plugin Management Handlers)
// =============================================================================

func listAvailablePluginsHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		availablePlugins := pluginManager.GetAvailablePlugins()
		if availablePlugins == nil {
			// 确保即使没有可用插件也返回一个空的 JSON 数组，而不是 null
			availablePlugins = make([]domain.PluginManifest, 0)
		}
		c.JSON(http.StatusOK, gin.H{"data": availablePlugins})
	}
}

func installPluginHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	type installPayload struct {
		PluginID string `json:"plugin_id" binding:"required"`
		Version  string `json:"version" binding:"required"`
	}
	return func(c *gin.Context) {
		var payload installPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}

		if err := pluginManager.Install(payload.PluginID, payload.Version); err != nil {
			_ = c.Error(fmt.Errorf("插件 '%s' v%s 安装失败: %w", payload.PluginID, payload.Version, err))
			return
		}
		// 安装是一个异步过程，这里只返回任务提交成功的消息
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件 '%s' v%s 已成功提交安装任务。", payload.PluginID, payload.Version)})
	}
}

func listInstancesHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instances, err := pluginManager.ListInstances()
		if err != nil {
			_ = c.Error(err)
			return
		}
		if instances == nil {
			// 确保即使没有实例也返回一个空的 JSON 数组
			instances = make([]domain.PluginInstance, 0)
		}
		c.JSON(http.StatusOK, gin.H{"data": instances})
	}
}

func createInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		type createPayload struct {
			DisplayName string `json:"display_name" binding:"required"`
			PluginID    string `json:"plugin_id" binding:"required"`
			Version     string `json:"version" binding:"required"`
			BizName     string `json:"biz_name" binding:"required"`
		}
		var payload createPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}

		instanceID, err := pluginManager.CreateInstance(payload.DisplayName, payload.PluginID, payload.Version, payload.BizName)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusCreated, gin.H{
			"message":     "插件实例创建成功",
			"instance_id": instanceID,
		})
	}
}

func deleteInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if err := pluginManager.DeleteInstance(instanceID); err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已成功删除。", instanceID)})
	}
}

func startInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if err := pluginManager.Start(instanceID); err != nil {
			_ = c.Error(fmt.Errorf("启动插件实例 '%s' 失败: %w", instanceID, err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已成功提交启动任务。", instanceID)})
	}
}

func stopInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if err := pluginManager.Stop(instanceID); err != nil {
			if errors.Is(err, plugin_manager.ErrInstanceNotRunning) {
				c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已处于停止状态。", instanceID)})
				return
			}
			_ = c.Error(fmt.Errorf("停止插件实例 '%s' 失败: %w", instanceID, err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已成功停止。", instanceID)})
	}
}

func reloadInstanceHandler(pluginManager *plugin_manager.PluginManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		instanceID := c.Param("instance_id")
		if err := pluginManager.Reload(instanceID); err != nil {
			_ = c.Error(fmt.Errorf("重载插件实例 '%s' 失败: %w", instanceID, err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("插件实例 '%s' 已成功重载。", instanceID)})
	}
}
