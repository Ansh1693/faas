package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ansh1693/faas/internal"
)

var ErrFunctionNotFound = errors.New("function not found")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) CreateFunction(ctx context.Context, fn *internal.Function) (*internal.Function, error) {
	env, err := json.Marshal(fn.Environment)
	if err != nil {
		return nil, fmt.Errorf("marshal function environment: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO functions (
			name, runtime, handler, timeout_seconds, memory_mb, environment, code_path,
			container_strategy, warm_pool_size, image_name
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, name, runtime, handler, timeout_seconds, memory_mb, environment, code_path,
			container_strategy, warm_pool_size, image_name, created_at, updated_at, deleted_at
	`, fn.Name, fn.Runtime, fn.Handler, fn.TimeoutSeconds, fn.MemoryMB, env, fn.CodePath,
		fn.ContainerStrategy, fn.WarmPoolSize, fn.ImageName)
	return scanFunction(row)
}

func (s *Store) ListFunctions(ctx context.Context) ([]internal.Function, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, runtime, handler, timeout_seconds, memory_mb, environment, code_path,
			container_strategy, warm_pool_size, image_name, created_at, updated_at, deleted_at
		FROM functions
		WHERE deleted_at IS NULL
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]internal.Function, 0)
	for rows.Next() {
		fn, scanErr := scanFunctionFromRows(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *fn)
	}
	return out, rows.Err()
}

func (s *Store) GetFunctionByName(ctx context.Context, name string) (*internal.Function, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, name, runtime, handler, timeout_seconds, memory_mb, environment, code_path,
			container_strategy, warm_pool_size, image_name, created_at, updated_at, deleted_at
		FROM functions
		WHERE name = $1 AND deleted_at IS NULL
	`, name)
	fn, err := scanFunction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFunctionNotFound
		}
		return nil, err
	}
	return fn, nil
}

func (s *Store) UpdateFunction(ctx context.Context, fn *internal.Function) (*internal.Function, error) {
	env, err := json.Marshal(fn.Environment)
	if err != nil {
		return nil, fmt.Errorf("marshal function environment: %w", err)
	}
	row := s.pool.QueryRow(ctx, `
		UPDATE functions
		SET runtime = $2, handler = $3, timeout_seconds = $4, memory_mb = $5, environment = $6,
		    code_path = $7, container_strategy = $8, warm_pool_size = $9, image_name = $10, updated_at = now()
		WHERE name = $1 AND deleted_at IS NULL
		RETURNING id, name, runtime, handler, timeout_seconds, memory_mb, environment, code_path,
			container_strategy, warm_pool_size, image_name, created_at, updated_at, deleted_at
	`, fn.Name, fn.Runtime, fn.Handler, fn.TimeoutSeconds, fn.MemoryMB, env, fn.CodePath, fn.ContainerStrategy, fn.WarmPoolSize, fn.ImageName)
	out, err := scanFunction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFunctionNotFound
		}
		return nil, err
	}
	return out, nil
}

func (s *Store) DeleteFunction(ctx context.Context, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE functions SET deleted_at = now(), updated_at = now() WHERE name = $1 AND deleted_at IS NULL`, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrFunctionNotFound
	}
	return nil
}

func (s *Store) ListFunctionLogs(ctx context.Context, functionName string, invocationID *string) ([]internal.FunctionLog, error) {
	baseQuery := `
		SELECT function_name, invocation_id, stream, log_line, logged_at
		FROM function_logs
		WHERE function_name = $1
	`
	var rows pgx.Rows
	var err error
	if invocationID != nil && *invocationID != "" {
		rows, err = s.pool.Query(ctx, baseQuery+` AND invocation_id::text = $2 ORDER BY logged_at DESC LIMIT 500`, functionName, *invocationID)
	} else {
		rows, err = s.pool.Query(ctx, baseQuery+` ORDER BY logged_at DESC LIMIT 500`, functionName)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]internal.FunctionLog, 0)
	for rows.Next() {
		var log internal.FunctionLog
		if scanErr := rows.Scan(&log.FunctionName, &log.InvocationID, &log.Stream, &log.LogLine, &log.LoggedAt); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, log)
	}
	return out, rows.Err()
}

func (s *Store) InsertFunctionLogs(ctx context.Context, logs []internal.FunctionLog) error {
	if len(logs) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, entry := range logs {
		batch.Queue(
			`INSERT INTO function_logs (function_name, invocation_id, stream, log_line) VALUES ($1, $2, $3, $4)`,
			entry.FunctionName,
			entry.InvocationID,
			entry.Stream,
			entry.LogLine,
		)
	}
	br := s.pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < len(logs); i++ {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func scanFunction(row pgx.Row) (*internal.Function, error) {
	var fn internal.Function
	var envJSON []byte
	err := row.Scan(
		&fn.ID, &fn.Name, &fn.Runtime, &fn.Handler, &fn.TimeoutSeconds, &fn.MemoryMB, &envJSON, &fn.CodePath,
		&fn.ContainerStrategy, &fn.WarmPoolSize, &fn.ImageName, &fn.CreatedAt, &fn.UpdatedAt, &fn.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	fn.Environment = map[string]string{}
	if len(envJSON) > 0 {
		_ = json.Unmarshal(envJSON, &fn.Environment)
	}
	return &fn, nil
}

func scanFunctionFromRows(rows pgx.Rows) (*internal.Function, error) {
	var fn internal.Function
	var envJSON []byte
	err := rows.Scan(
		&fn.ID, &fn.Name, &fn.Runtime, &fn.Handler, &fn.TimeoutSeconds, &fn.MemoryMB, &envJSON, &fn.CodePath,
		&fn.ContainerStrategy, &fn.WarmPoolSize, &fn.ImageName, &fn.CreatedAt, &fn.UpdatedAt, &fn.DeletedAt,
	)
	if err != nil {
		return nil, err
	}
	fn.Environment = map[string]string{}
	if len(envJSON) > 0 {
		_ = json.Unmarshal(envJSON, &fn.Environment)
	}
	return &fn, nil
}
