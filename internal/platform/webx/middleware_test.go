package webx

import (
	"bytes"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAccessLog_ClientMetadataAndResolvedIP(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	_, trusted, err := net.ParseCIDR("10.0.0.0/8")
	require.NoError(t, err)

	h := ClientMetadata(AccessLog(log, trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.5:80"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	req.Header.Set(HeaderClientPlatform, "web")
	req.Header.Set(HeaderClientVersion, "2.0.0")
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	assert.Contains(t, out, "client_platform=web", "the platform triple enters access logs")
	assert.Contains(t, out, "client_version=2.0.0")
	assert.Contains(t, out, "client_os=unknown", "missing header lands on the unknown sentinel")
	assert.Contains(t, out, "ip=203.0.113.7", "log IP and rate-limit key share the source under a trusted proxy (XFF parsing)")
	assert.Contains(t, out, "status=200")
}

func TestAccessLog_LevelSplit(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	h := AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	assert.Contains(t, buf.String(), "level=WARN", "  4xx uses the Warn level")
}
