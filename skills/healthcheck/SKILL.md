---
name: healthcheck
description: Poll each service in the test env (postgres, redis, otel) until it's ready to accept connections, or fail with which service(s) timed out. Use AFTER `docker-up` reports success and BEFORE running integration tests — `docker compose up -d` returning 0 does NOT mean the services are ready, only that the containers started. Trigger this whenever an agent is about to run tests against a freshly-brought-up test env, even if the user says "the containers are running, let's go" — because tests against a still-warming-up Postgres will fail in confusing ways.
---

# healthcheck

You're polling each service in the just-brought-up test env until it accepts
connections, with a per-service timeout. Returns ok when all green, or
surfaces which service(s) timed out.

## When this matters

A container's "started" event ≠ a service's "ready to accept queries" event.
Postgres takes 1-5 seconds to initialize, Redis maybe 200ms, OTel collector
varies. Running tests before all three are ready produces flaky failures that
look like real bugs — `connection refused` errors get blamed on the test code.
This skill closes that gap explicitly.

## Inputs

- `env_config` JSON from port-pool (provides ports + db_name)
- Per-service timeout from `.bmad-test-env.yml` (default: postgres 30s,
  redis 10s, otel 30s)
- `<worktree_path>` for context

## Protocol

### Run probes in parallel

For each service in the env_config, run its healthcheck command in parallel
and wait for all to succeed or any to time out.

#### Postgres

```bash
timeout 30s bash -c "until pg_isready -h localhost -p <pg_port> -d <db_name>; do sleep 1; done"
```

#### Redis

```bash
timeout 10s bash -c "until redis-cli -p <redis_port> ping | grep -q PONG; do sleep 1; done"
```

#### OTel collector (if otel_port present)

```bash
timeout 30s bash -c "until curl -sf http://localhost:<otel_port>/ > /dev/null; do sleep 1; done"
```

### Aggregate results

If every probe succeeded → emit `{"status": "ok"}` and proceed to the next
stage (usually the implement/test L3 agent).

If one or more timed out → emit:

```json
{
  "status": "failed",
  "failed_services": [
    {"name": "postgres", "timeout_s": 30, "last_error": "connection refused"},
    {"name": "redis", "timeout_s": 10, "last_error": "ping timeout"}
  ]
}
```

When this fails, the orchestrator should tear down the env (call `docker-up`'s
inverse + `port-pool`'s Release) before retrying. Don't leave half-up infra.

## Failure modes

- `pg_isready: not found` / `redis-cli: not found` — the host doesn't have
  the postgres/redis client tools installed. Surface this to the user; it's
  a one-time `apt install postgresql-client redis-tools` (or equivalent).
- All probes time out — Docker started the containers but they crashed.
  Run `docker compose -f docker-compose.test.yml logs <service>` to see why,
  surface the logs to the user. Common cause: insufficient RAM if running
  many parallel envs.
- Healthcheck passes but tests still fail with "connection refused" — race
  on the first test connection. Most test frameworks have a retry knob;
  set it to 3 attempts with a 500ms backoff. The healthcheck skill ensures
  the service is ready ONCE, not that every subsequent connect succeeds.

## Why this skill exists

Splitting healthcheck from docker-up (SRP per spec §12.4) lets the
orchestrator distinguish "Docker started the container" from "the service
is ready." These are different failure surfaces with different remediations
(restart Docker vs. increase service start budget vs. add a missing image
dependency), and conflating them produces misleading diagnostics.
