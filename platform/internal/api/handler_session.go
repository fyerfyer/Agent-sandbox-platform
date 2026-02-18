package api

import (
	"context"
	"log/slog"
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

func (h *SessionHandler) ListSessions(c *gin.Context) {
	projectID := c.Query("project_id")

	if projectID != "" {
		sessions, err := h.svc.ListSessionsByProject(c.Request.Context(), projectID)
		if err != nil {
			respondError(c, http.StatusInternalServerError, err)
			return
		}

		var resp []SessionResponse
		for _, sess := range sessions {
			resp = append(resp, SessionResponse{
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

		c.JSON(http.StatusOK, SessionListResponse{Sessions: resp})
		return
	}

	// 无 project_id 参数时列出活跃 session
	sessions, err := h.svc.ListActiveSessions(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, err)
		return
	}

	var resp []SessionResponse
	for _, sess := range sessions {
		resp = append(resp, SessionResponse{
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

	c.JSON(http.StatusOK, SessionListResponse{Sessions: resp})
}

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

// 立即返回响应，在后台 goroutine 中执行容器清理，避免 CLI 退出延迟。
func (h *SessionHandler) TerminateSession(c *gin.Context) {
	id := c.Param("id")

	if _, err := h.svc.GetSession(c.Request.Context(), id); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	// 立即返回
	c.JSON(http.StatusOK, gin.H{
		"status":     "terminating",
		"session_id": id,
	})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.svc.TerminateSession(ctx, id); err != nil {
			slog.Error("Background terminate failed", "session_id", id, "error", err)
		}
	}()
}

func (h *SessionHandler) ConfigureAgent(c *gin.Context) {
	id := c.Param("id")

	var req ConfigureAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
		return
	}

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

// 立即返回响应，在后台 goroutine 中执行 gRPC Stop
func (h *SessionHandler) StopAgent(c *gin.Context) {
	id := c.Param("id")

	if _, err := h.svc.GetSession(c.Request.Context(), id); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	// 立即返回
	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"message":    "Stop signal sent",
		"session_id": id,
	})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := h.svc.StopAgent(ctx, id); err != nil {
			slog.Error("Background stop agent failed", "session_id", id, "error", err)
		}
	}()
}

func (h *SessionHandler) RestartSession(c *gin.Context) {
	id := c.Param("id")

	if err := h.svc.RestartSession(c.Request.Context(), id); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "restarted",
		"session_id": id,
		"message":    "Container restarted successfully",
	})
}

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

	containerState, _ := h.svc.GetContainerState(c.Request.Context(), id)

	c.JSON(http.StatusOK, HealthResponse{
		Status:         healthStatus,
		ContainerState: containerState,
		Timestamp:      formatTime(time.Now()),
	})
}

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

// 为 Agent 创建一个伴随的 Docker 容器
// TODO：现在使用 docker-compose.yml 管理外部依赖，这个之后可以删除
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

// Agent 通过 compose 文件创建一组基础设施服务（DooD 方式）
func (h *SessionHandler) CreateComposeStack(c *gin.Context) {
	id := c.Param("id")

	var req CreateComposeAPIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
		return
	}

	stack, err := h.svc.CreateComposeStack(c.Request.Context(), id, service.CreateComposeRequest{
		ComposeContent: req.ComposeContent,
		ComposeFile:    req.ComposeFile,
	})
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	var services []ComposeServiceResponse
	for _, svc := range stack.Services {
		services = append(services, ComposeServiceResponse{
			Name:        svc.Name,
			ContainerID: svc.ContainerID,
			IP:          svc.IP,
			Status:      svc.Status,
		})
	}

	c.JSON(http.StatusCreated, ComposeStackResponse{
		SessionID:   id,
		ProjectName: stack.ProjectName,
		Status:      stack.Status,
		Services:    services,
	})
}

func (h *SessionHandler) GetComposeStack(c *gin.Context) {
	id := c.Param("id")

	stack, err := h.svc.RefreshComposeStack(c.Request.Context(), id)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	var services []ComposeServiceResponse
	for _, svc := range stack.Services {
		services = append(services, ComposeServiceResponse{
			Name:        svc.Name,
			ContainerID: svc.ContainerID,
			IP:          svc.IP,
			Status:      svc.Status,
		})
	}

	c.JSON(http.StatusOK, ComposeStackResponse{
		SessionID:   id,
		ProjectName: stack.ProjectName,
		Status:      stack.Status,
		Services:    services,
	})
}

func (h *SessionHandler) TeardownComposeStack(c *gin.Context) {
	id := c.Param("id")

	if err := h.svc.TeardownComposeStack(c.Request.Context(), id); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "torn_down",
		"session_id": id,
	})
}
