package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vancemichael/092002-industrial-visit-intent/internal/store"
)

func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	db, err := store.Open(context.Background(), "")
	if err != nil {
		t.Fatalf("打开内存库: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return Router(db)
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("健康接口状态码为 %d", response.Code)
	}
}
