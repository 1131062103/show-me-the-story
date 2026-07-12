package main

import (
	"net/http"
	"net/http/httptest"
	"showmethestory/internal/infra/apiconfig"
	"testing"
)

func TestHTTPServerRoutesAPIAndStaticContent(t *testing.T) {
	runtime := newV2Runtime(t.TempDir(), apiconfig.Default())
	defer runtime.close()

	server, err := newHTTPServer(runtime.handler(), ":0")
	if err != nil {
		t.Fatalf("newHTTPServer() error = %v", err)
	}

	for _, route := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodGet, "/api/version", http.StatusOK},
		{http.MethodGet, "/api/status", http.StatusBadRequest},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			server.Handler.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
			if response.Code != route.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, route.status, response.Body.String())
			}
		})
	}
}
