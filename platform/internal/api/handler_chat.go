package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"platform/internal/eventbus"
	"platform/internal/service"
	"time"

	"github.com/gin-gonic/gin"
)

type ChatHandler struct {
	svc *service.Service
}

func NewChatHandler(svc *service.Service) *ChatHandler {
	return &ChatHandler{svc: svc}
}

// SendMessage POST /api/v1/sessions/:id/chat
// 将用户消息发送给 Session 的 Container 的 Agent
func (h *ChatHandler) SendMessage(c *gin.Context) {
	sessionID := c.Param("id")

	var req ChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondErrorWithDetails(c, http.StatusBadRequest, ErrInvalidRequest, err.Error())
		return
	}

	if err := h.svc.SendMessage(c.Request.Context(), sessionID, req.Message); err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.JSON(http.StatusOK, ChatResponse{
		Status:    "sent",
		SessionID: sessionID,
	})
}

// StreamEvents GET /api/v1/sessions/:id/stream
// 通过 SSE 向客户端推送 Session 事件流
func (h *ChatHandler) StreamEvents(c *gin.Context) {
	sessionID := c.Param("id")

	eventCh, err := h.svc.StreamEvents(c.Request.Context(), sessionID)
	if err != nil {
		status := mapServiceError(err)
		respondError(c, status, err)
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")

	// 对于这个长时间存在的 SSE 连接，禁用服务器级别的 WriteTimeout。
	// 否则 http.Server.WriteTimeout（默认 120s）会在传输过程中强行关闭 TCP 连接
	// 导致客户端出现 IncompleteRead。
	rc := http.NewResponseController(c.Writer)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("Failed to disable write deadline for SSE", "error", err)
	}

	c.Stream(func(w io.Writer) bool {
		select {
		case event, ok := <-eventCh:
			if !ok {
				// 事件通道关闭，结束 SSE 连接
				return false
			}

			// stream.done 内部信号，关闭 SSE 连接
			if event.Type == eventbus.EventStreamDone {
				return false
			}

			sseEvent := SSEEvent{
				Type:      string(event.Type),
				SessionID: event.SessionID,
				Payload:   event.Payload,
				Timestamp: formatTime(event.Timestamp),
			}

			data, err := json.Marshal(sseEvent)
			if err != nil {
				return false
			}

			c.SSEvent("message", string(data))
			return true

		case <-c.Request.Context().Done():
			// 客户端断连
			return false

		case <-time.After(30 * time.Second):
			// 心跳保持连接
			c.SSEvent("ping", "")
			return true
		}
	})
}
