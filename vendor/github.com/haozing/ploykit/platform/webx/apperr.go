package webx

import (
	"errors"
	"log/slog"
	"net/http"
)

// Error 是服务层返回的结构化业务错误；handler 统一用它写响应，其余错误一律 500。
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func NewError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func NewValidation(msg string) *Error      { return NewError(400, CodeValidation, msg) }
func NewUnauthenticated(msg string) *Error { return NewError(401, CodeUnauthenticated, msg) }
func NewForbidden(msg string) *Error       { return NewError(403, CodeForbidden, msg) }
func NewNotFound(msg string) *Error        { return NewError(404, CodeNotFound, msg) }
func NewConflict(msg string) *Error        { return NewError(409, CodeConflict, msg) }
func NewRateLimited(msg string) *Error     { return NewError(429, CodeRateLimited, msg) }
func NewQuotaExceeded(msg string) *Error   { return NewError(402, CodeQuotaExceeded, msg) }

// WriteErr 把任意 error 映射为响应：webx.Error 按其状态码，其余按 500（并打印原始错误）。
func WriteErr(w http.ResponseWriter, err error) {
	var we *Error
	if errors.As(err, &we) {
		WriteError(w, we.Status, we.Code, we.Message, nil)
		return
	}
	slog.Error("internal error (unmapped)", "err", err)
	ErrInternal(w)
}
