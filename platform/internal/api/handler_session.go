package api

import (
	"net/http"
	"platform/internal/agentproto"
	"platform/internal/orchestrator"
	"platform/internal/service"
	"platform/internal/session"
	"time"

	"github.com/gin-gonic/gin"
)

type SessionHandler struct {
	svc *service.Service
}

func NewSessionHandler(svc *service.Service) *SessionHandler {
	return &SessionHandler{svc: svc}
}

// CreateSession POST /api/v1/sessions
func (h *SessionHandler) CreateSession(c *gin.Context) {
	var req CreateSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
		return
	}

	params := session.SessionParams{
		ProjectID: req.ProjectID,
		UserID:    req.UserID,
		Strategy:  mapStrategyType(req.Strategy),
		EnvVars:   req.EnvVars,
		ContainerOpts: orchestrator.ContainerOptions{
			Image:     req.Image,
			ProjectID: req.ProjectID,
			EnvVars:   req.EnvVars,
		},
	}

	sess, err := h.svc.CreateSession(c.Request.Context(), params)
	if err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusCreated, SessionResponse{
		ID:        sess.ID,
		ProjectID: sess.ProjectID,
		UserID:    sess.UserID,
		Status:    string(sess.Status),
		Strategy:  string(sess.Strategy),
		CreatedAt: formatTime(sess.CreatedAt),
	})
}

// GetSession GET /api/v1/sessions/:id
func (h *SessionHandler) GetSession(c *gin.Context) {
	id := c.Param("id")

	sess, err := h.svc.GetSession(c.Request.Context(), id)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, SessionResponse{
		ID:          sess.ID,
		ProjectID:   sess.ProjectID,
		UserID:      sess.UserID,
		ContainerID: sess.ContainerID,
		NodeIP:      sess.NodeIP,
		Status:      string(sess.Status),
		Strategy:    string(sess.Strategy),
		CreatedAt:   formatTime(sess.CreatedAt),
	})
}

// TerminateSession DELETE /api/v1/sessions/:id
func (h *SessionHandler) TerminateSession(c *gin.Context) {
	id := c.Param("id")

	if err := h.svc.TerminateSession(c.Request.Context(), id); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "terminated",
		"session_id": id,
	})
}

// ConfigureAgent POST /api/v1/sessions/:id/configure
// 给 Session 的 Agent 发送配置请求
func (h *SessionHandler) ConfigureAgent(c *gin.Context) {
	id := c.Param("id")

	var req ConfigureAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
		return
	}

	// Convert request to proto
	protoReq := &agentproto.ConfigureRequest{
		SessionId:    id,
		SystemPrompt: req.SystemPrompt,
		BuiltinTools: req.BuiltinTools,
		AgentConfig:  req.AgentConfig,
	}

	for _, td := range req.Tools {
		protoReq.Tools = append(protoReq.Tools, &agentproto.ToolDef{
			Name:           td.Name,
			Description:    td.Description,
			ParametersJson: td.ParametersJSON,
		})
	}

	resp, err := h.svc.ConfigureSession(c.Request.Context(), id, protoReq)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, ConfigureAgentResponse{
		Success:        resp.Success,
		Message:        resp.Message,
		AvailableTools: resp.AvailableTools,
	})
}

// StopAgent POST /api/v1/sessions/:id/stop
// 给 Session 的 Agent 发送停止请求
func (h *SessionHandler) StopAgent(c *gin.Context) {
	id := c.Param("id")

	resp, err := h.svc.StopAgent(c.Request.Context(), id)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":    resp.Success,
		"message":    resp.Message,
		"session_id": id,
	})
}

// SyncFiles POST /api/v1/sessions/:id/sync
// 将文件从容器工作目录复制到主机项目目录。
func (h *SessionHandler) SyncFiles(c *gin.Context) {
	id := c.Param("id")

	var req SyncFilesRequest
	// Body is optional, allow empty JSON
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.SyncFilesToHost(c.Request.Context(), id, req.SrcPath, req.DestPath); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, SyncFilesResponse{
		Status:    "synced",
		SessionID: id,
		Message:   "Files copied from container to host successfully",
	})
}

// ListFiles GET /api/v1/sessions/:id/files
// 列出容器工作目录中的文件。
func (h *SessionHandler) ListFiles(c *gin.Context) {
	id := c.Param("id")
	path := c.DefaultQuery("path", "")

	output, err := h.svc.ListContainerFiles(c.Request.Context(), id, path)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, FilesListResponse{
		SessionID: id,
		Output:    output,
	})
}

// ReadFile GET /api/v1/sessions/:id/files/read
// 读取容器工作目录中的单个文件。
func (h *SessionHandler) ReadFile(c *gin.Context) {
	id := c.Param("id")
	path := c.Query("path")

	if path == "" {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, "path query parameter required")
		return
	}

	content, err := h.svc.ReadContainerFile(c.Request.Context(), id, path)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, FileContentResponse{
		SessionID: id,
		Path:      path,
		Content:   string(content),
	})
}

// HealthCheckSession GET /api/v1/sessions/:id/health
func (h *SessionHandler) HealthCheckSession(c *gin.Context) {
	id := c.Param("id")

	healthy, err := h.svc.HealthCheck(c.Request.Context(), id)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	healthStatus := "unhealthy"
	if healthy {
		healthStatus = "healthy"
	}

	c.JSON(http.StatusOK, HealthResponse{
		Status:    healthStatus,
		Timestamp: formatTime(time.Now()),
	})
}

// WaitReady GET /api/v1/sessions/:id/wait
func (h *SessionHandler) WaitReady(c *gin.Context) {
	id := c.Param("id")

	sess, err := h.svc.WaitForReady(c.Request.Context(), id, 500*time.Millisecond)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, SessionResponse{
		ID:          sess.ID,
		ProjectID:   sess.ProjectID,
		UserID:      sess.UserID,
		ContainerID: sess.ContainerID,
		NodeIP:      sess.NodeIP,
		Status:      string(sess.Status),
		Strategy:    string(sess.Strategy),
		CreatedAt:   formatTime(sess.CreatedAt),
	})
}

// CreateService POST /api/v1/sessions/:id/services
// 为 Agent 创建一个伴随的 Docker 容器
// TODO：这部分的逻辑可以优化一下，当前的 Docker 管理有些混乱
func (h *SessionHandler) CreateService(c *gin.Context) {
	id := c.Param("id")

	var req CreateServiceAPIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
		return
	}

	svc, err := h.svc.CreateCompanionService(c.Request.Context(), id, service.CreateServiceRequest{
		Name:    req.Name,
		Image:   req.Image,
		EnvVars: req.EnvVars,
		Cmd:     req.Cmd,
	})
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusCreated, ServiceResponse{
		ServiceID: svc.ID,
		Name:      svc.Name,
		Image:     svc.Image,
		IP:        svc.IP,
		Status:    svc.Status,
		SessionID: id,
	})
}

// RemoveService DELETE /api/v1/sessions/:id/services/:service_id
func (h *SessionHandler) RemoveService(c *gin.Context) {
	id := c.Param("id")
	serviceID := c.Param("service_id")

	if err := h.svc.RemoveCompanionService(c.Request.Context(), id, serviceID); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "removed",
		"service_id": serviceID,
		"session_id": id,
	})
}

// ListServices GET /api/v1/sessions/:id/services
func (h *SessionHandler) ListServices(c *gin.Context) {
	id := c.Param("id")

	services := h.svc.ListCompanionServices(id)

	var respServices []ServiceResponse
	for _, svc := range services {
		respServices = append(respServices, ServiceResponse{
			ServiceID: svc.ID,
			Name:      svc.Name,
			Image:     svc.Image,
			IP:        svc.IP,
			Status:    svc.Status,
			SessionID: id,
		})
	}

	c.JSON(http.StatusOK, ServiceListResponse{
		SessionID: id,
		Services:  respServices,
	})
}
