package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"showmethestory/internal/config"
)

func TestFetchModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[{"id":"model-a"},{"id":"model-b"},{"id":"model-a"}]}`))
	}))
	defer srv.Close()

	cfg := &config.APIConfig{
		BaseURL: srv.URL,
		APIKey:  "sk-test",
	}
	models, err := FetchModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("FetchModels: %v", err)
	}
	if len(models) != 2 || models[0] != "model-a" || models[1] != "model-b" {
		t.Fatalf("models = %v", models)
	}
}

func TestFetchModelsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	cfg := &config.APIConfig{BaseURL: srv.URL}
	_, err := FetchModels(context.Background(), cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v", err)
	}
}

func TestFetchModelsEmptyBase(t *testing.T) {
	cfg := &config.APIConfig{}
	if _, err := FetchModels(context.Background(), cfg); err == nil {
		t.Fatal("expected error for empty base URL")
	}
}
