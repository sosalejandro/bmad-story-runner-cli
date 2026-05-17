// Package state defines the v6 state store domain — entity types and narrow
// ports (ISP) that infrastructure adapters satisfy. No persistence concerns
// leak into this package; it is pure data + contracts.
package state

import "time"

// ---------- Config ----------

// ConfigEntry is a single row in the runtime-knobs config table.
type ConfigEntry struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// ---------- Story enums ----------

// Status is the high-level state of a story.
type Status string

const (
	StatusPending    Status = "pending"
	StatusHydrating  Status = "hydrating"
	StatusInProgress Status = "in-progress"
	StatusReviewing  Status = "reviewing"
	StatusCommitting Status = "committing"
	StatusEnvUp      Status = "env-up"
	StatusEnvDown    Status = "env-down"
	StatusComplete   Status = "complete"
	StatusBlocked    Status = "blocked"
)

// Stage is the per-story pipeline stage (which L3 agent is next).
type Stage string

const (
	StageHydrate    Stage = "hydrate"
	StageATDD       Stage = "atdd"
	StageImplement  Stage = "implement"
	StageAutomate   Stage = "automate"
	StageTestReview Stage = "test-review"
	StageCodeReview Stage = "code-review"
	StageCommit     Stage = "commit"
	StageFinish     Stage = "finish"
	StageDone       Stage = "done"
)

// Complexity drives the §12.5 checkpoint trigger.
type Complexity string

const (
	ComplexityLow    Complexity = "low"
	ComplexityMedium Complexity = "medium"
	ComplexityHigh   Complexity = "high"
)

// StoryType drives the stage-applicability matrix — which stages must
// dispatch and which auto-skip as not-applicable for this story's nature.
//
//   - code:  full pipeline (hydrate + atdd + implement + automate +
//            test-review + code-review + commit)
//   - doc:   skip atdd / automate / test-review (no runtime behavior to test)
//   - mixed: full pipeline (treats like code; if subagents return blocked-NA
//            for portions, surface to user)
type StoryType string

const (
	StoryTypeCode  StoryType = "code"
	StoryTypeDoc   StoryType = "doc"
	StoryTypeMixed StoryType = "mixed"
)

// ResourceBudget is the per-story RAM + CPU envelope used by sprint-planning
// and system-check for parallel-batch sanity.
type ResourceBudget struct {
	RamMB    int
	CPUCores float64
}

// Story mirrors the stories table. Nullable columns are pointer-typed so the
// difference between "unset" and "zero value" is preserved across round-trips.
type Story struct {
	ID                string
	File              string
	Title             string
	Status            Status
	CurrentStage      *Stage
	ParallelGroup     *int
	HydratedFile      *string
	ResourceBudget    *ResourceBudget
	RequiresAndroid   bool
	Complexity        Complexity
	StoryType         StoryType
	CommitHash        *string
	PRURL             *string
	CIPassed          bool
	CompletedAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time

	// Claim audit — populated by Stories.ClaimNext when the orchestrator
	// atomically picks a story for dispatch. Cleared on completion or
	// explicit release. Nil = unclaimed.
	ClaimedAt *time.Time
	ClaimedBy *string
}

// Concern is one row in story_concerns.
type Concern struct {
	ID         int64
	StoryID    string
	AppendedAt time.Time
	Source     string
	BodyJSON   string
}

// RetryCounts is the per-story retry budget tracker (story_retry_counts).
type RetryCounts struct {
	StoryID          string
	TDDCycles        int
	QACycles         int
	CIRetries        int
	ReviewIterations int
}

// ---------- Batches ----------

// BatchStatus tracks where a planned batch is in its lifecycle.
type BatchStatus string

const (
	BatchPlanned  BatchStatus = "planned"
	BatchInFlight BatchStatus = "in-flight"
	BatchComplete BatchStatus = "complete"
)

// Batch mirrors the batches table; StoryIDs are loaded eagerly from batch_stories.
type Batch struct {
	ID          int64
	SequenceNo  int
	Status      BatchStatus
	StoryIDs    []string
	CreatedAt   time.Time
	StartedAt   *time.Time
	CompletedAt *time.Time
}

// ---------- Worktrees ----------

// Worktree mirrors the worktrees table.
type Worktree struct {
	StoryID        string
	Path           string
	BranchName     string
	CreatedAt      time.Time
	LastActivityAt *time.Time
}

// ---------- Env allocations ----------

// EnvAllocation mirrors env_allocations. ReclaimedAt is nil while the env is live.
type EnvAllocation struct {
	StoryID       string
	PGPort        int
	RedisPort     int
	OtelPort      *int
	DBName        string
	ContainerIDs  []string
	CreatedAt     time.Time
	ReclaimedAt   *time.Time
	ReclaimReason *string
}

// ---------- Dispatches ----------

// DispatchStatus is the return-status of one L3 invocation.
type DispatchStatus string

const (
	DispatchOK      DispatchStatus = "ok"
	DispatchBlocked DispatchStatus = "blocked"
	DispatchErrored DispatchStatus = "errored"
)

// TokenCounts carries the four token-usage axes returned by the agent API.
type TokenCounts struct {
	Input       int64
	Output      int64
	CacheRead   int64
	CacheCreate int64
}

// Dispatch mirrors one row in dispatches (§12.7 token-cost tracking).
//
// IdempotencyKey: generated by the orchestrator BEFORE the L3 Task() call.
// Recorded on the row at insert time (status=dispatched). The L3 agent
// echoes the key in its return JSON; the orchestrator's MarkReturned uses
// it to find the exact row to update — UNIQUE index prevents double-record.
// On crash resume the orchestrator scans for rows where Status=dispatched
// AND ReturnedAt IS NULL — those are in-flight or lost.
type Dispatch struct {
	ID              int64
	StoryID         string
	Stage           Stage
	AgentRole       string
	AttemptNo       int
	Status          DispatchStatus
	Reason          *string
	Tokens          TokenCounts
	Model           *string
	DurationMS      int64
	DispatchedAt    time.Time
	ReturnedAt      *time.Time
	IdempotencyKey  *string
}

// DispatchDispatched is the in-flight status — written when the orchestrator
// pre-records a dispatch row before invoking the L3 agent. Distinguishes
// "Task() sent, agent hasn't returned yet" from "ok/blocked/errored".
const DispatchDispatched DispatchStatus = "dispatched"

// ---------- Checkpoints ----------

// CheckpointTrigger identifies which §12.5 trigger fired the checkpoint.
type CheckpointTrigger string

const (
	CheckpointCount      CheckpointTrigger = "count"
	CheckpointComplexity CheckpointTrigger = "complexity"
)

// CheckpointDecision is the user's verdict on a fired checkpoint.
type CheckpointDecision string

const (
	DecisionContinue CheckpointDecision = "continue"
	DecisionAdjust   CheckpointDecision = "adjust"
	DecisionHalt     CheckpointDecision = "halt"
)

// Checkpoint mirrors the checkpoints table.
//
// IdempotencyKey: generated by the orchestrator (typically
// `count:<batch_seq>` or `complexity:<story_id>`) before Fire(). The UNIQUE
// index turns "I already fired this checkpoint" into a no-op error the
// caller swallows — prevents duplicate user-prompt halts for the same
// trigger across crash resumes.
type Checkpoint struct {
	ID               int64
	TriggeredAt      time.Time
	TriggerKind      CheckpointTrigger
	TriggerDetail    *string
	StoriesSinceLast int
	UserDecision     *CheckpointDecision
	DecidedAt        *time.Time
	SummaryJSON      string
	IdempotencyKey   *string
}

// ---------- Depguard ----------

// DepguardState is the per-rule severity (warn or error).
type DepguardState string

const (
	DepguardWarn  DepguardState = "warn"
	DepguardError DepguardState = "error"
)

// DepguardFlip mirrors one row in depguard_flips (current state).
type DepguardFlip struct {
	Rule      string
	State     DepguardState
	FlippedAt time.Time
}

// DepguardFlipEvent is one row in depguard_flip_history (audit log).
type DepguardFlipEvent struct {
	ID        int64
	Rule      string
	From      DepguardState
	To        DepguardState
	FlippedAt time.Time
	Reason    *string
}
