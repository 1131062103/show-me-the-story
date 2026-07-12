package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// ProjectInfo is the legacy-compatible project-list entry exposed by global routes.
type ProjectInfo struct {
	Name      string `json:"name"`
	Phase     string `json:"phase,omitempty"`
	Title     string `json:"title,omitempty"`
	Language  string `json:"language"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// GlobalRoutes owns process-wide routes that do not belong to a selected project.
// Its implementation lives at the application composition boundary because global
// API configuration and the data directory are outside the project aggregate.
type GlobalRoutes interface {
	ListProjects(context.Context) ([]ProjectInfo, error)
	CreateProject(context.Context, string, string) (ProjectInfo, error)
	SelectProject(context.Context, string) (ProjectInfo, error)
	CurrentProject(context.Context) ProjectInfo
	DeleteProject(context.Context, string) error
	Version(context.Context) string
	GetAPIConfig(context.Context) (any, error)
	PutAPIConfig(context.Context, json.RawMessage) (any, error)
	ListModels(context.Context, json.RawMessage) (any, error)
	TestAPI(context.Context, json.RawMessage) (any, error)
}

// WithGlobalRoutes registers project management, version, and global API-config routes.
func WithGlobalRoutes(routes GlobalRoutes) Option {
	return func(d *workflowDependencies) { d.global = routes }
}

func (s *Server) registerGlobalRoutes() {
	s.mux.HandleFunc("GET /api/projects", s.getProjects)
	s.mux.HandleFunc("POST /api/projects", s.postProject)
	s.mux.HandleFunc("GET /api/projects/current", s.getCurrentProject)
	s.mux.HandleFunc("POST /api/projects/select", s.postProjectSelect)
	s.mux.HandleFunc("DELETE /api/projects/{name}", s.deleteProject)
	s.mux.HandleFunc("GET /api/version", s.getVersion)
	s.mux.HandleFunc("GET /api/config/api", s.getAPIConfig)
	s.mux.HandleFunc("PUT /api/config/api", s.putAPIConfig)
	s.mux.HandleFunc("POST /api/config/api/models", s.postAPIModels)
	s.mux.HandleFunc("POST /api/config/api/test", s.postAPITest)
}

func (s *Server) requireGlobal(w http.ResponseWriter) GlobalRoutes {
	if s.workflows.global == nil {
		s.notImplemented(w, nil)
		return nil
	}
	return s.workflows.global
}

func (s *Server) getProjects(w http.ResponseWriter, r *http.Request) {
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	items, err := routes.ListProjects(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list_projects_failed")
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *Server) postProject(w http.ResponseWriter, r *http.Request) {
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	var body struct{ Name, Language string }
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "missing_project_name")
		return
	}
	item, err := routes.CreateProject(r.Context(), strings.TrimSpace(body.Name), body.Language)
	if errors.Is(err, ErrProjectExists) {
		writeError(w, http.StatusConflict, "project_exists")
		return
	}
	if errors.Is(err, ErrProjectName) {
		writeError(w, http.StatusBadRequest, "project_name_invalid_chars")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create_project_dir_failed")
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) getCurrentProject(w http.ResponseWriter, r *http.Request) {
	routes := s.requireGlobal(w)
	if routes != nil {
		writeJSON(w, http.StatusOK, routes.CurrentProject(r.Context()))
	}
}
func (s *Server) postProjectSelect(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || strings.TrimSpace(body.Name) == "" {
		writeError(w, http.StatusBadRequest, "missing_project_name")
		return
	}
	item, err := routes.SelectProject(r.Context(), strings.TrimSpace(body.Name))
	if errors.Is(err, ErrProjectNotFound) {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "select_project_failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": item.Name})
}
func (s *Server) deleteProject(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	if err := routes.DeleteProject(r.Context(), r.PathValue("name")); errors.Is(err, ErrProjectCurrent) {
		writeError(w, http.StatusConflict, "cannot_delete_current_project")
	} else if errors.Is(err, ErrProjectNotFound) {
		writeError(w, http.StatusNotFound, "project_not_found")
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "delete_project_failed")
	} else {
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}
func (s *Server) getVersion(w http.ResponseWriter, r *http.Request) {
	routes := s.requireGlobal(w)
	if routes != nil {
		writeJSON(w, http.StatusOK, map[string]string{"version": routes.Version(r.Context())})
	}
}
func (s *Server) getAPIConfig(w http.ResponseWriter, r *http.Request) {
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	value, err := routes.GetAPIConfig(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load_api_config_failed")
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (s *Server) postAPIModels(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	var raw json.RawMessage
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	value, err := routes.ListModels(r.Context(), raw)
	if err != nil {
		writeError(w, http.StatusBadGateway, "api_test_failed")
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (s *Server) postAPITest(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	var raw json.RawMessage
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	value, err := routes.TestAPI(r.Context(), raw)
	if err != nil {
		writeError(w, http.StatusBadGateway, "api_test_failed")
		return
	}
	writeJSON(w, http.StatusOK, value)
}
func (s *Server) putAPIConfig(w http.ResponseWriter, r *http.Request) {
	if s.rejectTask(w) {
		return
	}
	routes := s.requireGlobal(w)
	if routes == nil {
		return
	}
	var raw json.RawMessage
	if json.NewDecoder(r.Body).Decode(&raw) != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	value, err := routes.PutAPIConfig(r.Context(), raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

var (
	ErrProjectExists   = errors.New("project exists")
	ErrProjectName     = errors.New("invalid project name")
	ErrProjectNotFound = errors.New("project not found")
	ErrProjectCurrent  = errors.New("current project")
)
