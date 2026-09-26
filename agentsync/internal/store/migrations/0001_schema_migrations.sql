-- Records which migrations have been applied. The runner inserts each
-- migration's row in the same transaction as the migration itself.
CREATE TABLE agentsync_schema_migrations (
  version     INT PRIMARY KEY,
  applied_at  TIMESTAMPTZ NOT NULL
);
