# State Introspection Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add CLI and MCP tools for inspecting and managing Bud's internal state (traces, percepts, threads, logs, queues, sessions).

**Architecture:** Shared inspection library in `internal/state/`, CLI binary at `cmd/bud-state/`, MCP tools in `cmd/bud-mcp/`. Both CLI and MCP call the same library functions.

**Tech Stack:** Go, JSON file I/O, existing memory package patterns

---

## Task 1: Add Delete method to TracePool

**Files:**
- Modify: `/Users/thunder/src/bud2/internal/memory/traces.go`
- Test: `/Users/thunder/src/bud2/internal/memory/traces_test.go` (create)

**Step 1: Write the failing test**

Create `/Users/thunder/src/bud2/internal/memory/traces_test.go`:

```go
package memory

import (
	"path/filepath"
	"testing"

	"github.com/vthunder/bud2/internal/types"
)

func TestTracePool_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewTracePool(filepath.Join(tmpDir, "traces.json"))

	// Add a trace
	trace := &types.Trace{ID: "test-1", Content: "test content"}
	pool.Add(trace)

	// Verify it exists
	if pool.Get("test-1") == nil {
		t.Fatal("trace should exist after Add")
	}

	// Delete it
	deleted := pool.Delete("test-1")
	if !deleted {
		t.Error("Delete should return true for existing trace")
	}

	// Verify it's gone
	if pool.Get("test-1") != nil {
		t.Error("trace should not exist after Delete")
	}

	// Delete non-existent should return false
	deleted = pool.Delete("non-existent")
	if deleted {
		t.Error("Delete should return false for non-existent trace")
	}
}

func TestTracePool_ClearNonCore(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewTracePool(filepath.Join(tmpDir, "traces.json"))

	// Add core and non-core traces
	pool.Add(&types.Trace{ID: "core-1", Content: "core", IsCore: true})
	pool.Add(&types.Trace{ID: "normal-1", Content: "normal", IsCore: false})
	pool.Add(&types.Trace{ID: "normal-2", Content: "normal 2", IsCore: false})

	// Clear non-core
	cleared := pool.ClearNonCore()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	// Verify core remains
	if pool.Get("core-1") == nil {
		t.Error("core trace should remain")
	}
	if pool.Get("normal-1") != nil {
		t.Error("non-core trace should be cleared")
	}
}

func TestTracePool_ClearCore(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewTracePool(filepath.Join(tmpDir, "traces.json"))

	pool.Add(&types.Trace{ID: "core-1", Content: "core", IsCore: true})
	pool.Add(&types.Trace{ID: "core-2", Content: "core 2", IsCore: true})
	pool.Add(&types.Trace{ID: "normal-1", Content: "normal", IsCore: false})

	cleared := pool.ClearCore()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Get("core-1") != nil {
		t.Error("core trace should be cleared")
	}
	if pool.Get("normal-1") == nil {
		t.Error("non-core trace should remain")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/memory/... -run TestTracePool -v`
Expected: FAIL with "pool.Delete undefined" or similar

**Step 3: Write minimal implementation**

Add to `/Users/thunder/src/bud2/internal/memory/traces.go` after the `SetCore` method:

```go
// Delete removes a trace by ID, returns true if found
func (p *TracePool) Delete(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.traces[id]; ok {
		delete(p.traces, id)
		return true
	}
	return false
}

// ClearNonCore removes all non-core traces, returns count deleted
func (p *TracePool) ClearNonCore() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cleared := 0
	for id, trace := range p.traces {
		if !trace.IsCore {
			delete(p.traces, id)
			cleared++
		}
	}
	return cleared
}

// ClearCore removes all core traces, returns count deleted
func (p *TracePool) ClearCore() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	cleared := 0
	for id, trace := range p.traces {
		if trace.IsCore {
			delete(p.traces, id)
			cleared++
		}
	}
	return cleared
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/memory/... -run TestTracePool -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/memory/traces.go internal/memory/traces_test.go
git commit -m "feat(memory): add Delete, ClearNonCore, ClearCore to TracePool"
```

---

## Task 2: Add Clear method to PerceptPool

**Files:**
- Modify: `/Users/thunder/src/bud2/internal/memory/percepts.go`
- Test: `/Users/thunder/src/bud2/internal/memory/percepts_test.go` (create)

**Step 1: Write the failing test**

Create `/Users/thunder/src/bud2/internal/memory/percepts_test.go`:

```go
package memory

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

func TestPerceptPool_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewPerceptPool(filepath.Join(tmpDir, "percepts.json"))

	pool.Add(&types.Percept{ID: "p1", Timestamp: time.Now()})
	pool.Add(&types.Percept{ID: "p2", Timestamp: time.Now()})

	if pool.Count() != 2 {
		t.Fatalf("expected 2 percepts, got %d", pool.Count())
	}

	cleared := pool.Clear()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Count() != 0 {
		t.Errorf("expected 0 percepts after clear, got %d", pool.Count())
	}
}

func TestPerceptPool_Count(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewPerceptPool(filepath.Join(tmpDir, "percepts.json"))

	if pool.Count() != 0 {
		t.Errorf("expected 0, got %d", pool.Count())
	}

	pool.Add(&types.Percept{ID: "p1", Timestamp: time.Now()})
	if pool.Count() != 1 {
		t.Errorf("expected 1, got %d", pool.Count())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/memory/... -run TestPerceptPool -v`
Expected: FAIL

**Step 3: Write minimal implementation**

Add to `/Users/thunder/src/bud2/internal/memory/percepts.go`:

```go
// Clear removes all percepts, returns count deleted
func (p *PerceptPool) Clear() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	count := len(p.percepts)
	p.percepts = make(map[string]*types.Percept)
	return count
}

// Count returns the number of percepts
func (p *PerceptPool) Count() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.percepts)
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/memory/... -run TestPerceptPool -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/memory/percepts.go internal/memory/percepts_test.go
git commit -m "feat(memory): add Clear, Count to PerceptPool"
```

---

## Task 3: Add Clear methods to ThreadPool

**Files:**
- Modify: `/Users/thunder/src/bud2/internal/memory/threads.go`
- Test: `/Users/thunder/src/bud2/internal/memory/threads_test.go` (create)

**Step 1: Write the failing test**

Create `/Users/thunder/src/bud2/internal/memory/threads_test.go`:

```go
package memory

import (
	"path/filepath"
	"testing"

	"github.com/vthunder/bud2/internal/types"
)

func TestThreadPool_Clear(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewThreadPool(filepath.Join(tmpDir, "threads.json"))

	pool.Add(&types.Thread{ID: "t1", Status: types.StatusActive})
	pool.Add(&types.Thread{ID: "t2", Status: types.StatusFrozen})

	if pool.Count() != 2 {
		t.Fatalf("expected 2, got %d", pool.Count())
	}

	cleared := pool.Clear()
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Count() != 0 {
		t.Errorf("expected 0 after clear, got %d", pool.Count())
	}
}

func TestThreadPool_ClearByStatus(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewThreadPool(filepath.Join(tmpDir, "threads.json"))

	pool.Add(&types.Thread{ID: "t1", Status: types.StatusActive})
	pool.Add(&types.Thread{ID: "t2", Status: types.StatusFrozen})
	pool.Add(&types.Thread{ID: "t3", Status: types.StatusFrozen})

	cleared := pool.ClearByStatus(types.StatusFrozen)
	if cleared != 2 {
		t.Errorf("expected 2 cleared, got %d", cleared)
	}

	if pool.Get("t1") == nil {
		t.Error("active thread should remain")
	}
	if pool.Get("t2") != nil {
		t.Error("frozen thread should be cleared")
	}
}

func TestThreadPool_Count(t *testing.T) {
	tmpDir := t.TempDir()
	pool := NewThreadPool(filepath.Join(tmpDir, "threads.json"))

	if pool.Count() != 0 {
		t.Errorf("expected 0, got %d", pool.Count())
	}

	pool.Add(&types.Thread{ID: "t1"})
	if pool.Count() != 1 {
		t.Errorf("expected 1, got %d", pool.Count())
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/memory/... -run TestThreadPool -v`
Expected: FAIL

**Step 3: Write minimal implementation**

Add to `/Users/thunder/src/bud2/internal/memory/threads.go`:

```go
// Clear removes all threads, returns count deleted
func (t *ThreadPool) Clear() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	count := len(t.threads)
	t.threads = make(map[string]*types.Thread)
	return count
}

// ClearByStatus removes threads with given status, returns count deleted
func (t *ThreadPool) ClearByStatus(status types.ThreadStatus) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	cleared := 0
	for id, thread := range t.threads {
		if thread.Status == status {
			delete(t.threads, id)
			cleared++
		}
	}
	return cleared
}

// Count returns the number of threads
func (t *ThreadPool) Count() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.threads)
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/memory/... -run TestThreadPool -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/memory/threads.go internal/memory/threads_test.go
git commit -m "feat(memory): add Clear, ClearByStatus, Count to ThreadPool"
```

---

## Task 4: Create state inspection package

**Files:**
- Create: `/Users/thunder/src/bud2/internal/state/inspect.go`
- Test: `/Users/thunder/src/bud2/internal/state/inspect_test.go`

**Step 1: Write the failing test**

Create `/Users/thunder/src/bud2/internal/state/inspect_test.go`:

```go
package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInspector_Summary(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestState(t, tmpDir)

	inspector := NewInspector(tmpDir)
	summary, err := inspector.Summary()
	if err != nil {
		t.Fatalf("Summary failed: %v", err)
	}

	if summary.Traces.Total < 0 {
		t.Error("traces total should be >= 0")
	}
}

func TestInspector_Health(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestState(t, tmpDir)

	inspector := NewInspector(tmpDir)
	health, err := inspector.Health()
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}

	if health.Status == "" {
		t.Error("health status should not be empty")
	}
}

func setupTestState(t *testing.T, dir string) {
	t.Helper()

	// Create minimal valid state files
	os.WriteFile(filepath.Join(dir, "traces.json"), []byte(`{"traces":[]}`), 0644)
	os.WriteFile(filepath.Join(dir, "percepts.json"), []byte(`{"percepts":[]}`), 0644)
	os.WriteFile(filepath.Join(dir, "threads.json"), []byte(`{"threads":[]}`), 0644)
	os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`{}`), 0644)
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/state/... -v`
Expected: FAIL (package doesn't exist)

**Step 3: Write minimal implementation**

Create `/Users/thunder/src/bud2/internal/state/inspect.go`:

```go
package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/types"
)

// Inspector provides state introspection capabilities
type Inspector struct {
	statePath string
}

// NewInspector creates a new state inspector
func NewInspector(statePath string) *Inspector {
	return &Inspector{statePath: statePath}
}

// ComponentSummary holds summary for one state component
type ComponentSummary struct {
	Total int `json:"total"`
	Core  int `json:"core,omitempty"` // for traces
}

// StateSummary holds summary of all state
type StateSummary struct {
	Traces   ComponentSummary `json:"traces"`
	Percepts ComponentSummary `json:"percepts"`
	Threads  ComponentSummary `json:"threads"`
	Journal  int              `json:"journal_entries"`
	Activity int              `json:"activity_entries"`
	Inbox    int              `json:"inbox_entries"`
	Outbox   int              `json:"outbox_entries"`
	Signals  int              `json:"signals_entries"`
}

// HealthReport holds health check results
type HealthReport struct {
	Status         string   `json:"status"` // "healthy", "warnings", "issues"
	Warnings       []string `json:"warnings,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
}

// Summary returns a summary of all state components
func (i *Inspector) Summary() (*StateSummary, error) {
	summary := &StateSummary{}

	// Count traces
	traces, err := i.loadTraces()
	if err == nil {
		summary.Traces.Total = len(traces)
		for _, t := range traces {
			if t.IsCore {
				summary.Traces.Core++
			}
		}
	}

	// Count percepts
	percepts, err := i.loadPercepts()
	if err == nil {
		summary.Percepts.Total = len(percepts)
	}

	// Count threads
	threads, err := i.loadThreads()
	if err == nil {
		summary.Threads.Total = len(threads)
	}

	// Count JSONL files
	summary.Journal = i.countJSONL("journal.jsonl")
	summary.Activity = i.countJSONL("activity.jsonl")
	summary.Inbox = i.countJSONL("inbox.jsonl")
	summary.Outbox = i.countJSONL("outbox.jsonl")
	summary.Signals = i.countJSONL("signals.jsonl")

	return summary, nil
}

// Health runs health checks and returns a report
func (i *Inspector) Health() (*HealthReport, error) {
	report := &HealthReport{Status: "healthy"}

	summary, _ := i.Summary()

	// Check for potential issues
	if summary.Traces.Total > 1000 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("High trace count: %d", summary.Traces.Total))
		report.Recommendations = append(report.Recommendations, "Consider pruning old non-core traces")
	}

	if summary.Percepts.Total > 100 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("High percept count: %d", summary.Percepts.Total))
		report.Recommendations = append(report.Recommendations, "Percepts should decay; check consolidation")
	}

	if summary.Traces.Core == 0 {
		report.Warnings = append(report.Warnings, "No core traces found")
		report.Recommendations = append(report.Recommendations, "Run --regen-core to bootstrap from core_seed.md")
	}

	if summary.Journal > 10000 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Large journal: %d entries", summary.Journal))
		report.Recommendations = append(report.Recommendations, "Consider truncating old journal entries")
	}

	if len(report.Warnings) > 0 {
		report.Status = "warnings"
	}

	return report, nil
}

// TraceSummary is a condensed view of a trace
type TraceSummary struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	IsCore    bool      `json:"is_core"`
	Strength  int       `json:"strength"`
	CreatedAt time.Time `json:"created_at"`
}

// ListTraces returns summaries of all traces
func (i *Inspector) ListTraces() ([]TraceSummary, error) {
	traces, err := i.loadTraces()
	if err != nil {
		return nil, err
	}

	result := make([]TraceSummary, 0, len(traces))
	for _, t := range traces {
		content := t.Content
		if len(content) > 100 {
			content = content[:100] + "..."
		}
		result = append(result, TraceSummary{
			ID:        t.ID,
			Content:   content,
			IsCore:    t.IsCore,
			Strength:  t.Strength,
			CreatedAt: t.CreatedAt,
		})
	}
	return result, nil
}

// GetTrace returns full trace by ID
func (i *Inspector) GetTrace(id string) (*types.Trace, error) {
	traces, err := i.loadTraces()
	if err != nil {
		return nil, err
	}

	for _, t := range traces {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("trace not found: %s", id)
}

// DeleteTrace removes a trace by ID
func (i *Inspector) DeleteTrace(id string) error {
	traces, err := i.loadTraces()
	if err != nil {
		return err
	}

	found := false
	result := make([]*types.Trace, 0, len(traces))
	for _, t := range traces {
		if t.ID == id {
			found = true
		} else {
			result = append(result, t)
		}
	}

	if !found {
		return fmt.Errorf("trace not found: %s", id)
	}

	return i.saveTraces(result)
}

// ClearTraces clears traces based on filter
func (i *Inspector) ClearTraces(clearCore bool) (int, error) {
	traces, err := i.loadTraces()
	if err != nil {
		return 0, err
	}

	var keep []*types.Trace
	cleared := 0
	for _, t := range traces {
		if clearCore {
			// Clear core, keep non-core
			if t.IsCore {
				cleared++
			} else {
				keep = append(keep, t)
			}
		} else {
			// Clear non-core, keep core
			if t.IsCore {
				keep = append(keep, t)
			} else {
				cleared++
			}
		}
	}

	if err := i.saveTraces(keep); err != nil {
		return 0, err
	}
	return cleared, nil
}

// PerceptSummary is a condensed view of a percept
type PerceptSummary struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Preview   string    `json:"preview"`
}

// ListPercepts returns summaries of all percepts
func (i *Inspector) ListPercepts() ([]PerceptSummary, error) {
	percepts, err := i.loadPercepts()
	if err != nil {
		return nil, err
	}

	result := make([]PerceptSummary, 0, len(percepts))
	for _, p := range percepts {
		preview := ""
		if content, ok := p.Data["content"].(string); ok {
			preview = content
			if len(preview) > 80 {
				preview = preview[:80] + "..."
			}
		}
		result = append(result, PerceptSummary{
			ID:        p.ID,
			Type:      p.Type,
			Source:    p.Source,
			Timestamp: p.Timestamp,
			Preview:   preview,
		})
	}
	return result, nil
}

// ClearPercepts removes all percepts, optionally filtering by age
func (i *Inspector) ClearPercepts(olderThan time.Duration) (int, error) {
	if olderThan == 0 {
		// Clear all
		if err := i.savePercepts(nil); err != nil {
			return 0, err
		}
		return 0, nil // We don't know how many were there
	}

	percepts, err := i.loadPercepts()
	if err != nil {
		return 0, err
	}

	cutoff := time.Now().Add(-olderThan)
	var keep []*types.Percept
	cleared := 0
	for _, p := range percepts {
		if p.Timestamp.Before(cutoff) {
			cleared++
		} else {
			keep = append(keep, p)
		}
	}

	if err := i.savePercepts(keep); err != nil {
		return 0, err
	}
	return cleared, nil
}

// ThreadSummary is a condensed view of a thread
type ThreadSummary struct {
	ID           string             `json:"id"`
	Status       types.ThreadStatus `json:"status"`
	SessionState types.SessionState `json:"session_state"`
	PerceptCount int                `json:"percept_count"`
}

// ListThreads returns summaries of all threads
func (i *Inspector) ListThreads() ([]ThreadSummary, error) {
	threads, err := i.loadThreads()
	if err != nil {
		return nil, err
	}

	result := make([]ThreadSummary, 0, len(threads))
	for _, t := range threads {
		result = append(result, ThreadSummary{
			ID:           t.ID,
			Status:       t.Status,
			SessionState: t.SessionState,
			PerceptCount: len(t.PerceptRefs),
		})
	}
	return result, nil
}

// GetThread returns full thread by ID
func (i *Inspector) GetThread(id string) (*types.Thread, error) {
	threads, err := i.loadThreads()
	if err != nil {
		return nil, err
	}

	for _, t := range threads {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, fmt.Errorf("thread not found: %s", id)
}

// ClearThreads removes threads, optionally by status
func (i *Inspector) ClearThreads(status *types.ThreadStatus) (int, error) {
	if status == nil {
		// Clear all
		if err := i.saveThreads(nil); err != nil {
			return 0, err
		}
		return 0, nil
	}

	threads, err := i.loadThreads()
	if err != nil {
		return 0, err
	}

	var keep []*types.Thread
	cleared := 0
	for _, t := range threads {
		if t.Status == *status {
			cleared++
		} else {
			keep = append(keep, t)
		}
	}

	if err := i.saveThreads(keep); err != nil {
		return 0, err
	}
	return cleared, nil
}

// TailLogs returns recent entries from journal and activity logs
func (i *Inspector) TailLogs(count int) ([]map[string]any, error) {
	var entries []map[string]any

	// Read journal
	journalEntries := i.tailJSONL("journal.jsonl", count)
	for _, e := range journalEntries {
		e["_source"] = "journal"
		entries = append(entries, e)
	}

	// Read activity
	activityEntries := i.tailJSONL("activity.jsonl", count)
	for _, e := range activityEntries {
		e["_source"] = "activity"
		entries = append(entries, e)
	}

	return entries, nil
}

// TruncateLogs keeps only the last N entries in each log
func (i *Inspector) TruncateLogs(keep int) error {
	for _, name := range []string{"journal.jsonl", "activity.jsonl"} {
		if err := i.truncateJSONL(name, keep); err != nil {
			return fmt.Errorf("failed to truncate %s: %w", name, err)
		}
	}
	return nil
}

// QueuesSummary holds queue counts
type QueuesSummary struct {
	Inbox   int `json:"inbox"`
	Outbox  int `json:"outbox"`
	Signals int `json:"signals"`
}

// ListQueues returns queue entry counts
func (i *Inspector) ListQueues() (*QueuesSummary, error) {
	return &QueuesSummary{
		Inbox:   i.countJSONL("inbox.jsonl"),
		Outbox:  i.countJSONL("outbox.jsonl"),
		Signals: i.countJSONL("signals.jsonl"),
	}, nil
}

// ClearQueues clears all queue files
func (i *Inspector) ClearQueues() error {
	for _, name := range []string{"inbox.jsonl", "outbox.jsonl", "signals.jsonl"} {
		path := filepath.Join(i.statePath, name)
		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			return fmt.Errorf("failed to clear %s: %w", name, err)
		}
	}
	return nil
}

// SessionInfo represents a session entry
type SessionInfo struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
}

// ListSessions returns session info
func (i *Inspector) ListSessions() ([]SessionInfo, error) {
	path := filepath.Join(i.statePath, "sessions.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var sessions map[string]any
	if err := json.Unmarshal(data, &sessions); err != nil {
		return nil, err
	}

	// Convert to list
	var result []SessionInfo
	for id, v := range sessions {
		info := SessionInfo{ID: id}
		if m, ok := v.(map[string]any); ok {
			if status, ok := m["status"].(string); ok {
				info.Status = status
			}
		}
		result = append(result, info)
	}
	return result, nil
}

// ClearSessions clears session tracking
func (i *Inspector) ClearSessions() error {
	path := filepath.Join(i.statePath, "sessions.json")
	return os.WriteFile(path, []byte("{}"), 0644)
}

// RegenCore regenerates core traces from seed file
func (i *Inspector) RegenCore(seedPath string) (int, error) {
	// First clear existing core traces
	cleared, err := i.ClearTraces(true) // clearCore=true
	if err != nil {
		return 0, fmt.Errorf("failed to clear core traces: %w", err)
	}

	// Read seed file
	file, err := os.Open(seedPath)
	if os.IsNotExist(err) {
		return cleared, fmt.Errorf("seed file not found: %s", seedPath)
	}
	if err != nil {
		return cleared, err
	}
	defer file.Close()

	// Parse entries separated by "---"
	var entries []string
	var current strings.Builder
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			if current.Len() > 0 {
				entries = append(entries, strings.TrimSpace(current.String()))
				current.Reset()
			}
		} else {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	if current.Len() > 0 {
		entries = append(entries, strings.TrimSpace(current.String()))
	}

	if err := scanner.Err(); err != nil {
		return cleared, err
	}

	// Load existing traces (non-core)
	traces, _ := i.loadTraces()

	// Create new core traces
	for idx, content := range entries {
		if content == "" {
			continue
		}
		trace := &types.Trace{
			ID:         fmt.Sprintf("core-%d-%d", time.Now().UnixNano(), idx),
			Content:    content,
			Activation: 1.0,
			Strength:   100,
			IsCore:     true,
			CreatedAt:  time.Now(),
			LastAccess: time.Now(),
		}
		traces = append(traces, trace)
	}

	if err := i.saveTraces(traces); err != nil {
		return cleared, err
	}

	return len(entries), nil
}

// Helper methods

func (i *Inspector) loadTraces() ([]*types.Trace, error) {
	path := filepath.Join(i.statePath, "traces.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var file struct {
		Traces []*types.Trace `json:"traces"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Traces, nil
}

func (i *Inspector) saveTraces(traces []*types.Trace) error {
	path := filepath.Join(i.statePath, "traces.json")
	file := struct {
		Traces []*types.Trace `json:"traces"`
	}{Traces: traces}
	if file.Traces == nil {
		file.Traces = []*types.Trace{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (i *Inspector) loadPercepts() ([]*types.Percept, error) {
	path := filepath.Join(i.statePath, "percepts.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var file struct {
		Percepts []*types.Percept `json:"percepts"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Percepts, nil
}

func (i *Inspector) savePercepts(percepts []*types.Percept) error {
	path := filepath.Join(i.statePath, "percepts.json")
	file := struct {
		Percepts []*types.Percept `json:"percepts"`
	}{Percepts: percepts}
	if file.Percepts == nil {
		file.Percepts = []*types.Percept{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (i *Inspector) loadThreads() ([]*types.Thread, error) {
	path := filepath.Join(i.statePath, "threads.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var file struct {
		Threads []*types.Thread `json:"threads"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Threads, nil
}

func (i *Inspector) saveThreads(threads []*types.Thread) error {
	path := filepath.Join(i.statePath, "threads.json")
	file := struct {
		Threads []*types.Thread `json:"threads"`
	}{Threads: threads}
	if file.Threads == nil {
		file.Threads = []*types.Thread{}
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func (i *Inspector) countJSONL(name string) int {
	path := filepath.Join(i.statePath, name)
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	count := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) > 0 {
			count++
		}
	}
	return count
}

func (i *Inspector) tailJSONL(name string, count int) []map[string]any {
	path := filepath.Join(i.statePath, name)
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}

	// Take last N
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}

	var result []map[string]any
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err == nil {
			result = append(result, entry)
		}
	}
	return result
}

func (i *Inspector) truncateJSONL(name string, keep int) error {
	path := filepath.Join(i.statePath, name)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	file.Close()

	// Keep last N
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}

	// Rewrite file
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/thunder/src/bud2 && go test ./internal/state/... -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/state/
git commit -m "feat(state): add inspection package with Summary, Health, and component ops"
```

---

## Task 5: Create CLI binary

**Files:**
- Create: `/Users/thunder/src/bud2/cmd/bud-state/main.go`

**Step 1: Write the CLI**

Create `/Users/thunder/src/bud2/cmd/bud-state/main.go`:

```go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vthunder/bud2/internal/state"
	"github.com/vthunder/bud2/internal/types"
)

func main() {
	// Global flags
	statePath := os.Getenv("BUD_STATE_PATH")
	if statePath == "" {
		statePath = "state"
	}

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(0)
	}

	inspector := state.NewInspector(statePath)
	cmd := os.Args[1]

	switch cmd {
	case "summary", "":
		handleSummary(inspector)
	case "health":
		handleHealth(inspector)
	case "traces":
		handleTraces(inspector, statePath, os.Args[2:])
	case "percepts":
		handlePercepts(inspector, os.Args[2:])
	case "threads":
		handleThreads(inspector, os.Args[2:])
	case "logs":
		handleLogs(inspector, os.Args[2:])
	case "queues":
		handleQueues(inspector, os.Args[2:])
	case "sessions":
		handleSessions(inspector, os.Args[2:])
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`bud-state - Inspect and manage Bud's internal state

Usage: bud-state <command> [options]

Commands:
  summary              Overview of all state components (default)
  health               Run health checks with recommendations

  traces               List all traces
  traces <id>          Show full trace
  traces -d <id>       Delete specific trace
  traces --clear       Clear all non-core traces
  traces --clear-core  Clear core traces (will need regeneration)
  traces --regen-core  Regenerate core traces from core_seed.md

  percepts             List all percepts
  percepts --count     Just show count
  percepts --clear     Clear all percepts
  percepts --clear --older-than=1h  Clear percepts older than duration

  threads              List all threads
  threads <id>         Show full thread
  threads --clear      Clear all threads
  threads --clear --status=frozen  Clear threads by status

  logs                 Tail recent journal + activity entries
  logs --truncate=100  Keep only last N entries in each log

  queues               Show inbox/outbox/signals counts
  queues --clear       Clear all queues

  sessions             List sessions
  sessions --clear     Clear session tracking

Environment:
  BUD_STATE_PATH       State directory (default: "state")`)
}

func handleSummary(inspector *state.Inspector) {
	summary, err := inspector.Summary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("State Summary")
	fmt.Println("=============")
	fmt.Printf("Traces:    %d total, %d core\n", summary.Traces.Total, summary.Traces.Core)
	fmt.Printf("Percepts:  %d\n", summary.Percepts.Total)
	fmt.Printf("Threads:   %d\n", summary.Threads.Total)
	fmt.Printf("Journal:   %d entries\n", summary.Journal)
	fmt.Printf("Activity:  %d entries\n", summary.Activity)
	fmt.Printf("Inbox:     %d\n", summary.Inbox)
	fmt.Printf("Outbox:    %d\n", summary.Outbox)
	fmt.Printf("Signals:   %d\n", summary.Signals)
}

func handleHealth(inspector *state.Inspector) {
	health, err := inspector.Health()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Health Status: %s\n", health.Status)
	if len(health.Warnings) > 0 {
		fmt.Println("\nWarnings:")
		for _, w := range health.Warnings {
			fmt.Printf("  - %s\n", w)
		}
	}
	if len(health.Recommendations) > 0 {
		fmt.Println("\nRecommendations:")
		for _, r := range health.Recommendations {
			fmt.Printf("  - %s\n", r)
		}
	}
}

func handleTraces(inspector *state.Inspector, statePath string, args []string) {
	fs := flag.NewFlagSet("traces", flag.ExitOnError)
	deleteID := fs.String("d", "", "Delete trace by ID")
	clear := fs.Bool("clear", false, "Clear all non-core traces")
	clearCore := fs.Bool("clear-core", false, "Clear core traces")
	regenCore := fs.Bool("regen-core", false, "Regenerate core from seed")
	fs.Parse(args)

	if *regenCore {
		seedPath := filepath.Join(statePath, "core_seed.md")
		count, err := inspector.RegenCore(seedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Regenerated %d core traces from %s\n", count, seedPath)
		return
	}

	if *clearCore {
		count, err := inspector.ClearTraces(true)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Cleared %d core traces\n", count)
		return
	}

	if *clear {
		count, err := inspector.ClearTraces(false)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Cleared %d non-core traces\n", count)
		return
	}

	if *deleteID != "" {
		if err := inspector.DeleteTrace(*deleteID); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Deleted trace: %s\n", *deleteID)
		return
	}

	// Show single trace or list all
	if fs.NArg() > 0 {
		trace, err := inspector.GetTrace(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(trace, "", "  ")
		fmt.Println(string(data))
		return
	}

	// List all
	traces, err := inspector.ListTraces()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Traces (%d total)\n", len(traces))
	fmt.Println("================")
	for _, t := range traces {
		coreMarker := ""
		if t.IsCore {
			coreMarker = " [CORE]"
		}
		fmt.Printf("%s%s (strength=%d)\n  %s\n\n", t.ID, coreMarker, t.Strength, t.Content)
	}
}

func handlePercepts(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("percepts", flag.ExitOnError)
	countOnly := fs.Bool("count", false, "Just show count")
	clear := fs.Bool("clear", false, "Clear percepts")
	olderThan := fs.String("older-than", "", "Clear percepts older than duration (e.g., 1h, 30m)")
	fs.Parse(args)

	if *clear {
		var dur time.Duration
		if *olderThan != "" {
			var err error
			dur, err = time.ParseDuration(*olderThan)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Invalid duration: %v\n", err)
				os.Exit(1)
			}
		}
		count, err := inspector.ClearPercepts(dur)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if dur > 0 {
			fmt.Printf("Cleared %d percepts older than %s\n", count, dur)
		} else {
			fmt.Println("Cleared all percepts")
		}
		return
	}

	percepts, err := inspector.ListPercepts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *countOnly {
		fmt.Printf("%d\n", len(percepts))
		return
	}

	fmt.Printf("Percepts (%d total)\n", len(percepts))
	fmt.Println("==================")
	for _, p := range percepts {
		age := time.Since(p.Timestamp).Round(time.Second)
		fmt.Printf("%s (%s, %s ago)\n  %s\n\n", p.ID, p.Source, age, p.Preview)
	}
}

func handleThreads(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("threads", flag.ExitOnError)
	clear := fs.Bool("clear", false, "Clear threads")
	status := fs.String("status", "", "Filter by status (active, paused, frozen, complete)")
	fs.Parse(args)

	if *clear {
		var statusPtr *types.ThreadStatus
		if *status != "" {
			s := types.ThreadStatus(*status)
			statusPtr = &s
		}
		count, err := inspector.ClearThreads(statusPtr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if statusPtr != nil {
			fmt.Printf("Cleared %d threads with status=%s\n", count, *status)
		} else {
			fmt.Println("Cleared all threads")
		}
		return
	}

	// Show single thread or list all
	if fs.NArg() > 0 {
		thread, err := inspector.GetThread(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, _ := json.MarshalIndent(thread, "", "  ")
		fmt.Println(string(data))
		return
	}

	threads, err := inspector.ListThreads()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Threads (%d total)\n", len(threads))
	fmt.Println("=================")
	for _, t := range threads {
		fmt.Printf("%s (status=%s, session=%s, %d percepts)\n",
			t.ID, t.Status, t.SessionState, t.PerceptCount)
	}
}

func handleLogs(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	truncate := fs.Int("truncate", 0, "Keep only last N entries")
	count := fs.Int("n", 20, "Number of entries to show")
	fs.Parse(args)

	if *truncate > 0 {
		if err := inspector.TruncateLogs(*truncate); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Truncated logs to last %d entries\n", *truncate)
		return
	}

	entries, err := inspector.TailLogs(*count)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Recent Log Entries (%d)\n", len(entries))
	fmt.Println("======================")
	for _, e := range entries {
		source := e["_source"]
		delete(e, "_source")
		ts := ""
		if t, ok := e["timestamp"].(string); ok {
			ts = t
		}
		summary := ""
		if s, ok := e["summary"].(string); ok {
			summary = s
		}
		fmt.Printf("[%s] %s: %s\n", source, ts, summary)
	}
}

func handleQueues(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("queues", flag.ExitOnError)
	clear := fs.Bool("clear", false, "Clear all queues")
	fs.Parse(args)

	if *clear {
		if err := inspector.ClearQueues(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Cleared all queues")
		return
	}

	queues, err := inspector.ListQueues()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Queues")
	fmt.Println("======")
	fmt.Printf("Inbox:   %d\n", queues.Inbox)
	fmt.Printf("Outbox:  %d\n", queues.Outbox)
	fmt.Printf("Signals: %d\n", queues.Signals)
}

func handleSessions(inspector *state.Inspector, args []string) {
	fs := flag.NewFlagSet("sessions", flag.ExitOnError)
	clear := fs.Bool("clear", false, "Clear session tracking")
	fs.Parse(args)

	if *clear {
		if err := inspector.ClearSessions(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Cleared sessions")
		return
	}

	sessions, err := inspector.ListSessions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Sessions (%d)\n", len(sessions))
	fmt.Println("============")
	for _, s := range sessions {
		fmt.Printf("%s (status=%s)\n", s.ID, s.Status)
	}
}
```

**Step 2: Build and test manually**

Run: `cd /Users/thunder/src/bud2 && go build ./cmd/bud-state && ./bud-state help`
Expected: Usage output

Run: `./bud-state summary`
Expected: State summary output

**Step 3: Commit**

```bash
git add cmd/bud-state/
git commit -m "feat(cli): add bud-state CLI for state introspection"
```

---

## Task 6: Add MCP tools for state introspection

**Files:**
- Modify: `/Users/thunder/src/bud2/cmd/bud-mcp/main.go`
- Modify: `/Users/thunder/src/bud2/internal/mcp/server.go`

**Step 1: Add tool handlers to bud-mcp/main.go**

Add after existing tool registrations (around line 500):

```go
	// State introspection tools
	stateInspector := state.NewInspector(statePath)

	server.RegisterTool("state_summary", func(ctx any, args map[string]any) (string, error) {
		summary, err := stateInspector.Summary()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(summary, "", "  ")
		return string(data), nil
	})

	server.RegisterTool("state_health", func(ctx any, args map[string]any) (string, error) {
		health, err := stateInspector.Health()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(health, "", "  ")
		return string(data), nil
	})

	server.RegisterTool("state_traces", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			traces, err := stateInspector.ListTraces()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(traces, "", "  ")
			return string(data), nil

		case "show":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for show action")
			}
			trace, err := stateInspector.GetTrace(id)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(trace, "", "  ")
			return string(data), nil

		case "delete":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for delete action")
			}
			if err := stateInspector.DeleteTrace(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted trace: %s", id), nil

		case "clear":
			clearCore, _ := args["clear_core"].(bool)
			count, err := stateInspector.ClearTraces(clearCore)
			if err != nil {
				return "", err
			}
			if clearCore {
				return fmt.Sprintf("Cleared %d core traces", count), nil
			}
			return fmt.Sprintf("Cleared %d non-core traces", count), nil

		case "regen_core":
			seedPath := filepath.Join(statePath, "core_seed.md")
			count, err := stateInspector.RegenCore(seedPath)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Regenerated %d core traces", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_percepts", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			percepts, err := stateInspector.ListPercepts()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(percepts, "", "  ")
			return string(data), nil

		case "count":
			percepts, err := stateInspector.ListPercepts()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", len(percepts)), nil

		case "clear":
			olderThan, _ := args["older_than"].(string)
			var dur time.Duration
			if olderThan != "" {
				var err error
				dur, err = time.ParseDuration(olderThan)
				if err != nil {
					return "", fmt.Errorf("invalid duration: %w", err)
				}
			}
			count, err := stateInspector.ClearPercepts(dur)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Cleared %d percepts", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_threads", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			threads, err := stateInspector.ListThreads()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(threads, "", "  ")
			return string(data), nil

		case "show":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for show action")
			}
			thread, err := stateInspector.GetThread(id)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(thread, "", "  ")
			return string(data), nil

		case "clear":
			statusStr, _ := args["status"].(string)
			var statusPtr *types.ThreadStatus
			if statusStr != "" {
				s := types.ThreadStatus(statusStr)
				statusPtr = &s
			}
			count, err := stateInspector.ClearThreads(statusPtr)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Cleared %d threads", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_logs", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "tail"
		}

		switch action {
		case "tail":
			count := 20
			if c, ok := args["count"].(float64); ok {
				count = int(c)
			}
			entries, err := stateInspector.TailLogs(count)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(entries, "", "  ")
			return string(data), nil

		case "truncate":
			keep := 100
			if k, ok := args["keep"].(float64); ok {
				keep = int(k)
			}
			if err := stateInspector.TruncateLogs(keep); err != nil {
				return "", err
			}
			return fmt.Sprintf("Truncated logs to %d entries", keep), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_queues", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			queues, err := stateInspector.ListQueues()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(queues, "", "  ")
			return string(data), nil

		case "clear":
			if err := stateInspector.ClearQueues(); err != nil {
				return "", err
			}
			return "Cleared all queues", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_sessions", func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			sessions, err := stateInspector.ListSessions()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(sessions, "", "  ")
			return string(data), nil

		case "clear":
			if err := stateInspector.ClearSessions(); err != nil {
				return "", err
			}
			return "Cleared sessions", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_regen_core", func(ctx any, args map[string]any) (string, error) {
		seedPath := filepath.Join(statePath, "core_seed.md")
		count, err := stateInspector.RegenCore(seedPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Regenerated %d core traces from %s", count, seedPath), nil
	})
```

**Step 2: Add import for state package**

Add to imports in `cmd/bud-mcp/main.go`:
```go
"github.com/vthunder/bud2/internal/state"
```

**Step 3: Add tool definitions to server.go**

Add to `handleToolsList` in `internal/mcp/server.go`:

```go
		{
			Name:        "state_summary",
			Description: "Get summary of all state components (traces, percepts, threads, logs, queues).",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
		{
			Name:        "state_health",
			Description: "Run health checks on state and get recommendations for cleanup.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
		{
			Name:        "state_traces",
			Description: "Manage memory traces. Actions: list, show, delete, clear, regen_core.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: list (default), show, delete, clear, regen_core",
					},
					"id": {
						Type:        "string",
						Description: "Trace ID (for show/delete)",
					},
					"clear_core": {
						Type:        "boolean",
						Description: "If true with clear action, clears core traces instead of non-core",
					},
				},
			},
		},
		{
			Name:        "state_percepts",
			Description: "Manage percepts (short-term memory). Actions: list, count, clear.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: list (default), count, clear",
					},
					"older_than": {
						Type:        "string",
						Description: "Duration for clear (e.g., '1h', '30m'). If omitted, clears all.",
					},
				},
			},
		},
		{
			Name:        "state_threads",
			Description: "Manage threads (working memory). Actions: list, show, clear.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: list (default), show, clear",
					},
					"id": {
						Type:        "string",
						Description: "Thread ID (for show)",
					},
					"status": {
						Type:        "string",
						Description: "Filter for clear (active, paused, frozen, complete)",
					},
				},
			},
		},
		{
			Name:        "state_logs",
			Description: "Manage journal and activity logs. Actions: tail, truncate.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: tail (default), truncate",
					},
					"count": {
						Type:        "integer",
						Description: "Number of entries for tail (default 20)",
					},
					"keep": {
						Type:        "integer",
						Description: "Entries to keep for truncate (default 100)",
					},
				},
			},
		},
		{
			Name:        "state_queues",
			Description: "Manage message queues (inbox, outbox, signals). Actions: list, clear.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: list (default), clear",
					},
				},
			},
		},
		{
			Name:        "state_sessions",
			Description: "Manage session tracking. Actions: list, clear.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"action": {
						Type:        "string",
						Description: "Action: list (default), clear",
					},
				},
			},
		},
		{
			Name:        "state_regen_core",
			Description: "Regenerate core identity traces from core_seed.md. Clears existing core traces first.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
```

**Step 4: Build and verify**

Run: `cd /Users/thunder/src/bud2 && go build ./cmd/bud-mcp`
Expected: No errors

**Step 5: Commit**

```bash
git add cmd/bud-mcp/main.go internal/mcp/server.go
git commit -m "feat(mcp): add state introspection tools"
```

---

## Task 7: Create Bud's state management guide

**Files:**
- Create: `/Users/thunder/src/bud2/state/notes/state-management.md`

**Step 1: Write the guide**

```markdown
# State Management Guide

This guide helps me (Bud) inspect and manage my own internal state.

## When to Introspect

- User asks "what do you remember about X?" → search traces
- Something seems wrong with my responses → check recent traces, activity
- User reports stale/wrong info → find and propose deletion
- Before claiming "I don't know" → verify traces were checked
- User asks for cleanup → run state_health(), propose actions

## Tool Quick Reference

| Task | Tool Call |
|------|-----------|
| Overview of all state | `state_summary()` |
| Health check | `state_health()` |
| List memories | `state_traces(action="list")` |
| Show specific memory | `state_traces(action="show", id="...")` |
| List percepts | `state_percepts(action="list")` |
| Recent activity | `state_logs(action="tail")` |
| Queue status | `state_queues(action="list")` |

## Cleanup Protocol

**IMPORTANT: Always get user approval before deleting anything.**

1. Run `state_health()` to identify issues
2. Describe findings to the user
3. Propose specific deletions with reasoning
4. Wait for explicit approval
5. Execute deletion only after consent

Example:
```
Me: "I found 45 non-core traces, 3 of which appear to be from testing
     (they contain 'test' in content). Want me to delete just those 3?"
User: "yes"
Me: [deletes the 3 test traces]
```

## Safe vs Unsafe Operations

### Safe (regenerable/transient)
- `state_traces(action="clear", clear_core=true)` + `state_regen_core()` - core traces regenerate from core_seed.md
- `state_percepts(action="clear")` - percepts are transient by design
- `state_queues(action="clear")` - operational, not memory
- `state_sessions(action="clear")` - just tracking data

### Careful (check first)
- `state_traces(action="delete", id="...")` - may lose learned information
- `state_traces(action="clear")` - clears all non-core traces
- `state_threads(action="clear")` - may lose conversation context
- `state_logs(action="truncate")` - loses audit trail

## Example Scenarios

### "Why did you say X earlier?"
```
1. state_logs(action="tail", count=50) - check recent activity
2. state_traces(action="list") - scan for relevant memories
3. Report findings to user
```

### "Something seems off with your memory"
```
1. state_health() - get health report
2. state_summary() - see counts
3. Share report with user
4. Propose cleanup if needed
```

### "Clear out test data"
```
1. state_traces(action="list") - find test-related traces
2. List IDs to user for approval
3. After approval: state_traces(action="delete", id="...") for each
```

### "Start fresh with identity"
```
1. Confirm with user this will clear learned memories
2. state_traces(action="clear", clear_core=true) - clear core
3. state_regen_core() - regenerate from core_seed.md
4. Optionally: state_traces(action="clear") - clear non-core too
```
```

**Step 2: Commit**

```bash
git add state/notes/state-management.md
git commit -m "docs: add Bud state management guide"
```

---

## Task 8: Add core trace pointer

**Files:**
- Modify: `/Users/thunder/src/bud2/state/core_seed.md`

**Step 1: Add state introspection pointer**

Add to the end of core_seed.md:

```markdown
---

I can inspect and manage my own state using the state_* MCP tools (state_summary, state_health, state_traces, state_percepts, state_threads, state_logs, state_queues, state_sessions, state_regen_core).

For detailed guidance on when and how to use these tools, read state/notes/state-management.md
```

**Step 2: Commit**

```bash
git add state/core_seed.md
git commit -m "docs: add state introspection pointer to core_seed.md"
```

---

## Task 9: Final integration test

**Step 1: Build all binaries**

```bash
cd /Users/thunder/src/bud2
go build ./...
```

**Step 2: Test CLI**

```bash
./bud-state summary
./bud-state health
./bud-state traces
./bud-state percepts --count
./bud-state queues
```

**Step 3: Run all tests**

```bash
go test ./... -v
```

**Step 4: Final commit if any fixes needed**

---

## Summary

After completing all tasks:

1. **Memory package** has Delete/Clear methods for TracePool, PerceptPool, ThreadPool
2. **State package** provides unified Inspector with Summary, Health, and component operations
3. **CLI binary** `bud-state` for human use
4. **MCP tools** `state_*` for Bud's self-introspection
5. **Guide** at `state/notes/state-management.md`
6. **Core pointer** in `core_seed.md`

To use:
- Human: `bud-state <command>`
- Bud: `state_summary()`, `state_health()`, etc.
