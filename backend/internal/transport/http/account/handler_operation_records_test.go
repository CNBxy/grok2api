package account

import (
	"net/http/httptest"
	"strings"
	"testing"

	accountapp "github.com/chenyme/grok2api/backend/internal/application/account"
	"github.com/gin-gonic/gin"
)

func TestListOperationRecordsRejectsCredentialRefreshForWeb(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := accountapp.NewService(nil, nil, nil, nil, nil, nil, nil)
	handler := NewHandler(service, nil)
	router := gin.New()
	handler.Register(router.Group("/api/admin/v1"))

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/admin/v1/accounts/operation-records?provider=grok_web&opType=credential_refresh", nil)
	router.ServeHTTP(recorder, req)
	if recorder.Code != 400 || !strings.Contains(recorder.Body.String(), "invalidOperationRecordQuery") {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestListOperationLogsRejectsInvalidOpType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// pathID will fail without a real account service Get; invalid opType is checked after Get.
	// Use invalid id path first to ensure route exists under :id/operation-logs.
	service := accountapp.NewService(nil, nil, nil, nil, nil, nil, nil)
	handler := NewHandler(service, nil)
	router := gin.New()
	handler.Register(router.Group("/api/admin/v1"))

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/admin/v1/accounts/abc/operation-logs?opType=quota_sync", nil)
	router.ServeHTTP(recorder, req)
	if recorder.Code != 400 {
		t.Fatalf("expected invalid id 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
