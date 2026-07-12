package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"showmethestory/internal/infra/apiconfig"
	"sync"
	"testing"
	"time"
)

func TestV2APIConfigUpdateUsesNewServicesAndPreservesActiveTask(t *testing.T) {
	firstRequest := make(chan string, 1)
	allowFirstResponse := make(chan struct{})
	first := v2CompletionServer(t, firstRequest, allowFirstResponse)
	defer first.Close()

	secondRequest := make(chan string, 1)
	second := v2CompletionServer(t, secondRequest, nil)
	defer second.Close()

	runtime := newV2Runtime(t.TempDir(), &apiconfig.Config{BaseURL: first.URL, Model: "first-model", HTTPTimeoutSeconds: 5})
	defer runtime.close()
	handler := runtime.handler()
	v2CreateAndSelectProject(t, handler)

	oldAPI, oldOutline, oldWriting := runtime.api, runtime.outline, runtime.writing
	oldPostprocess, oldAgent := runtime.postprocess, runtime.agent
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/outline/generate", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("first outline generation = %d: %s", response.Code, response.Body.String())
	}
	if got := receiveV2Model(t, firstRequest); got != "first-model" {
		t.Fatalf("active task model = %q, want first-model", got)
	}

	config, _ := json.Marshal(apiconfig.Config{BaseURL: second.URL, Model: "second-model", HTTPTimeoutSeconds: 5})
	if _, err := runtime.PutAPIConfig(context.Background(), config); err != nil {
		t.Fatalf("PutAPIConfig() error = %v", err)
	}
	if runtime.api == oldAPI || runtime.outline == oldOutline || runtime.writing == oldWriting || runtime.postprocess == oldPostprocess || runtime.agent == oldAgent {
		t.Fatal("PutAPIConfig did not replace every AI workflow service")
	}

	close(allowFirstResponse)
	waitForV2Task(t, runtime)

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/outline/generate", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("second outline generation = %d: %s", response.Code, response.Body.String())
	}
	if got := receiveV2Model(t, secondRequest); got != "second-model" {
		t.Fatalf("next task model = %q, want second-model", got)
	}
	waitForV2Task(t, runtime)
}

func TestV2APIConfigRefreshIsConcurrentSafe(t *testing.T) {
	runtime := newV2Runtime(t.TempDir(), apiconfig.Default())
	defer runtime.close()
	handler := runtime.handler()

	const workers, updates = 8, 20
	var group sync.WaitGroup
	errs := make(chan error, workers*updates)
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for update := 0; update < updates; update++ {
				config, _ := json.Marshal(apiconfig.Config{BaseURL: "https://provider.example.test", Model: "model", HTTPTimeoutSeconds: 5})
				if _, err := runtime.PutAPIConfig(context.Background(), config); err != nil {
					errs <- err
					return
				}
				if _, err := runtime.GetAPIConfig(context.Background()); err != nil {
					errs <- err
					return
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status", nil))
				if response.Code != http.StatusBadRequest {
					errs <- &v2UnexpectedStatusError{code: response.Code}
					return
				}
			}
		}(worker)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

type v2UnexpectedStatusError struct{ code int }

func (e *v2UnexpectedStatusError) Error() string { return "unexpected status code" }

func v2CompletionServer(t *testing.T, requests chan<- string, block <-chan struct{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- request.Model
		if block != nil {
			<-block
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"title\":\"Story\",\"core_prompt\":\"Prompt\",\"story_synopsis\":\"Synopsis\",\"chapters\":[{\"num\":1,\"title\":\"One\",\"outline\":\"` + string(bytes.Repeat([]byte("x"), 140)) + `\"}]}"}}]}`))
	}))
}

func v2CreateAndSelectProject(t *testing.T, handler http.Handler) {
	t.Helper()
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/projects", `{"name":"story","language":"en"}`},
		{http.MethodPost, "/api/projects/select", `{"name":"story"}`},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(request.method, request.path, bytes.NewBufferString(request.body)))
		if response.Code != http.StatusOK {
			t.Fatalf("%s %s = %d: %s", request.method, request.path, response.Code, response.Body.String())
		}
	}
}

func receiveV2Model(t *testing.T, requests <-chan string) string {
	t.Helper()
	select {
	case model := <-requests:
		return model
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for provider request")
		return ""
	}
}

func waitForV2Task(t *testing.T, runtime *v2Runtime) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for runtime.tasks.Running() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if runtime.tasks.Running() {
		t.Fatal("timed out waiting for v2 task completion")
	}
}
