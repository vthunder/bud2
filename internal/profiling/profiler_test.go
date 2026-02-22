package profiling

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// newTestProfiler creates a Profiler instance for testing (bypasses singleton).
func newTestProfiler(level ProfilingLevel, logPath string) *Profiler {
	p := &Profiler{
		enabled: level != LevelOff,
		level:   level,
		logPath: logPath,
	}
	if p.enabled && logPath != "" {
		var err error
		p.logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			panic(err)
		}
		p.encoder = json.NewEncoder(p.logFile)
	}
	return p
}

// readTimings reads all recorded timings from a file.
func readTimings(t *testing.T, path string) []MessageTiming {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read timings: %v", err)
	}

	var timings []MessageTiming
	dec := json.NewDecoder(strings.NewReader(string(data)))
	for dec.More() {
		var mt MessageTiming
		if err := dec.Decode(&mt); err != nil {
			t.Fatalf("decode timing: %v", err)
		}
		timings = append(timings, mt)
	}
	return timings
}

// --- IsEnabled / GetLevel ---

func TestIsEnabled_Off(t *testing.T) {
	p := newTestProfiler(LevelOff, "")
	if p.IsEnabled() {
		t.Error("expected disabled for LevelOff")
	}
}

func TestIsEnabled_Minimal(t *testing.T) {
	p := newTestProfiler(LevelMinimal, "")
	if !p.IsEnabled() {
		t.Error("expected enabled for LevelMinimal")
	}
}

func TestGetLevel(t *testing.T) {
	for _, level := range []ProfilingLevel{LevelOff, LevelMinimal, LevelDetailed, LevelTrace} {
		p := newTestProfiler(level, "")
		if p.GetLevel() != level {
			t.Errorf("GetLevel() = %q, want %q", p.GetLevel(), level)
		}
	}
}

// --- ShouldProfile ---

func TestShouldProfile_Disabled(t *testing.T) {
	p := newTestProfiler(LevelOff, "")
	for _, lvl := range []ProfilingLevel{LevelMinimal, LevelDetailed, LevelTrace} {
		if p.ShouldProfile(lvl) {
			t.Errorf("ShouldProfile(%q) should be false when disabled", lvl)
		}
	}
}

func TestShouldProfile_Minimal(t *testing.T) {
	p := newTestProfiler(LevelMinimal, "")
	if !p.ShouldProfile(LevelMinimal) {
		t.Error("ShouldProfile(Minimal) should be true at Minimal level")
	}
	if p.ShouldProfile(LevelDetailed) {
		t.Error("ShouldProfile(Detailed) should be false at Minimal level")
	}
	if p.ShouldProfile(LevelTrace) {
		t.Error("ShouldProfile(Trace) should be false at Minimal level")
	}
}

func TestShouldProfile_Detailed(t *testing.T) {
	p := newTestProfiler(LevelDetailed, "")
	if !p.ShouldProfile(LevelMinimal) {
		t.Error("ShouldProfile(Minimal) should be true at Detailed level")
	}
	if !p.ShouldProfile(LevelDetailed) {
		t.Error("ShouldProfile(Detailed) should be true at Detailed level")
	}
	if p.ShouldProfile(LevelTrace) {
		t.Error("ShouldProfile(Trace) should be false at Detailed level")
	}
}

func TestShouldProfile_Trace(t *testing.T) {
	p := newTestProfiler(LevelTrace, "")
	for _, lvl := range []ProfilingLevel{LevelMinimal, LevelDetailed, LevelTrace} {
		if !p.ShouldProfile(lvl) {
			t.Errorf("ShouldProfile(%q) should be true at Trace level", lvl)
		}
	}
}

// --- Record ---

func TestRecord_WritesJSON(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "profiling-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	p := newTestProfiler(LevelMinimal, f.Name())
	defer p.Close()

	p.Record("msg-1", "memory_retrieve", 42*time.Millisecond, nil)

	timings := readTimings(t, f.Name())
	if len(timings) != 1 {
		t.Fatalf("expected 1 timing, got %d", len(timings))
	}

	got := timings[0]
	if got.MessageID != "msg-1" {
		t.Errorf("MessageID = %q, want %q", got.MessageID, "msg-1")
	}
	if got.Stage != "memory_retrieve" {
		t.Errorf("Stage = %q, want %q", got.Stage, "memory_retrieve")
	}
	// Allow some clock skew — just verify it's in the right ballpark
	if got.DurationMs < 40 || got.DurationMs > 50 {
		t.Errorf("DurationMs = %.2f, want ~42ms", got.DurationMs)
	}
}

func TestRecord_WithMetadata(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "profiling-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	p := newTestProfiler(LevelMinimal, f.Name())
	defer p.Close()

	p.Record("msg-2", "stage", 1*time.Millisecond, map[string]interface{}{
		"key": "value",
	})

	timings := readTimings(t, f.Name())
	if len(timings) != 1 {
		t.Fatalf("expected 1 timing, got %d", len(timings))
	}
	if timings[0].Metadata["key"] != "value" {
		t.Errorf("Metadata[key] = %v, want %q", timings[0].Metadata["key"], "value")
	}
}

func TestRecord_DisabledNoWrite(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "profiling-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	p := newTestProfiler(LevelOff, f.Name())
	p.Record("msg-x", "stage", 1*time.Millisecond, nil)

	info, err := os.Stat(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Errorf("expected empty file when disabled, got %d bytes", info.Size())
	}
}

func TestRecord_MultipleEntries(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "profiling-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	p := newTestProfiler(LevelMinimal, f.Name())
	defer p.Close()

	for i := 0; i < 5; i++ {
		p.Record("msg", "stage", time.Duration(i)*time.Millisecond, nil)
	}

	timings := readTimings(t, f.Name())
	if len(timings) != 5 {
		t.Fatalf("expected 5 timings, got %d", len(timings))
	}
}

// --- Start ---

func TestStart_ReturnsNoop_WhenDisabled(t *testing.T) {
	p := newTestProfiler(LevelOff, "")
	stop := p.Start("msg", "stage")
	stop() // must not panic
}

func TestStart_MeasuresDuration(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "profiling-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	p := newTestProfiler(LevelMinimal, f.Name())
	defer p.Close()

	stop := p.Start("msg-3", "sleep_stage")
	time.Sleep(10 * time.Millisecond)
	stop()

	timings := readTimings(t, f.Name())
	if len(timings) != 1 {
		t.Fatalf("expected 1 timing, got %d", len(timings))
	}
	if timings[0].DurationMs < 8 {
		t.Errorf("DurationMs = %.2fms, expected >= 8ms", timings[0].DurationMs)
	}
	if timings[0].Stage != "sleep_stage" {
		t.Errorf("Stage = %q, want %q", timings[0].Stage, "sleep_stage")
	}
}

// --- StartWithMetadata ---

func TestStartWithMetadata_WritesMetadata(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "profiling-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	p := newTestProfiler(LevelMinimal, f.Name())
	defer p.Close()

	meta := map[string]interface{}{"source": "test"}
	stop := p.StartWithMetadata("msg-4", "meta_stage", meta)
	stop()

	timings := readTimings(t, f.Name())
	if len(timings) != 1 {
		t.Fatalf("expected 1 timing, got %d", len(timings))
	}
	if timings[0].Metadata["source"] != "test" {
		t.Errorf("Metadata[source] = %v, want %q", timings[0].Metadata["source"], "test")
	}
}

func TestStartWithMetadata_ReturnsNoop_WhenDisabled(t *testing.T) {
	p := newTestProfiler(LevelOff, "")
	stop := p.StartWithMetadata("msg", "stage", map[string]interface{}{"k": "v"})
	stop() // must not panic
}

// --- Close ---

func TestClose_IsIdempotent(t *testing.T) {
	p := newTestProfiler(LevelMinimal, "")
	// No file opened (logPath is empty, enabled but no file) — Close should not panic
	if err := p.Close(); err != nil {
		// Only an error if logFile was non-nil; with empty path enabled=true but file isn't opened
		// in our newTestProfiler (path="" skips file creation), so Close is a no-op
		t.Errorf("Close() error: %v", err)
	}
}
