// Package router file: internal/transport/http/router/handlers_workflow.go
package router

import (
	"ArchiveAegis/internal/cluster"
	"ArchiveAegis/internal/core/domain"
	"ArchiveAegis/internal/service/workflow"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// registerWorkflowExecutionRoutes 注册面向普通用户的工作流执行路由。
func registerWorkflowExecutionRoutes(group *gin.RouterGroup, deps Dependencies) {
	group.POST("/:workflow_id/run", runWorkflowHandler(deps.WorkflowService, deps.ClusterManager))
}

// registerWorkflowAdminRoutes 注册面向管理员的工作流管理（增删改查）路由。
func registerWorkflowAdminRoutes(group *gin.RouterGroup, deps Dependencies) {
	// --- 工作流级别的 CRUD ---
	group.POST("/", createWorkflowHandler(deps.WorkflowService, deps.ClusterManager))
	group.GET("/", listWorkflowsHandler(deps.WorkflowService))
	group.GET("/:workflow_id", getWorkflowHandler(deps.WorkflowService))
	group.PUT("/:workflow_id", updateWorkflowHandler(deps.WorkflowService, deps.ClusterManager))
	group.DELETE("/:workflow_id", deleteWorkflowHandler(deps.WorkflowService, deps.ClusterManager))

	// --- 节点级别的 CRUD ---
	nodesGroup := group.Group("/:workflow_id/nodes")
	{
		nodesGroup.POST("/", addNodeHandler(deps.WorkflowService, deps.ClusterManager))
		nodesGroup.PUT("/:node_id", updateNodeHandler(deps.WorkflowService, deps.ClusterManager))
		nodesGroup.DELETE("/:node_id", deleteNodeHandler(deps.WorkflowService, deps.ClusterManager))
	}

	// --- 边级别的 CRUD ---
	edgesGroup := group.Group("/:workflow_id/edges")
	{
		edgesGroup.POST("/", addEdgeHandler(deps.WorkflowService, deps.ClusterManager))
		edgesGroup.PUT("/:edge_id", updateEdgeHandler(deps.WorkflowService, deps.ClusterManager))
		edgesGroup.DELETE("/:edge_id", deleteEdgeHandler(deps.WorkflowService, deps.ClusterManager))
	}
}

// =============================================================================
//  工作流操作平面处理器 (Workflow Operation Plane Handler)
// =============================================================================

// runWorkflowHandler 触发一个工作流的执行。
func runWorkflowHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	type runPayload struct {
		InitialParams map[string]any `json:"initial_params"`
	}
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		if clusterManager != nil && !clusterManager.IsLeader() {
			leader := clusterManager.LeaderAddress()
			c.JSON(http.StatusConflict, gin.H{"error": "当前节点不是 Leader，无法调度工作流", "leader": leader})
			return
		}

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
func createWorkflowHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var payload workflow.FullWorkflowPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}

		if clusterManager != nil {
			resp, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandCreateWorkflow, Payload: mustJSON(payload)})
			if err != nil {
				_ = c.Error(err)
				return
			}
			c.JSON(http.StatusCreated, resp)
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
func updateWorkflowHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var payload workflow.FullWorkflowPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			_ = c.Error(err)
			return
		}
		payload.Workflow.ID = workflowID

		if clusterManager != nil {
			resp, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandUpdateWorkflow, Payload: mustJSON(payload)})
			if err != nil {
				_ = c.Error(err)
				return
			}
			c.JSON(http.StatusOK, resp)
			return
		}

		updatedWorkflow, err := workflowService.UpdateWorkflow(c.Request.Context(), payload.Workflow, payload.Nodes, payload.Edges)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, updatedWorkflow)
	}
}

// deleteWorkflowHandler 删除一个工作流。
func deleteWorkflowHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		if clusterManager != nil {
			cmdPayload := domain.Workflow{ID: workflowID}
			if _, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandDeleteWorkflow, Payload: mustJSON(cmdPayload)}); err != nil {
				_ = c.Error(err)
				return
			}
			c.Status(http.StatusNoContent)
			return
		}
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

func addNodeHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var node domain.WorkflowNode
		if err := c.ShouldBindJSON(&node); err != nil {
			_ = c.Error(err)
			return
		}

		if clusterManager != nil {
			envelope := cluster.WorkflowNodeEnvelope{WorkflowID: workflowID, Node: node}
			resp, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandAddWorkflowNode, Payload: mustJSON(envelope)})
			if err != nil {
				_ = c.Error(err)
				return
			}
			c.JSON(http.StatusCreated, resp)
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

func updateNodeHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		nodeID := c.Param("node_id")
		var node domain.WorkflowNode
		if err := c.ShouldBindJSON(&node); err != nil {
			_ = c.Error(err)
			return
		}

		if clusterManager != nil {
			envelope := cluster.WorkflowNodeEnvelope{WorkflowID: workflowID, NodeID: nodeID, Node: node}
			resp, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandUpdateWorkflowNode, Payload: mustJSON(envelope)})
			if err != nil {
				_ = c.Error(err)
				return
			}
			c.JSON(http.StatusOK, resp)
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

func deleteNodeHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		nodeID := c.Param("node_id")

		if clusterManager != nil {
			envelope := cluster.WorkflowNodeEnvelope{WorkflowID: workflowID, NodeID: nodeID}
			if _, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandDeleteWorkflowNode, Payload: mustJSON(envelope)}); err != nil {
				_ = c.Error(err)
				return
			}
			c.Status(http.StatusNoContent)
			return
		}

		if err := workflowService.DeleteNode(c.Request.Context(), workflowID, nodeID); err != nil {
			_ = c.Error(err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

// --- Edge Handlers ---

func addEdgeHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		var edge domain.WorkflowEdge
		if err := c.ShouldBindJSON(&edge); err != nil {
			_ = c.Error(err)
			return
		}

		if clusterManager != nil {
			envelope := cluster.WorkflowEdgeEnvelope{WorkflowID: workflowID, Edge: edge}
			resp, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandAddWorkflowEdge, Payload: mustJSON(envelope)})
			if err != nil {
				_ = c.Error(err)
				return
			}
			c.JSON(http.StatusCreated, resp)
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

func updateEdgeHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		edgeIDStr := c.Param("edge_id")
		edgeID, err := strconv.ParseInt(edgeIDStr, 10, 64)
		if err != nil {
			_ = c.Error(fmt.Errorf("edge_id 参数无效: %s", edgeIDStr))
			return
		}

		var edge domain.WorkflowEdge
		if err := c.ShouldBindJSON(&edge); err != nil {
			_ = c.Error(err)
			return
		}

		if clusterManager != nil {
			envelope := cluster.WorkflowEdgeEnvelope{WorkflowID: workflowID, EdgeID: edgeID, Edge: edge}
			resp, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandUpdateWorkflowEdge, Payload: mustJSON(envelope)})
			if err != nil {
				_ = c.Error(err)
				return
			}
			c.JSON(http.StatusOK, resp)
			return
		}

		updatedEdge, err := workflowService.UpdateEdge(c.Request.Context(), workflowID, edgeID, edge)
		if err != nil {
			_ = c.Error(err)
			return
		}
		c.JSON(http.StatusOK, updatedEdge)
	}
}

func deleteEdgeHandler(workflowService *workflow.Service, clusterManager *cluster.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		workflowID := c.Param("workflow_id")
		edgeIDStr := c.Param("edge_id")
		edgeID, err := strconv.ParseInt(edgeIDStr, 10, 64)
		if err != nil {
			_ = c.Error(fmt.Errorf("edge_id 参数无效: %s", edgeIDStr))
			return
		}

		if clusterManager != nil {
			envelope := cluster.WorkflowEdgeEnvelope{WorkflowID: workflowID, EdgeID: edgeID}
			if _, err := applyWorkflowCommand(c, clusterManager, cluster.Command{Type: cluster.CommandDeleteWorkflowEdge, Payload: mustJSON(envelope)}); err != nil {
				_ = c.Error(err)
				return
			}
			c.Status(http.StatusNoContent)
			return
		}
		if err := workflowService.DeleteEdge(c.Request.Context(), workflowID, edgeID); err != nil {
			_ = c.Error(err)
			return
		}
		c.Status(http.StatusNoContent)
	}
}

func applyWorkflowCommand(c *gin.Context, manager *cluster.Manager, cmd cluster.Command) (any, error) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	return manager.ApplyCommand(ctx, cmd)
}

func mustJSON(v any) []byte {
	data, _ := json.Marshal(v)
	return data
}
