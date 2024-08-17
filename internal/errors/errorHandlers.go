// File: nexus_scholar_go_backend/internal/errors/errors.go

package errors

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// ErrorType represents the type of error
type ErrorType string

const (
	ErrorTypeBadRequest          ErrorType = "BAD_REQUEST"
	ErrorTypeUnauthorized        ErrorType = "UNAUTHORIZED"
	ErrorTypeForbidden           ErrorType = "FORBIDDEN"
	ErrorTypeNotFound            ErrorType = "NOT_FOUND"
	ErrorTypeInternalServerError ErrorType = "INTERNAL_SERVER_ERROR"
)

// CustomError represents a custom error with associated HTTP status code and type
type CustomError struct {
	Type       ErrorType
	Message    string
	StatusCode int
	Internal   error
}

// Error implements the error interface
func (e *CustomError) Error() string {
	return e.Message
}

// newError creates a new CustomError
func newError(errType ErrorType, message string, statusCode int, internal error) *CustomError {
	return &CustomError{
		Type:       errType,
		Message:    message,
		StatusCode: statusCode,
		Internal:   internal,
	}
}

// New400Error creates a new bad request error
func New400Error(message string) *CustomError {
	return newError(ErrorTypeBadRequest, message, http.StatusBadRequest, nil)
}

// New401Error creates a new unauthorized error
func New401Error() *CustomError {
	return newError(ErrorTypeUnauthorized, "Unauthorized access", http.StatusUnauthorized, nil)
}

// New403Error creates a new forbidden error
func New403Error() *CustomError {
	return newError(ErrorTypeForbidden, "Access forbidden", http.StatusForbidden, nil)
}

// New404Error creates a new not found error
func New404Error(message string) *CustomError {
	return newError(ErrorTypeNotFound, message, http.StatusNotFound, nil)
}

// New500Error creates a new internal server error
func New500Error(internal error) *CustomError {
	return newError(ErrorTypeInternalServerError, "An unexpected error occurred", http.StatusInternalServerError, internal)
}

// HandleError handles the custom error and sends an appropriate JSON response
func HandleError(c *gin.Context, err error) {
	var customErr *CustomError
	var ok bool

	if customErr, ok = err.(*CustomError); !ok {
		customErr = New500Error(err)
	}

	// Log internal server errors
	if customErr.Type == ErrorTypeInternalServerError {
		log.Error().
			Err(customErr.Internal).
			Str("url", c.Request.URL.String()).
			Msg("Internal Server Error")
	}

	c.JSON(customErr.StatusCode, gin.H{
		"error": gin.H{
			"type":    customErr.Type,
			"message": customErr.Message,
		},
	})
}

// LogAndReturn500 logs an internal error and returns a 500 error
func LogAndReturn500(internal error) *CustomError {
	log.Error().Err(internal).Msg("Internal Server Error")
	return New500Error(internal)
}
