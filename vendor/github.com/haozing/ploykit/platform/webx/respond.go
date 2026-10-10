// Package webx 提供与框架各域共用的 HTTP 基础件：统一响应、Principal 上下文、认证中间件。
package webx

import (
	"encoding/json"
	"errors"
	"net/http"
)

// ErrorBody 是全框架统一的错误响应包体。
type ErrorBody struct {
	Code    string `json:"error"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func WriteError(w http.ResponseWriter, status int, code, message string, details any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorBody{Code: code, Message: message, Details: details})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// DecodeJSON 严格解码请求体：未知字段报错、仅接受单个 JSON 对象
// （W7：首个对象解码成功后若仍有尾随数据——如 {"a":1}{"b":2}——按 400
// 拒绝，兑现注释承诺）。
// 请求体超过 BodyLimit 设置的上限时（MaxBytesReader 读出 *http.MaxBytesError），
// 返回 413 E_PAYLOAD_TOO_LARGE 而非笼统的 400。
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			WriteError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large", err.Error())
			return false
		}
		WriteError(w, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", err.Error())
		return false
	}
	if dec.More() {
		WriteError(w, http.StatusBadRequest, "E_BAD_JSON", "invalid request body", "trailing data")
		return false
	}
	return true
}

// 常用错误码与 HTTP 状态的固定搭配，避免各 handler 各写一套。
const (
	CodeUnauthenticated = "E_UNAUTHENTICATED"
	CodeForbidden       = "E_FORBIDDEN"
	CodeNotFound        = "E_NOT_FOUND"
	CodeValidation      = "E_VALIDATION"
	CodeConflict        = "E_CONFLICT"
	CodeRateLimited     = "E_RATE_LIMITED"
	CodeQuotaExceeded   = "E_QUOTA_EXCEEDED"
	CodeInternal        = "E_INTERNAL"
	CodeTimeout         = "E_TIMEOUT"
	CodePayloadTooLarge = "E_PAYLOAD_TOO_LARGE"
	CodeUnavailable     = "E_UNAVAILABLE"
)

func ErrUnauthenticated(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, msg, nil)
}
func ErrForbidden(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusForbidden, CodeForbidden, msg, nil)
}
func ErrNotFound(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusNotFound, CodeNotFound, msg, nil)
}
func ErrValidation(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusBadRequest, CodeValidation, msg, nil)
}
func ErrConflict(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusConflict, CodeConflict, msg, nil)
}
func ErrInternal(w http.ResponseWriter) {
	WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error", nil)
}
func ErrTimeout(w http.ResponseWriter) {
	WriteError(w, http.StatusGatewayTimeout, CodeTimeout, "request timeout", nil)
}
func ErrPayloadTooLarge(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, msg, nil)
}
func ErrUnavailable(w http.ResponseWriter, msg string) {
	WriteError(w, http.StatusServiceUnavailable, CodeUnavailable, msg, nil)
}
