-- Initial v6 schema per spec §3. schema_version table is bootstrapped by the
-- migration runner before this file is applied.

-- ---------- Config (runtime knobs, key/value) ----------
CREATE TABLE config (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- ---------- Stories ----------
CREATE TABLE stories (
  id                         TEXT PRIMARY KEY,
  file                       TEXT NOT NULL,
  title                      TEXT NOT NULL,
  status                     TEXT NOT NULL,
  current_stage              TEXT,
  parallel_group             INTEGER,
  hydrated_file              TEXT,
  resource_budget_ram_mb     INTEGER,
  resource_budget_cpu_cores  REAL,
  requires_android           INTEGER NOT NULL DEFAULT 0,
  complexity                 TEXT NOT NULL DEFAULT 'medium',
  commit_hash                TEXT,
  pr_url                     TEXT,
  ci_passed                  INTEGER NOT NULL DEFAULT 0,
  completed_at               TIMESTAMP,
  created_at                 TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at                 TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE story_dependencies (
  story_id      TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  depends_on_id TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  PRIMARY KEY (story_id, depends_on_id)
);

CREATE TABLE story_affects (
  story_id TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  path     TEXT NOT NULL,
  PRIMARY KEY (story_id, path)
);

CREATE TABLE story_concerns (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  story_id    TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  appended_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  source      TEXT NOT NULL,
  body_json   TEXT NOT NULL
);

CREATE TABLE story_retry_counts (
  story_id          TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
  tdd_cycles        INTEGER NOT NULL DEFAULT 0,
  qa_cycles         INTEGER NOT NULL DEFAULT 0,
  ci_retries        INTEGER NOT NULL DEFAULT 0,
  review_iterations INTEGER NOT NULL DEFAULT 0
);

-- ---------- Batches (sprint-planning output) ----------
CREATE TABLE batches (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  sequence_no  INTEGER NOT NULL UNIQUE,
  status       TEXT NOT NULL,
  created_at   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  started_at   TIMESTAMP,
  completed_at TIMESTAMP
);

CREATE TABLE batch_stories (
  batch_id INTEGER NOT NULL REFERENCES batches(id) ON DELETE CASCADE,
  story_id TEXT    NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  PRIMARY KEY (batch_id, story_id)
);

-- ---------- Worktrees ----------
CREATE TABLE worktrees (
  story_id         TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
  path             TEXT NOT NULL UNIQUE,
  branch_name      TEXT NOT NULL UNIQUE,
  created_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  last_activity_at TIMESTAMP
);

-- ---------- Test-env allocations ----------
CREATE TABLE env_allocations (
  story_id       TEXT PRIMARY KEY REFERENCES stories(id) ON DELETE CASCADE,
  pg_port        INTEGER NOT NULL,
  redis_port     INTEGER NOT NULL,
  otel_port      INTEGER,
  db_name        TEXT NOT NULL,
  container_ids  TEXT NOT NULL,
  created_at     TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  reclaimed_at   TIMESTAMP,
  reclaim_reason TEXT
);

CREATE INDEX env_allocations_reclaimed_idx ON env_allocations(reclaimed_at);

-- ---------- Dispatches (one row per L3 agent invocation; §12.7) ----------
CREATE TABLE dispatches (
  id                   INTEGER PRIMARY KEY AUTOINCREMENT,
  story_id             TEXT NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
  stage                TEXT NOT NULL,
  agent_role           TEXT NOT NULL,
  attempt_no           INTEGER NOT NULL,
  status               TEXT NOT NULL,
  reason               TEXT,
  input_tokens         INTEGER NOT NULL DEFAULT 0,
  output_tokens        INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens    INTEGER NOT NULL DEFAULT 0,
  cache_create_tokens  INTEGER NOT NULL DEFAULT 0,
  model                TEXT,
  duration_ms          INTEGER NOT NULL DEFAULT 0,
  dispatched_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  returned_at          TIMESTAMP
);

CREATE INDEX dispatches_story_idx ON dispatches(story_id, stage);

-- ---------- Checkpoints (§12.5 dual-trigger) ----------
CREATE TABLE checkpoints (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  triggered_at       TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  trigger_kind       TEXT NOT NULL,
  trigger_detail     TEXT,
  stories_since_last INTEGER NOT NULL,
  user_decision      TEXT,
  decided_at         TIMESTAMP,
  summary_json       TEXT NOT NULL
);

-- ---------- Depguard flips ----------
CREATE TABLE depguard_flips (
  rule       TEXT PRIMARY KEY,
  state      TEXT NOT NULL,
  flipped_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE depguard_flip_history (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  rule       TEXT NOT NULL,
  from_state TEXT NOT NULL,
  to_state   TEXT NOT NULL,
  flipped_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  reason     TEXT
);
