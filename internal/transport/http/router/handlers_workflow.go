// Package router file: internal/transport/http/router/handlers_workflow.go
package router

import (
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/service/workflow"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// registerWorkflowExecutionRoutes 注册面向普通用户的工作流执行路由。
func registerWorkflowExecutionRoutes(group *gin.RouterGroup, deps Dependencies) {
	group.POST("/:workflow_id/run", runWorkflowHandler(deps.WorkflowService))
}

// registerWorkflowAdminRoutes 注册面向管理员的工作流管理（增删改查）路由。
func registerWorkflowAdminRoutes(group *gin.RouterGroup, deps Dependencies) {
	// --- 工作流级别的 CRUD ---
	group.POST("/", createWorkflowHandler(deps.WorkflowService))
	group.GET("/", listWorkflowsHandler(deps.WorkflowService))
	group.GET("/:workflow_id", getWorkflowHandler(deps.WorkflowService))
	group.PUT("/:workflow_id", updateWorkflowHandler(deps.WorkflowService))
	group.DELETE("/:workflow_id", deleteWorkflowHandler(deps.WorkflowService))

	// --- 节点级别的 CRUD ---
	nodesGroup := group.Group("/:workflow_id/nodes")
	{
		nodesGroup.POST("/", addNodeHandler(deps.WorkflowService))
		nodesGroup.PUT("/:node_id", updateNodeHandler(deps.WorkflowService))
		nodesGroup.DELETE("/:node_id", deleteNodeHandler(deps.WorkflowService))
	}

	// --- 边级别的 CRUD ---
	edgesGroup := group.Group("/:workflow_id/edges")
	{
		edgesGroup.POST("/", addEdgeHandler(deps.WorkflowService))
		edgesGroup.PUT("/:edge_id", updateEdgeHandler(deps.WorkflowService))
		edgesGroup.DELETE("/:edge_id", deleteEdgeHandler(deps.WorkflowService))
	}
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

// =============================================================================
//  工作流节点和边精细化管理处理器
// =============================================================================

// --- Node Handlers ---

func addNodeHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var node domain.WorkflowNode
		if err := c.ShouldBindJSON(&node); err != nil {
			_ = c.Error(err)
			return
		}

		createdNode, err := workflowService.AddNode(c.Request.Context(), workflowID, node)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusCreated, createdNode)
	}
}

func updateNodeHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		nodeID := c.Param("node_id")
		var node domain.WorkflowNode
		if err := c.ShouldBindJSON(&node); err != nil {
			_ = c.Error(err)
			return
		}

		updatedNode, err := workflowService.UpdateNode(c.Request.Context(), workflowID, nodeID, node)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, updatedNode)
	}
}

func deleteNodeHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		nodeID := c.Param("node_id")

		if err := workflowService.DeleteNode(c.Request.Context(), workflowID, nodeID); err != nil {
			_ = c.Error(err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// --- Edge Handlers ---

func addEdgeHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var edge domain.WorkflowEdge
		if err := c.ShouldBindJSON(&edge); err != nil {
			_ = c.Error(err)
			return
		}

		createdEdge, err := workflowService.AddEdge(c.Request.Context(), workflowID, edge)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusCreated, createdEdge)
	}
}

func updateEdgeHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		edgeID := c.Param("edge_id") // 注意：边ID是自增整数，需要类型转换

		var edge domain.WorkflowEdge
		if err := c.ShouldBindJSON(&edge); err != nil {
			_ = c.Error(err)
			return
		}

		// Gin的Param返回字符串，需要转换为int64
               id, err := strconv.ParseInt(edgeID, 10, 64)
               if err != nil {
                       _ = c.Error(fmt.Errorf("无效的边ID: %s", edgeID))
                       return
               }

		updatedEdge, err := workflowService.UpdateEdge(c.Request.Context(), workflowID, id, edge)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, updatedEdge)
	}
}

func deleteEdgeHandler(workflowService *workflow.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		edgeID := c.Param("edge_id")

               id, err := strconv.ParseInt(edgeID, 10, 64)
               if err != nil {
                       _ = c.Error(fmt.Errorf("无效的边ID: %s", edgeID))
                       return
               }

		if err := workflowService.DeleteEdge(c.Request.Context(), workflowID, id); err != nil {
			_ = c.Error(err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}
