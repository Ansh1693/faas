CREATE EXTENSION IF NOT EXISTS "pgcrypto";

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'function_runtime') THEN
        CREATE TYPE function_runtime AS ENUM ('nodejs22', 'go122');
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'container_strategy') THEN
        CREATE TYPE container_strategy AS ENUM ('cold', 'warm');
    END IF;
END $$;

CREATE TABLE IF NOT EXISTS functions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    runtime function_runtime NOT NULL,
    handler TEXT NOT NULL,
    timeout_seconds INTEGER NOT NULL DEFAULT 30 CHECK (timeout_seconds BETWEEN 1 AND 900),
    memory_mb INTEGER NOT NULL DEFAULT 128 CHECK (memory_mb >= 64),
    environment JSONB NOT NULL DEFAULT '{}'::jsonb,
    code_path TEXT NOT NULL,
    container_strategy container_strategy NOT NULL DEFAULT 'warm',
    warm_pool_size INTEGER NOT NULL DEFAULT 1 CHECK (warm_pool_size >= 1),
    image_name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_functions_name_active
    ON functions (name)
    WHERE deleted_at IS NULL;
