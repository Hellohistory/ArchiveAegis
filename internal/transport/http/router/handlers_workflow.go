// Package router file: internal/transport/http/router/handlers_workflow.go
package router

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/service/workflow"
	"net/http"

	"github.com/gin-gonic/gin"
)

// registerWorkflowExecutionRoutes 注册面向普通用户的工作流执行路由。
func registerWorkflowExecutionRoutes(group *gin.RouterGroup, deps Dependencies) {
	group.POST("/:workflow_id/run", runWorkflowHandler(deps.WorkflowService))
}

// registerWorkflowAdminRoutes 注册面向管理员的工作流管理（增删改查）路由。
func registerWorkflowAdminRoutes(group *gin.RouterGroup, deps Dependencies) {
	group.POST("/", createWorkflowHandler(deps.WorkflowService))
	group.GET("/", listWorkflowsHandler(deps.WorkflowService))
	group.GET("/:workflow_id", getWorkflowHandler(deps.WorkflowService))
	group.PUT("/:workflow_id", updateWorkflowHandler(deps.WorkflowService))
	group.DELETE("/:workflow_id", deleteWorkflowHandler(deps.WorkflowService))
}

// =============================================================================
//  工作流操作平面处理器 (Workflow Operation Plane Handler)
// =============================================================================

// runWorkflowHandler 触发一个工作流的执行。
func runWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	type runPayload struct {
		InitialParams map[string]any `json:"initial_params"`
	}
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var payload runPayload
		// 允许 payload 为空的情况 (err.Error() == "EOF")
		if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
			_ = c.Error(err)
			return
		}
		if payload.InitialParams == nil {
			payload.InitialParams = make(map[string]any)
		}

		finalAction, err := workflowService.RunWorkflow(c.Request.Context(), workflowID, payload.InitialParams)
		if err != nil {
			_ = c.Error(err)
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"status":       "completed",
			"workflow_id":  workflowID,
			"final_action": finalAction,
		})
	}
}

// =============================================================================
//  工作流管理处理器 (Workflow Admin Handlers)
// =============================================================================

// createWorkflowHandler 创建一个新的工作流定义（包括其节点和边）。
func createWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		var payload workflow.FullWorkflowPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}

		createdWorkflow, err := workflowService.CreateWorkflow(c.Request.Context(), payload.Workflow, payload.Nodes, payload.Edges)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusCreated, createdWorkflow)
	}
}

// listWorkflowsHandler 列出所有工作流的基本信息。
func listWorkflowsHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflows, err := workflowService.ListWorkflows(c.Request.Context())
		if err != nil {
			_ = c.Error(err)
			return
		}
		if workflows == nil {
			workflows = []domain.Workflow{}
		}
		c.JSON(http.StatusOK, workflows)
	}
}

// getWorkflowHandler 获取单个工作流的完整定义（包括节点和边）。
func getWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		getWorkflow, err := workflowService.GetWorkflow(c.Request.Context(), workflowID)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, getWorkflow)
	}
}

// updateWorkflowHandler 更新一个工作流的定义。
func updateWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var payload workflow.FullWorkflowPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		payload.Workflow.ID = workflowID

		updatedWorkflow, err := workflowService.UpdateWorkflow(c.Request.Context(), payload.Workflow, payload.Nodes, payload.Edges)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, updatedWorkflow)
	}
}

// deleteWorkflowHandler 删除一个工作流。
func deleteWorkflowHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		if err := workflowService.DeleteWorkflow(c.Request.Context(), workflowID); err != nil {
			_ = c.Error(err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
