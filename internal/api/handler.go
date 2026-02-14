package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Ansh1693/faas/internal"
	"github.com/Ansh1693/faas/internal/service"
	"github.com/Ansh1693/faas/internal/store"
)

type Handler struct {
	svc    *service.Service
	logger *slog.Logger
}

func NewRouter(svc *service.Service, logger *slog.Logger) http.Handler {
	h := &Handler{svc: svc, logger: logger}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(middleware.Heartbeat("/health"))

	r.Post("/functions", h.CreateFunction)
	r.Get("/functions", h.ListFunctions)
	r.Get("/functions/{name}", h.GetFunction)
	r.Put("/functions/{name}", h.UpdateFunction)
	r.Delete("/functions/{name}", h.DeleteFunction)
	r.Post("/functions/{name}/invoke", h.InvokeFunction)
	r.Get("/functions/{name}/logs", h.GetFunctionLogs)

	// Internal endpoint for sqs-service trigger dispatcher.
	r.Post("/internal/invoke/{functionName}", h.InternalInvokeFunction)

	return r
}

type functionRequest struct {
	Name              string            `json:"name"`
	Runtime           string            `json:"runtime"`
	Handler           string            `json:"handler"`
	TimeoutSeconds    *int              `json:"timeout_seconds,omitempty"`
	MemoryMB          *int              `json:"memory_mb,omitempty"`
	Environment       map[string]string `json:"environment,omitempty"`
	CodePath          string            `json:"code_path"`
	ContainerStrategy *string           `json:"container_strategy,omitempty"`
	WarmPoolSize      *int              `json:"warm_pool_size,omitempty"`
}

type functionUpdateRequest struct {
	Runtime           *string           `json:"runtime,omitempty"`
	Handler           *string           `json:"handler,omitempty"`
	TimeoutSeconds    *int              `json:"timeout_seconds,omitempty"`
	MemoryMB          *int              `json:"memory_mb,omitempty"`
	Environment       map[string]string `json:"environment,omitempty"`
	CodePath          *string           `json:"code_path,omitempty"`
	ContainerStrategy *string           `json:"container_strategy,omitempty"`
	WarmPoolSize      *int              `json:"warm_pool_size,omitempty"`
}

func (h *Handler) CreateFunction(w http.ResponseWriter, r *http.Request) {
	var req functionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	fn, err := h.svc.CreateFunction(r.Context(), service.CreateFunctionInput{
		Name:              req.Name,
		Runtime:           req.Runtime,
		Handler:           req.Handler,
		TimeoutSeconds:    req.TimeoutSeconds,
		MemoryMB:          req.MemoryMB,
		Environment:       req.Environment,
		CodePath:          req.CodePath,
		ContainerStrategy: req.ContainerStrategy,
		WarmPoolSize:      req.WarmPoolSize,
	})
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, fn)
}

func (h *Handler) ListFunctions(w http.ResponseWriter, r *http.Request) {
	fns, err := h.svc.ListFunctions(r.Context())
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"functions": fns})
}

func (h *Handler) GetFunction(w http.ResponseWriter, r *http.Request) {
	fn, err := h.svc.GetFunction(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fn)
}

func (h *Handler) UpdateFunction(w http.ResponseWriter, r *http.Request) {
	var req functionUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	fn, err := h.svc.UpdateFunction(r.Context(), chi.URLParam(r, "name"), service.UpdateFunctionInput{
		Runtime:           req.Runtime,
		Handler:           req.Handler,
		TimeoutSeconds:    req.TimeoutSeconds,
		MemoryMB:          req.MemoryMB,
		Environment:       req.Environment,
		CodePath:          req.CodePath,
		ContainerStrategy: req.ContainerStrategy,
		WarmPoolSize:      req.WarmPoolSize,
	})
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, fn)
}

func (h *Handler) DeleteFunction(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.DeleteFunction(r.Context(), chi.URLParam(r, "name")); err != nil {
		h.handleError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InvokeFunction(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var payload internal.TriggerPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, err := h.svc.InvokeFunctionRaw(r.Context(), name, payload)
	if err != nil {
		h.handleError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if len(resp) == 0 {
		_, _ = w.Write([]byte("{}"))
		return
	}
	_, _ = w.Write(resp)
}

func (h *Handler) InternalInvokeFunction(w http.ResponseWriter, r *http.Request) {
	var payload internal.TriggerPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	resp, err := h.svc.InvokeFunction(r.Context(), chi.URLParam(r, "functionName"), payload)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetFunctionLogs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var invocationID *string
	if raw := r.URL.Query().Get("invocation_id"); raw != "" {
		invocationID = &raw
	}
	logs, err := h.svc.GetFunctionLogs(r.Context(), name, invocationID)
	if err != nil {
		h.handleError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"logs": logs})
}

func (h *Handler) handleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrFunctionNotFound):
		writeError(w, http.StatusNotFound, "function not found")
	case errors.Is(err, service.ErrInvalidFunctionName),
		errors.Is(err, service.ErrInvalidFunctionRuntime),
		errors.Is(err, service.ErrInvalidFunctionHandler),
		errors.Is(err, service.ErrInvalidFunctionTimeout),
		errors.Is(err, service.ErrInvalidFunctionMemory),
		errors.Is(err, service.ErrInvalidFunctionCodePath),
		errors.Is(err, service.ErrInvalidFunctionStrategy),
		errors.Is(err, service.ErrInvalidWarmPoolSize):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		h.logger.Error("internal error", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
