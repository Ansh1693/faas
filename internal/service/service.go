package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/Ansh1693/faas/internal"
	lambdaruntime "github.com/Ansh1693/faas/internal/lambda"
	"github.com/Ansh1693/faas/internal/store"
)

var (
	ErrInvalidFunctionName     = errors.New("function name is required")
	ErrInvalidFunctionRuntime  = errors.New("runtime must be nodejs22 or go122")
	ErrInvalidFunctionHandler  = errors.New("handler is required")
	ErrInvalidFunctionTimeout  = errors.New("timeout_seconds must be between 1 and 900")
	ErrInvalidFunctionMemory   = errors.New("memory_mb must be at least 64")
	ErrInvalidFunctionCodePath = errors.New("code_path must be an absolute path")
	ErrInvalidFunctionStrategy = errors.New("container_strategy must be cold or warm")
	ErrInvalidWarmPoolSize     = errors.New("warm_pool_size must be at least 1")
)

type Service struct {
	store  *store.Store
	runner lambdaruntime.Invoker
}

type runnerFunctionUpdater interface {
	ApplyFunctionUpdate(fn internal.Function)
}

type runnerFunctionRemover interface {
	RemoveFunction(functionName string)
}

func New(st *store.Store, runner lambdaruntime.Invoker) *Service {
	return &Service{store: st, runner: runner}
}

type CreateFunctionInput struct {
	Name              string
	Runtime           string
	Handler           string
	TimeoutSeconds    *int
	MemoryMB          *int
	Environment       map[string]string
	CodePath          string
	ContainerStrategy *string
	WarmPoolSize      *int
}

type UpdateFunctionInput struct {
	Runtime           *string
	Handler           *string
	TimeoutSeconds    *int
	MemoryMB          *int
	Environment       map[string]string
	CodePath          *string
	ContainerStrategy *string
	WarmPoolSize      *int
}

func (s *Service) CreateFunction(ctx context.Context, in CreateFunctionInput) (*internal.Function, error) {
	fn := internal.Function{
		Name:              strings.TrimSpace(in.Name),
		Runtime:           strings.TrimSpace(in.Runtime),
		Handler:           strings.TrimSpace(in.Handler),
		TimeoutSeconds:    30,
		MemoryMB:          128,
		Environment:       map[string]string{},
		CodePath:          strings.TrimSpace(in.CodePath),
		ContainerStrategy: "warm",
		WarmPoolSize:      1,
	}
	if in.TimeoutSeconds != nil {
		fn.TimeoutSeconds = *in.TimeoutSeconds
	}
	if in.MemoryMB != nil {
		fn.MemoryMB = *in.MemoryMB
	}
	if in.Environment != nil {
		fn.Environment = in.Environment
	}
	if in.ContainerStrategy != nil {
		fn.ContainerStrategy = *in.ContainerStrategy
	}
	if in.WarmPoolSize != nil {
		fn.WarmPoolSize = *in.WarmPoolSize
	}
	if err := validateFunction(&fn); err != nil {
		return nil, err
	}
	image, err := s.runner.BuildImage(ctx, fn)
	if err != nil {
		return nil, err
	}
	fn.ImageName = image
	return s.store.CreateFunction(ctx, &fn)
}

func (s *Service) ListFunctions(ctx context.Context) ([]internal.Function, error) {
	return s.store.ListFunctions(ctx)
}

func (s *Service) GetFunction(ctx context.Context, name string) (*internal.Function, error) {
	return s.store.GetFunctionByName(ctx, name)
}

func (s *Service) UpdateFunction(ctx context.Context, name string, in UpdateFunctionInput) (*internal.Function, error) {
	current, err := s.store.GetFunctionByName(ctx, name)
	if err != nil {
		return nil, err
	}
	next := *current
	if in.Runtime != nil {
		next.Runtime = strings.TrimSpace(*in.Runtime)
	}
	if in.Handler != nil {
		next.Handler = strings.TrimSpace(*in.Handler)
	}
	if in.TimeoutSeconds != nil {
		next.TimeoutSeconds = *in.TimeoutSeconds
	}
	if in.MemoryMB != nil {
		next.MemoryMB = *in.MemoryMB
	}
	if in.Environment != nil {
		next.Environment = in.Environment
	}
	if in.CodePath != nil {
		next.CodePath = strings.TrimSpace(*in.CodePath)
	}
	if in.ContainerStrategy != nil {
		next.ContainerStrategy = strings.TrimSpace(*in.ContainerStrategy)
	}
	if in.WarmPoolSize != nil {
		next.WarmPoolSize = *in.WarmPoolSize
	}
	if err := validateFunction(&next); err != nil {
		return nil, err
	}
	image, err := s.runner.BuildImage(ctx, next)
	if err != nil {
		return nil, err
	}
	next.ImageName = image
	updated, err := s.store.UpdateFunction(ctx, &next)
	if err != nil {
		return nil, err
	}
	if updater, ok := s.runner.(runnerFunctionUpdater); ok {
		updater.ApplyFunctionUpdate(*updated)
	}
	return updated, nil
}

func (s *Service) DeleteFunction(ctx context.Context, name string) error {
	if err := s.store.DeleteFunction(ctx, name); err != nil {
		return err
	}
	if remover, ok := s.runner.(runnerFunctionRemover); ok {
		remover.RemoveFunction(name)
	}
	return nil
}

func (s *Service) InvokeFunction(ctx context.Context, name string, payload internal.TriggerPayload) (*internal.TriggerInvocationResponse, error) {
	fn, err := s.store.GetFunctionByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if payload.InvocationID == uuid.Nil {
		payload.InvocationID = uuid.New()
	}
	return s.runner.Invoke(ctx, *fn, payload)
}

func (s *Service) InvokeFunctionRaw(ctx context.Context, name string, payload internal.TriggerPayload) (json.RawMessage, error) {
	fn, err := s.store.GetFunctionByName(ctx, name)
	if err != nil {
		return nil, err
	}
	if payload.InvocationID == uuid.Nil {
		payload.InvocationID = uuid.New()
	}
	return s.runner.InvokeRaw(ctx, *fn, payload)
}

func (s *Service) GetFunctionLogs(ctx context.Context, name string, invocationID *string) ([]internal.FunctionLog, error) {
	if _, err := s.store.GetFunctionByName(ctx, name); err != nil {
		return nil, err
	}
	return s.store.ListFunctionLogs(ctx, name, invocationID)
}

func validateFunction(fn *internal.Function) error {
	if strings.TrimSpace(fn.Name) == "" {
		return ErrInvalidFunctionName
	}
	rt := strings.ToLower(strings.TrimSpace(fn.Runtime))
	if rt != "nodejs22" && rt != "go122" {
		return ErrInvalidFunctionRuntime
	}
	fn.Runtime = rt
	if strings.TrimSpace(fn.Handler) == "" {
		return ErrInvalidFunctionHandler
	}
	if fn.TimeoutSeconds < 1 || fn.TimeoutSeconds > 900 {
		return ErrInvalidFunctionTimeout
	}
	if fn.MemoryMB < 64 {
		return ErrInvalidFunctionMemory
	}
	if !filepath.IsAbs(fn.CodePath) {
		return ErrInvalidFunctionCodePath
	}
	strategy := strings.ToLower(strings.TrimSpace(fn.ContainerStrategy))
	if strategy != "cold" && strategy != "warm" {
		return ErrInvalidFunctionStrategy
	}
	fn.ContainerStrategy = strategy
	if fn.WarmPoolSize < 1 {
		return ErrInvalidWarmPoolSize
	}
	return nil
}
