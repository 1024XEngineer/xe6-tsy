# Realtime Session Lifecycle Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the independently testable PR 1 skeleton for realtime session lifecycle and runtime state management.

**Architecture:** The `session` package owns lifecycle orchestration and runtime snapshots. It depends on narrow ports for member 1 session reads, member 2 language reads, pipeline resources, WebRTC cleanup, and runtime persistence; deterministic in-memory fakes keep all tests offline.

**Tech Stack:** Go 1.25, standard library `context`, `errors`, `sync`, `testing`

---

## File Map

- `go.mod`: declares the standalone realtime-audio Go module.
- `session/errors.go`: stable domain errors and error codes.
- `session/model.go`: business snapshots, language snapshots, commands, and runtime states.
- `session/ports.go`: external and internal dependency interfaces.
- `session/repository.go`: concurrency-safe in-memory runtime repository.
- `session/repository_test.go`: repository behavior and isolation tests.
- `session/service.go`: lifecycle construction, start, stop, and runtime reads.
- `session/service_test.go`: lifecycle behavior, failure, idempotency, and concurrency tests.

### Task 1: Establish the Module and Contracts

**Files:**
- Create: `services/realtime-audio/go.mod`
- Create: `services/realtime-audio/session/errors.go`
- Create: `services/realtime-audio/session/model.go`
- Create: `services/realtime-audio/session/ports.go`

- [ ] **Step 1: Declare the module**

```go
module github.com/1024XEngineer/xe6-tsy/services/realtime-audio

go 1.25
```

- [ ] **Step 2: Define stable lifecycle errors**

```go
var (
	ErrRuntimeNotFound  = errors.New("runtime snapshot not found")
	ErrSessionNotCreated = errors.New("session must be created before realtime starts")
	ErrInvalidDependency = errors.New("invalid lifecycle dependency")
)

const (
	ErrorCodeStartFailed = "realtime_start_failed"
	ErrorCodeStopFailed  = "realtime_stop_failed"
)
```

- [ ] **Step 3: Define models and runtime states**

Create the following typed constants and snapshot fields:

```go
type RuntimeState string

const (
	RuntimeStopped       RuntimeState = "stopped"
	RuntimeStarting      RuntimeState = "starting"
	RuntimeListening     RuntimeState = "listening"
	RuntimeASRProcessing RuntimeState = "asr_processing"
	RuntimeTranslating   RuntimeState = "translating"
	RuntimeTTSProcessing RuntimeState = "tts_processing"
	RuntimePlaying       RuntimeState = "playing"
	RuntimeStopping      RuntimeState = "stopping"
	RuntimeFailed        RuntimeState = "failed"
)

type SessionSnapshot struct {
	SessionID    string
	AccountID    string
	Status       string
	AudioConfig  json.RawMessage
	Capabilities json.RawMessage
	StartedAt    *time.Time
	EndedAt      *time.Time
}

type RuntimeSnapshot struct {
	SessionID        string
	RuntimeState     RuntimeState
	CurrentTurnID    *string
	CurrentPlaybackID *string
	LastErrorCode    *string
	UpdatedAt        time.Time
}
```

Also define `LanguagePair`, `LanguageConfigSnapshot`, `StartRealtimeCommand`,
and `StopRealtimeCommand`. The language reader accepts `sessionID` only and no
model contains a member 2 supplied Turn ID.

- [ ] **Step 4: Define narrow ports**

```go
type SessionReader interface {
	GetSession(context.Context, string) (SessionSnapshot, error)
}

type LanguageConfigReader interface {
	GetCurrentConfig(context.Context, string) (LanguageConfigSnapshot, error)
}

type RuntimeRepository interface {
	Get(context.Context, string) (RuntimeSnapshot, error)
	Save(context.Context, RuntimeSnapshot) error
}

type PipelineManager interface {
	Start(context.Context, SessionSnapshot) error
	Stop(context.Context, string) error
}

type WebRTCConnectionManager interface {
	Close(context.Context, string) error
}
```

- [ ] **Step 5: Format and compile the contracts**

Run: `gofmt -w session/errors.go session/model.go session/ports.go`

Run: `go test ./...`

Expected: PASS with `?[no test files]` for the `session` package.

- [ ] **Step 6: Commit the module contracts**

```bash
git add services/realtime-audio/go.mod services/realtime-audio/session
git commit -m "feat(realtime-audio): define lifecycle contracts"
```

### Task 2: Implement the In-Memory Runtime Repository

**Files:**
- Create: `services/realtime-audio/session/repository_test.go`
- Create: `services/realtime-audio/session/repository.go`

- [ ] **Step 1: Write failing repository tests**

Start with these concrete missing and round-trip tests:

```go
func TestMemoryRuntimeRepositoryMissing(t *testing.T) {
	repo := NewMemoryRuntimeRepository()
	_, err := repo.Get(context.Background(), "session-1")
	if !errors.Is(err, ErrRuntimeNotFound) {
		t.Fatalf("Get() error = %v, want ErrRuntimeNotFound", err)
	}
}

func TestMemoryRuntimeRepositorySaveAndGet(t *testing.T) {
	repo := NewMemoryRuntimeRepository()
	want := RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening}
	if err := repo.Save(context.Background(), want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := repo.Get(context.Background(), "session-1")
	if err != nil || got.RuntimeState != want.RuntimeState {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
}
```

Include pointer-isolation and concurrent read/write subtests. Coordinate
concurrency with `sync.WaitGroup`, never `time.Sleep`.

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./session -run MemoryRuntimeRepository -count=1`

Expected: FAIL because `NewMemoryRuntimeRepository` is undefined.

- [ ] **Step 3: Implement the repository**

Use `sync.RWMutex` and `map[string]RuntimeSnapshot`. Reject empty session IDs,
honor a canceled context before reading or saving, and clone optional string
pointers so callers cannot mutate stored state through aliases.

- [ ] **Step 4: Verify repository behavior**

Run: `go test -race ./session -run MemoryRuntimeRepository -count=1`

Expected: PASS with no race reports.

- [ ] **Step 5: Commit the repository**

```bash
git add services/realtime-audio/session/repository.go services/realtime-audio/session/repository_test.go
git commit -m "feat(realtime-audio): add runtime repository"
```

### Task 3: Implement Lifecycle Start and Runtime Reads

**Files:**
- Create: `services/realtime-audio/session/service_test.go`
- Create: `services/realtime-audio/session/service.go`

- [ ] **Step 1: Write failing start tests**

Use small fake implementations and a fixed clock. Implement these cases with
the listed assertions:

```go
tests := []struct {
	name              string
	businessStatus    string
	existingRuntime   *RuntimeSnapshot
	startErrors        []error
	wantState          RuntimeState
	wantError          error
	wantPipelineStarts int
}{
	{
		name: "created session starts",
		businessStatus: "created",
		wantState: RuntimeListening,
		wantPipelineStarts: 1,
	},
	{
		name: "active session is rejected",
		businessStatus: "active",
		wantError: ErrSessionNotCreated,
		wantPipelineStarts: 0,
	},
	{
		name: "listening runtime is idempotent",
		existingRuntime: &RuntimeSnapshot{
			SessionID: "session-1", RuntimeState: RuntimeListening,
		},
		wantState: RuntimeListening,
		wantPipelineStarts: 0,
	},
}
```

Add a two-call retry test where the fake pipeline returns `errProvider` on its
first call and nil on its second. The first result must be `RuntimeFailed` with
`ErrorCodeStartFailed` and satisfy `errors.Is(err, errProvider)`; the second
must be `RuntimeListening`, have no last error code, and leave the fake with two
start calls. Add a constructor table with each required dependency set to nil,
and a Get test that compares the saved snapshot with the returned snapshot.

- [ ] **Step 2: Verify the start tests fail**

Run: `go test ./session -run 'Lifecycle(Start|Get)|NewLifecycle' -count=1`

Expected: FAIL because `LifecycleService` is undefined.

- [ ] **Step 3: Implement constructor, keyed serialization, Start, and Get**

```go
type Dependencies struct {
	Sessions    SessionReader
	Runtimes    RuntimeRepository
	Pipelines   PipelineManager
	Connections WebRTCConnectionManager
	Now         func() time.Time
}

type LifecycleService struct {
	deps  Dependencies
	locks keyedLocker
}
```

`Start` acquires the session key lock, returns an existing non-failed and
non-stopped snapshot, validates a `created` business session, saves `starting`,
starts the pipeline, then saves `listening`. On pipeline failure it saves
`failed` with `realtime_start_failed`. A failed runtime is retryable.

- [ ] **Step 4: Verify start behavior**

Run: `go test -race ./session -run 'Lifecycle(Start|Get)|NewLifecycle' -count=1`

Expected: PASS with no race reports.

- [ ] **Step 5: Commit lifecycle start**

```bash
git add services/realtime-audio/session/service.go services/realtime-audio/session/service_test.go
git commit -m "feat(realtime-audio): add lifecycle start"
```

### Task 4: Implement Idempotent Stop and Concurrency Coverage

**Files:**
- Modify: `services/realtime-audio/session/service.go`
- Modify: `services/realtime-audio/session/service_test.go`

- [ ] **Step 1: Write failing stop and concurrency tests**

Use this table to drive stop behavior:

```go
tests := []struct {
	name                 string
	existing             *RuntimeSnapshot
	pipelineError        error
	connectionError      error
	wantState            RuntimeState
	wantPipelineStops    int
	wantConnectionCloses int
}{
	{
		name: "listening runtime stops",
		existing: &RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening},
		wantState: RuntimeStopped, wantPipelineStops: 1, wantConnectionCloses: 1,
	},
	{
		name: "missing runtime is idempotent",
		wantState: RuntimeStopped,
	},
	{
		name: "stopped runtime is idempotent",
		existing: &RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeStopped},
		wantState: RuntimeStopped,
	},
	{
		name: "pipeline cleanup failure still closes connection",
		existing: &RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening},
		pipelineError: errPipeline, wantState: RuntimeFailed,
		wantPipelineStops: 1, wantConnectionCloses: 1,
	},
	{
		name: "connection cleanup failure records failed state",
		existing: &RuntimeSnapshot{SessionID: "session-1", RuntimeState: RuntimeListening},
		connectionError: errConnection, wantState: RuntimeFailed,
		wantPipelineStops: 1, wantConnectionCloses: 1,
	},
}
```

The active case requires `stopping -> stopped`, clears Turn/playback IDs, and
records cleanup order as `pipeline`, then `connection`. Both cleanup methods
must be attempted even when one fails. Any cleanup error produces
`RuntimeFailed` with `realtime_stop_failed` and remains retryable.

For concurrent start, block the first fake pipeline call on a channel, launch
two `Start` calls, release the first call, then assert both results are
`RuntimeListening` and the pipeline start count is exactly one.

- [ ] **Step 2: Verify the new tests fail**

Run: `go test ./session -run 'Lifecycle(Stop|Concurrent)' -count=1`

Expected: FAIL because `Stop` is not implemented or does not satisfy cleanup.

- [ ] **Step 3: Implement Stop**

Acquire the same per-session lock used by `Start`. Return a synthetic stopped
snapshot for `ErrRuntimeNotFound`, and return an existing stopped snapshot as a
no-op. Otherwise save `stopping`, attempt pipeline and connection cleanup in
order, combine failures with `errors.Join`, save `failed` when cleanup fails,
or clear active IDs and save `stopped` on success.

- [ ] **Step 4: Run complete verification**

Run: `gofmt -w session/*.go`

Run: `go test -race ./... -count=1`

Run: `go vet ./...`

Expected: all commands PASS with no race or vet reports.

- [ ] **Step 5: Check delivery limits and scope**

Run: `git diff --check`

Run: `git status --short`

Run: `git diff --numstat HEAD~3..HEAD`

Expected: only `services/realtime-audio/` implementation files plus the known
pre-existing root `AGENTS.md` worktree modification; every commit is below 500
changed lines and the cumulative PR slice is below 2,000 changed lines.

- [ ] **Step 6: Commit lifecycle stop**

```bash
git add services/realtime-audio/session/service.go services/realtime-audio/session/service_test.go
git commit -m "feat(realtime-audio): complete lifecycle shutdown"
```

After this commit, stop before pushing the branch or creating PR 1 and request
explicit user approval.
