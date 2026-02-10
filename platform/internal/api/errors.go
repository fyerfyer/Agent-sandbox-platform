package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionNotReady = errors.New("session is not ready")
	ErrInvalidRequest  = errors.New("invalid request")
)

func respondError(c *gin.Context, code int, err error) {
	c.JSON(code, ErrorResponse{
		Error: err.Error(),
		Code:  code,
	})
}

func respondErrorWithDetails(c *gin.Context, code int, err error, details string) {
	c.JSON(code, ErrorResponse{
		Error:   err.Error(),
		Code:    code,
		Details: details,
	})
}

func abortWithError(c *gin.Context, code int, err error) {
	c.AbortWithStatusJSON(code, ErrorResponse{
		Error: err.Error(),
		Code:  code,
	})
}

func mapServiceError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	errMsg := err.Error()
	switch {
	case strings.Contains(errMsg, "not found"):
		return http.StatusNotFound
	case strings.Contains(errMsg, "not ready"):
		return http.StatusConflict
	case strings.Contains(errMsg, "already"):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
