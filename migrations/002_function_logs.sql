CREATE TABLE IF NOT EXISTS function_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    function_name TEXT NOT NULL,
    invocation_id UUID,
    stream TEXT NOT NULL,
    log_line TEXT NOT NULL,
    logged_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_function_logs_lookup
    ON function_logs (function_name, logged_at DESC);
