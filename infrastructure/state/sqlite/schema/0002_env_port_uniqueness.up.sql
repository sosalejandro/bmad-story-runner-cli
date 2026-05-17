-- Partial unique indexes on env_allocations ports — backstop against
-- port-pool race conditions where two concurrent processes might both
-- compute the same "free" block before either writes its allocation row.
-- The index turns the race into a deterministic UNIQUE violation that
-- the application layer retries on.
--
-- Partial-index predicates (`WHERE reclaimed_at IS NULL`) mean reclaimed
-- allocations are NOT covered, so a story can re-use a port that a prior
-- story released. Exactly the desired semantics.

CREATE UNIQUE INDEX env_active_pg_port_uniq
  ON env_allocations(pg_port)
  WHERE reclaimed_at IS NULL;

CREATE UNIQUE INDEX env_active_redis_port_uniq
  ON env_allocations(redis_port)
  WHERE reclaimed_at IS NULL;

CREATE UNIQUE INDEX env_active_otel_port_uniq
  ON env_allocations(otel_port)
  WHERE reclaimed_at IS NULL AND otel_port IS NOT NULL;
