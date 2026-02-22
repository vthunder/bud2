package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// helper: create a Log backed by a temp directory
func newTestLog(t *testing.T) (*Log, string) {
	t.Helper()
	dir := t.TempDir()
	sysDir := filepath.Join(dir, "system")
	if err := os.MkdirAll(sysDir, 0755); err != nil {
		t.Fatal(err)
	}
	return New(dir), filepath.Join(sysDir, "activity.jsonl")
}

// helper: read all raw entries from the JSONL file
func readEntries(t *testing.T, path string) []Entry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	var entries []Entry
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		entries = append(entries, e)
	}
	return entries
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// --- Basic write/read ---

func TestLog_WritesJSONL(t *testing.T) {
	log, path := newTestLog(t)

	ts := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	err := log.Log(Entry{
		Timestamp: ts,
		Type:      TypeInput,
		Summary:   "hello world",
		Source:    "test",
	})
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	entries := readEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Type != TypeInput {
		t.Errorf("type: got %q, want %q", e.Type, TypeInput)
	}
	if e.Summary != "hello world" {
		t.Errorf("summary: got %q", e.Summary)
	}
	if e.Source != "test" {
		t.Errorf("source: got %q", e.Source)
	}
	if !e.Timestamp.Equal(ts) {
		t.Errorf("timestamp: got %v, want %v", e.Timestamp, ts)
	}
}

func TestLog_AutoTimestamp(t *testing.T) {
	log, path := newTestLog(t)

	before := time.Now()
	if err := log.Log(Entry{Type: TypeAction, Summary: "auto-ts"}); err != nil {
		t.Fatal(err)
	}
	after := time.Now()

	entries := readEntries(t, path)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry")
	}
	ts := entries[0].Timestamp
	if ts.Before(before) || ts.After(after) {
		t.Errorf("auto-timestamp %v not in [%v, %v]", ts, before, after)
	}
}

func TestLog_MultipleEntries(t *testing.T) {
	log, path := newTestLog(t)

	for i := 0; i < 5; i++ {
		if err := log.Log(Entry{Type: TypeAction, Summary: "entry"}); err != nil {
			t.Fatalf("entry %d: %v", i, err)
		}
	}

	entries := readEntries(t, path)
	if len(entries) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(entries))
	}
}

func TestLog_SkipsMalformedLines(t *testing.T) {
	log, path := newTestLog(t)

	// Write one good entry
	if err := log.Log(Entry{Type: TypeAction, Summary: "good"}); err != nil {
		t.Fatal(err)
	}

	// Inject a malformed line
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not json at all\n")
	f.Close()

	// Write another good entry
	if err := log.Log(Entry{Type: TypeAction, Summary: "good2"}); err != nil {
		t.Fatal(err)
	}

	// readAll should return 2 good entries, skip the malformed one
	entries, err := log.readAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("expected 2 entries, got %d", len(entries))
	}
}

func TestLog_EmptyFileReturnsNil(t *testing.T) {
	log, _ := newTestLog(t)
	entries, err := log.readAll()
	if err != nil {
		t.Fatalf("readAll on missing file: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries for missing file")
	}
}

// --- Helper methods ---

func TestLogInput(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogInput("input summary", "discord", "ch123"); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeInput {
		t.Errorf("type: %q", e.Type)
	}
	if e.Source != "discord" || e.Channel != "ch123" {
		t.Errorf("source/channel: %q/%q", e.Source, e.Channel)
	}
}

func TestLogReflex(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogReflex("reflex summary", "weather", "what's the weather", "sunny"); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeReflex {
		t.Errorf("type: %q", e.Type)
	}
	if e.Intent != "weather" {
		t.Errorf("intent: %q", e.Intent)
	}
	if e.Data["query"] != "what's the weather" {
		t.Errorf("query in data: %v", e.Data["query"])
	}
	if e.Data["response"] != "sunny" {
		t.Errorf("response in data: %v", e.Data["response"])
	}
}

func TestLogReflexPass(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogReflexPass("pass", "unknown", "what is 42?"); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeReflexPass {
		t.Errorf("type: %q", e.Type)
	}
	if e.Data["query"] != "what is 42?" {
		t.Errorf("query: %v", e.Data["query"])
	}
}

func TestLogExecWake(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogExecWake("wake", "thread-1", "handle user request"); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeExecWake {
		t.Errorf("type: %q", e.Type)
	}
	if e.ThreadID != "thread-1" {
		t.Errorf("threadID: %q", e.ThreadID)
	}
	if e.Data["context"] != "handle user request" {
		t.Errorf("context: %v", e.Data["context"])
	}
}

func TestLogExecDone(t *testing.T) {
	log, _ := newTestLog(t)
	extra := map[string]any{"tokens": 42}
	if err := log.LogExecDone("done", "thread-1", 1.5, "executive", extra); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeExecDone {
		t.Errorf("type: %q", e.Type)
	}
	if e.Data["duration_sec"] != 1.5 {
		t.Errorf("duration_sec: %v", e.Data["duration_sec"])
	}
	if e.Data["completion"] != "executive" {
		t.Errorf("completion: %v", e.Data["completion"])
	}
	// extra data merged in
	if v, ok := e.Data["tokens"]; !ok || v != float64(42) {
		// JSON roundtrip makes numbers float64
		t.Errorf("tokens: %v", e.Data["tokens"])
	}
}

func TestLogExecDone_NilExtra(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogExecDone("done", "t1", 2.0, "executive", nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry")
	}
}

func TestLogAction(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogAction("sent msg", "bud", "ch1", "hello there"); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeAction {
		t.Errorf("type: %q", e.Type)
	}
	if e.Data["content"] != "hello there" {
		t.Errorf("content: %v", e.Data["content"])
	}
}

func TestLogDecision(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogDecision("decided", "because reasons", "some context", "outcome was good"); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeDecision {
		t.Errorf("type: %q", e.Type)
	}
	if e.Reasoning != "because reasons" {
		t.Errorf("reasoning: %q", e.Reasoning)
	}
}

func TestLogError(t *testing.T) {
	log, _ := newTestLog(t)
	import_err := os.ErrNotExist
	if err := log.LogError("something failed", import_err, map[string]any{"file": "foo.txt"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	e := entries[0]
	if e.Type != TypeError {
		t.Errorf("type: %q", e.Type)
	}
	if e.Data["error"] != import_err.Error() {
		t.Errorf("error field: %v", e.Data["error"])
	}
	if e.Data["file"] != "foo.txt" {
		t.Errorf("file field: %v", e.Data["file"])
	}
}

func TestLogError_NilData(t *testing.T) {
	log, _ := newTestLog(t)
	if err := log.LogError("failed", os.ErrPermission, nil); err != nil {
		t.Fatal(err)
	}
	entries, _ := log.readAll()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry")
	}
}

// --- Query: Recent ---

func TestRecent_Basic(t *testing.T) {
	log, _ := newTestLog(t)
	for i := 0; i < 10; i++ {
		log.Log(Entry{Type: TypeAction, Summary: "entry"})
	}
	entries, err := log.Recent(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3, got %d", len(entries))
	}
}

func TestRecent_FewerThanN(t *testing.T) {
	log, _ := newTestLog(t)
	log.Log(Entry{Type: TypeAction, Summary: "only one"})
	entries, err := log.Recent(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1, got %d", len(entries))
	}
}

func TestRecent_Empty(t *testing.T) {
	log, _ := newTestLog(t)
	entries, err := log.Recent(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0, got %d", len(entries))
	}
}

// --- Query: Today ---

func TestToday_ReturnsRecentEntries(t *testing.T) {
	log, _ := newTestLog(t)

	// Today's entry
	log.Log(Entry{Type: TypeAction, Summary: "today's entry", Timestamp: time.Now()})

	// Yesterday's entry (should be excluded)
	yesterday := time.Now().AddDate(0, 0, -1)
	log.Log(Entry{Type: TypeAction, Summary: "yesterday", Timestamp: yesterday})

	entries, err := log.Today()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 today entry, got %d", len(entries))
	}
	if entries[0].Summary != "today's entry" {
		t.Errorf("unexpected entry: %q", entries[0].Summary)
	}
}

// --- Query: Search ---

func TestSearch_BySummary(t *testing.T) {
	log, _ := newTestLog(t)
	log.Log(Entry{Type: TypeAction, Summary: "the quick brown fox"})
	log.Log(Entry{Type: TypeAction, Summary: "something else entirely"})

	results, err := log.Search("quick brown", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearch_ByData(t *testing.T) {
	log, _ := newTestLog(t)
	log.Log(Entry{
		Type:    TypeAction,
		Summary: "nope",
		Data:    map[string]any{"key": "unicorn_value"},
	})

	results, err := log.Search("unicorn_value", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearch_ByReasoning(t *testing.T) {
	log, _ := newTestLog(t)
	log.Log(Entry{
		Type:      TypeDecision,
		Summary:   "nope",
		Reasoning: "because unicorns exist",
	})

	results, err := log.Search("unicorns", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
}

func TestSearch_CaseInsensitive(t *testing.T) {
	log, _ := newTestLog(t)
	log.Log(Entry{Type: TypeAction, Summary: "UPPERCASE WORDS"})

	results, err := log.Search("uppercase", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1, got %d", len(results))
	}
}

func TestSearch_Limit(t *testing.T) {
	log, _ := newTestLog(t)
	for i := 0; i < 10; i++ {
		log.Log(Entry{Type: TypeAction, Summary: "matching entry"})
	}
	results, err := log.Search("matching", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3, got %d", len(results))
	}
}

// --- Query: ByType ---

func TestByType_Basic(t *testing.T) {
	log, _ := newTestLog(t)
	log.Log(Entry{Type: TypeAction, Summary: "action1"})
	log.Log(Entry{Type: TypeInput, Summary: "input1"})
	log.Log(Entry{Type: TypeAction, Summary: "action2"})

	results, err := log.ByType(TypeAction, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2, got %d", len(results))
	}
	for _, e := range results {
		if e.Type != TypeAction {
			t.Errorf("unexpected type: %q", e.Type)
		}
	}
}

func TestByType_Limit(t *testing.T) {
	log, _ := newTestLog(t)
	for i := 0; i < 5; i++ {
		log.Log(Entry{Type: TypeAction, Summary: "action"})
	}
	results, err := log.ByType(TypeAction, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2, got %d", len(results))
	}
}

// --- Query: Range ---

func TestRange_Basic(t *testing.T) {
	log, _ := newTestLog(t)

	base := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	log.Log(Entry{Type: TypeAction, Summary: "before", Timestamp: base.Add(-2 * time.Hour)})
	log.Log(Entry{Type: TypeAction, Summary: "in-range", Timestamp: base})
	log.Log(Entry{Type: TypeAction, Summary: "after", Timestamp: base.Add(2 * time.Hour)})

	start := base.Add(-30 * time.Minute)
	end := base.Add(30 * time.Minute)
	results, err := log.Range(start, end)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1, got %d", len(results))
	}
	if results[0].Summary != "in-range" {
		t.Errorf("unexpected: %q", results[0].Summary)
	}
}

// --- Concurrency ---

func TestConcurrentWrites(t *testing.T) {
	log, path := newTestLog(t)

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			log.Log(Entry{Type: TypeAction, Summary: "concurrent"})
		}()
	}
	wg.Wait()

	entries := readEntries(t, path)
	if len(entries) != n {
		t.Errorf("expected %d entries, got %d (possible corruption)", n, len(entries))
	}
}
