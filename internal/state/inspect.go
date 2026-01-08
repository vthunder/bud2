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
	Status          string   `json:"status"` // "healthy", "warnings", "issues"
	Warnings        []string `json:"warnings,omitempty"`
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
		return 0, err
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
