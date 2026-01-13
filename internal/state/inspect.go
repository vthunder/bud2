package state

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/types"
)

// Inspector provides state introspection capabilities
type Inspector struct {
	statePath string
	graphDB   *graph.DB
}

// NewInspector creates a new state inspector
func NewInspector(statePath string, graphDB *graph.DB) *Inspector {
	return &Inspector{statePath: statePath, graphDB: graphDB}
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
	Activity int              `json:"activity_entries"`
	Inbox    int              `json:"inbox_entries"`
	Outbox   int              `json:"outbox_entries"`
	Signals  int              `json:"signals_entries"`
}

// HealthReport holds health check results
type HealthReport struct {
	Status          string   `json:"status"` // "healthy", "warnings", "issues"
	Warnings        []string `json:"warnings,omitempty"`
	Recommendations []string `json:"recommendations,omitempty"`
}

// Summary returns a summary of all state components
func (i *Inspector) Summary() (*StateSummary, error) {
	summary := &StateSummary{}

	// Count traces from graph DB
	if i.graphDB != nil {
		total, core, err := i.graphDB.CountTraces()
		if err == nil {
			summary.Traces.Total = total
			summary.Traces.Core = core
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
	summary.Activity = i.countJSONL("system/activity.jsonl")
	summary.Inbox = i.countJSONL("system/queues/inbox.jsonl")
	summary.Outbox = i.countJSONL("system/queues/outbox.jsonl")
	summary.Signals = i.countJSONL("system/queues/signals.jsonl")

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

	if summary.Activity > 10000 {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Large activity log: %d entries", summary.Activity))
		report.Recommendations = append(report.Recommendations, "Consider truncating old activity entries")
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
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	traces, err := i.graphDB.GetAllTraces()
	if err != nil {
		return nil, err
	}

	result := make([]TraceSummary, 0, len(traces))
	for _, t := range traces {
		content := t.Summary
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
	if i.graphDB == nil {
		return nil, fmt.Errorf("graph database not initialized")
	}

	gt, err := i.graphDB.GetTrace(id)
	if err != nil {
		return nil, err
	}
	if gt == nil {
		return nil, fmt.Errorf("trace not found: %s", id)
	}

	// Convert graph.Trace to types.Trace
	return &types.Trace{
		ID:         gt.ID,
		Content:    gt.Summary,
		Embedding:  gt.Embedding,
		Activation: gt.Activation,
		Strength:   gt.Strength,
		IsCore:     gt.IsCore,
		CreatedAt:  gt.CreatedAt,
		LastAccess: gt.LastAccessed,
	}, nil
}

// DeleteTrace removes a trace by ID
func (i *Inspector) DeleteTrace(id string) error {
	if i.graphDB == nil {
		return fmt.Errorf("graph database not initialized")
	}
	return i.graphDB.DeleteTrace(id)
}

// ClearTraces clears traces based on filter
func (i *Inspector) ClearTraces(clearCore bool) (int, error) {
	if i.graphDB == nil {
		return 0, fmt.Errorf("graph database not initialized")
	}
	return i.graphDB.ClearTraces(clearCore)
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

// TailLogs returns recent entries from activity log
func (i *Inspector) TailLogs(count int) ([]map[string]any, error) {
	return i.tailJSONL("system/activity.jsonl", count), nil
}

// TruncateLogs keeps only the last N entries in the activity log
func (i *Inspector) TruncateLogs(keep int) error {
	if err := i.truncateJSONL("system/activity.jsonl", keep); err != nil {
		return fmt.Errorf("failed to truncate activity.jsonl: %w", err)
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
		Inbox:   i.countJSONL("system/queues/inbox.jsonl"),
		Outbox:  i.countJSONL("system/queues/outbox.jsonl"),
		Signals: i.countJSONL("system/queues/signals.jsonl"),
	}, nil
}

// ClearQueues clears all queue files
func (i *Inspector) ClearQueues() error {
	queuesPath := filepath.Join(i.statePath, "system", "queues")
	for _, name := range []string{"inbox.jsonl", "outbox.jsonl", "signals.jsonl"} {
		path := filepath.Join(queuesPath, name)
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
	path := filepath.Join(i.statePath, "system", "sessions.json")
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
	path := filepath.Join(i.statePath, "system", "sessions.json")
	return os.WriteFile(path, []byte("{}"), 0644)
}

// RegenCore regenerates core traces from seed file
func (i *Inspector) RegenCore(seedPath string) (int, error) {
	if i.graphDB == nil {
		return 0, fmt.Errorf("graph database not initialized")
	}

	// First clear existing core traces
	cleared, err := i.ClearTraces(true) // clearCore=true
	if err != nil {
		return 0, fmt.Errorf("failed to clear core traces: %w", err)
	}
	_ = cleared // We don't report cleared count, just the new count

	// Read seed file
	file, err := os.Open(seedPath)
	if os.IsNotExist(err) {
		return 0, fmt.Errorf("seed file not found: %s", seedPath)
	}
	if err != nil {
		return 0, err
	}
	defer file.Close()

	// Parse entries separated by "---"
	// Lines starting with # are section headers (for human readability) and are stripped
	var entries []string
	var current strings.Builder
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			if current.Len() > 0 {
				entries = append(entries, strings.TrimSpace(current.String()))
				current.Reset()
			}
		} else if strings.HasPrefix(trimmed, "#") {
			// Skip markdown headers - they're for human readability only
			continue
		} else {
			current.WriteString(line)
			current.WriteString("\n")
		}
	}
	if current.Len() > 0 {
		entries = append(entries, strings.TrimSpace(current.String()))
	}

	if err := scanner.Err(); err != nil {
		return 0, err
	}

	// Create new core traces
	count := 0
	for idx, content := range entries {
		if content == "" {
			continue
		}
		trace := &graph.Trace{
			ID:           fmt.Sprintf("core-%d-%d", time.Now().UnixNano(), idx),
			Summary:      content,
			Activation:   1.0,
			Strength:     100,
			IsCore:       true,
			CreatedAt:    time.Now(),
			LastAccessed: time.Now(),
		}
		if err := i.graphDB.AddTrace(trace); err != nil {
			return count, fmt.Errorf("failed to add trace: %w", err)
		}
		count++
	}

	return count, nil
}

// Helper methods

func (i *Inspector) loadPercepts() ([]*types.Percept, error) {
	path := filepath.Join(i.statePath, "system", "queues", "percepts.json")
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
	path := filepath.Join(i.statePath, "system", "queues", "percepts.json")
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
	path := filepath.Join(i.statePath, "system", "threads.json")
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
	path := filepath.Join(i.statePath, "system", "threads.json")
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
