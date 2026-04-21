package extensions_test

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vthunder/bud2/internal/extensions"
)

// --- fakeRunner ---

// fakeRunner records every RunWorkflow call.
type fakeRunner struct {
	mu    sync.Mutex
	calls []fakeCall
}

type fakeCall struct {
	name   string
	params map[string]any
}

func (r *fakeRunner) RunWorkflow(_ context.Context, name string, params map[string]any) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fakeCall{name: name, params: params})
	return "ok", nil
}

func (r *fakeRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *fakeRunner) last() (fakeCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return fakeCall{}, false
	}
	return r.calls[len(r.calls)-1], true
}

// --- EventBus tests ---

func TestEventBus_ExactMatch(t *testing.T) {
	bus := extensions.NewEventBus()

	var received []extensions.Event
	unsub := bus.Subscribe("bud-core:fetch:after", func(e extensions.Event) {
		received = append(received, e)
	})
	defer unsub()

	bus.Publish(extensions.Event{Topic: "bud-core:fetch:after", Payload: map[string]any{"k": "v"}})
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].Topic != "bud-core:fetch:after" {
		t.Errorf("topic = %q, want %q", received[0].Topic, "bud-core:fetch:after")
	}
}

func TestEventBus_WildcardCapability(t *testing.T) {
	bus := extensions.NewEventBus()

	var received []extensions.Event
	unsub := bus.Subscribe("test-ext:*:after", func(e extensions.Event) {
		received = append(received, e)
	})
	defer unsub()

	// Should match
	bus.Publish(extensions.Event{Topic: "test-ext:fetch:after", Payload: nil})
	bus.Publish(extensions.Event{Topic: "test-ext:store:after", Payload: nil})
	// Should NOT match (wrong ext)
	bus.Publish(extensions.Event{Topic: "other-ext:fetch:after", Payload: nil})
	// Should NOT match (wrong phase)
	bus.Publish(extensions.Event{Topic: "test-ext:fetch:before", Payload: nil})

	if len(received) != 2 {
		t.Fatalf("expected 2 events, got %d", len(received))
	}
}

func TestEventBus_WildcardAll(t *testing.T) {
	bus := extensions.NewEventBus()

	var count int
	unsub := bus.Subscribe("*:*:after", func(e extensions.Event) {
		count++
	})
	defer unsub()

	bus.Publish(extensions.Event{Topic: "a:b:after", Payload: nil})
	bus.Publish(extensions.Event{Topic: "x:y:after", Payload: nil})
	bus.Publish(extensions.Event{Topic: "a:b:before", Payload: nil}) // should NOT match

	if count != 2 {
		t.Errorf("expected 2 events, got %d", count)
	}
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := extensions.NewEventBus()

	var count int
	unsub := bus.Subscribe("a:b:c", func(e extensions.Event) { count++ })

	bus.Publish(extensions.Event{Topic: "a:b:c", Payload: nil})
	unsub()
	bus.Publish(extensions.Event{Topic: "a:b:c", Payload: nil})

	if count != 1 {
		t.Errorf("expected 1 event after unsubscribe, got %d", count)
	}
}

func TestEventBus_MalformedTopic(t *testing.T) {
	bus := extensions.NewEventBus()

	var count int
	bus.Subscribe("a:b:c", func(e extensions.Event) { count++ })

	// Malformed topics should be silently dropped.
	bus.Publish(extensions.Event{Topic: "no-colons", Payload: nil})
	bus.Publish(extensions.Event{Topic: "one:colon", Payload: nil})

	if count != 0 {
		t.Errorf("malformed topic should not match, got %d", count)
	}
}

// --- TriggerDispatcher tests ---

func makeTestExtension(t *testing.T, behaviors []extensions.Behavior) *extensions.Extension {
	t.Helper()
	dir := t.TempDir()
	writeExtensionYAML(t, dir, map[string]any{
		"name":         "test-ext",
		"description":  "trigger test extension",
		"capabilities": map[string]any{},
		"behaviors":    behaviorsToAny(behaviors),
	})
	ext, err := extensions.LoadExtension(dir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}
	return ext
}

// behaviorsToAny converts []Behavior to []any for YAML serialisation via writeExtensionYAML.
func behaviorsToAny(behaviors []extensions.Behavior) []any {
	out := make([]any, len(behaviors))
	for i, b := range behaviors {
		out[i] = map[string]any{
			"name":     b.Name,
			"trigger":  b.Trigger,
			"workflow": b.Workflow,
		}
	}
	return out
}

func TestDispatcher_SlashCommand(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name:    "check",
			Trigger: map[string]any{"slash_command": "test-ext-check"},
			Workflow: "do-check",
		},
	})

	if err := d.RegisterExtension(ctx, ext); err != nil {
		t.Fatalf("RegisterExtension: %v", err)
	}

	// Fire the slash command.
	d.FireSlashCommand("test-ext-check", map[string]any{"user": "alice"})

	if runner.count() != 1 {
		t.Fatalf("expected 1 workflow call, got %d", runner.count())
	}
	call, _ := runner.last()
	if call.name != "do-check" {
		t.Errorf("workflow = %q, want %q", call.name, "do-check")
	}
}

func TestDispatcher_SlashCommand_DefaultName(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name:    "check",
			Trigger: map[string]any{"slash_command": ""},
			Workflow: "do-check",
		},
	})

	d.RegisterExtension(ctx, ext) //nolint

	// Default name is <ext>-<behavior> = "test-ext-check".
	d.FireSlashCommand("test-ext-check", nil)

	if runner.count() != 1 {
		t.Errorf("expected 1 workflow call for default-named slash command, got %d", runner.count())
	}
}

func TestDispatcher_EventTrigger(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name:    "on-fetch-after",
			Trigger: map[string]any{"event": "test-ext:fetch:after"},
			Workflow: "handle-fetch-done",
		},
	})

	d.RegisterExtension(ctx, ext) //nolint

	bus.Publish(extensions.Event{Topic: "test-ext:fetch:after", Payload: map[string]any{"result": "data"}})

	if runner.count() != 1 {
		t.Fatalf("expected 1 workflow call, got %d", runner.count())
	}
	call, _ := runner.last()
	if call.name != "handle-fetch-done" {
		t.Errorf("workflow = %q, want %q", call.name, "handle-fetch-done")
	}
}

func TestDispatcher_WildcardEventTrigger(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name:    "on-any-after",
			Trigger: map[string]any{"event": "test-ext:*:after"},
			Workflow: "handle-any-done",
		},
	})

	d.RegisterExtension(ctx, ext) //nolint

	bus.Publish(extensions.Event{Topic: "test-ext:fetch:after", Payload: nil})
	bus.Publish(extensions.Event{Topic: "test-ext:store:after", Payload: nil})
	bus.Publish(extensions.Event{Topic: "test-ext:fetch:before", Payload: nil}) // should NOT fire

	if runner.count() != 2 {
		t.Errorf("expected 2 workflow calls for wildcard event, got %d", runner.count())
	}
}

func TestDispatcher_EmitCapabilityEvent(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name:    "on-after",
			Trigger: map[string]any{"event": "my-ext:my-cap:after"},
			Workflow: "post-process",
		},
	})

	d.RegisterExtension(ctx, ext) //nolint

	// Before events are fire-and-forget (goroutine); after events are synchronous.
	d.EmitCapabilityEvent("before", "my-ext", "my-cap", map[string]any{"input": "x"})
	// Give the goroutine a moment (before events are best-effort in testing).
	time.Sleep(10 * time.Millisecond)

	d.EmitCapabilityEvent("after", "my-ext", "my-cap", map[string]any{"output": "y"})

	if runner.count() != 1 {
		t.Errorf("expected 1 workflow call for 'after' event, got %d", runner.count())
	}
}

func TestDispatcher_UnregisterExtension(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name:    "check",
			Trigger: map[string]any{"slash_command": "test-ext-check"},
			Workflow: "do-check",
		},
	})

	d.RegisterExtension(ctx, ext) //nolint

	// Fire once — should work.
	d.FireSlashCommand("test-ext-check", nil)
	if runner.count() != 1 {
		t.Fatalf("expected 1 call before unregister, got %d", runner.count())
	}

	// Unregister and fire again — should NOT fire.
	d.UnregisterExtension("test-ext")
	d.FireSlashCommand("test-ext-check", nil)
	if runner.count() != 1 {
		t.Errorf("expected no additional calls after unregister, got %d", runner.count())
	}
}

func TestDispatcher_DisabledExtension(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	dir := t.TempDir()
	writeExtensionYAML(t, dir, map[string]any{
		"name":         "disabled-ext",
		"description":  "disabled extension",
		"capabilities": map[string]any{},
		"behaviors": []any{
			map[string]any{
				"name":     "check",
				"trigger":  map[string]any{"slash_command": "disabled-check"},
				"workflow": "do-check",
			},
		},
	})

	ext, err := extensions.LoadExtension(dir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}
	// Disable the extension.
	ext.StateSet("_enabled", false) //nolint

	d.RegisterExtension(ctx, ext) //nolint

	// Firing the slash command should not invoke the workflow.
	d.FireSlashCommand("disabled-check", nil)
	if runner.count() != 0 {
		t.Errorf("disabled extension: expected 0 calls, got %d", runner.count())
	}
}

// TestDispatcher_ScheduleTrigger verifies that a schedule trigger fires the workflow.
// We use a 1-second ticker cron expression "* * * * *" but need the scheduler
// to fire quickly. The test creates a schedule trigger and verifies it would
// fire at the current minute boundary. We simulate by directly testing parseCron.
func TestCronParsing(t *testing.T) {
	tests := []struct {
		expr string
		t    time.Time
		want bool
	}{
		{"* * * * *", time.Date(2026, 4, 21, 10, 30, 0, 0, time.UTC), true},
		{"*/5 * * * *", time.Date(2026, 4, 21, 10, 30, 0, 0, time.UTC), true},
		{"*/5 * * * *", time.Date(2026, 4, 21, 10, 31, 0, 0, time.UTC), false},
		{"0 9 * * 1", time.Date(2026, 4, 20, 9, 0, 0, 0, time.UTC), true},  // Monday = 1
		{"0 9 * * 1", time.Date(2026, 4, 21, 9, 0, 0, 0, time.UTC), false}, // Tuesday = 2
		{"0 9 * * *", time.Date(2026, 4, 21, 9, 0, 0, 0, time.UTC), true},
		{"0 9 * * *", time.Date(2026, 4, 21, 9, 1, 0, 0, time.UTC), false},
	}

	for _, tt := range tests {
		sched, err := extensions.ParseCron(tt.expr)
		if err != nil {
			t.Errorf("parseCron(%q): %v", tt.expr, err)
			continue
		}
		got := extensions.CronMatches(sched, tt.t)
		if got != tt.want {
			t.Errorf("parseCron(%q).matches(%v) = %v, want %v", tt.expr, tt.t, got, tt.want)
		}
	}
}

// TestDispatcher_PatternMatch verifies the pattern_match trigger fires on matching content.
func TestDispatcher_PatternMatch(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name: "on-hello",
			Trigger: map[string]any{
				"pattern_match": map[string]any{
					"pattern": "^hello",
				},
			},
			Workflow: "handle-hello",
		},
	})

	d.RegisterExtension(ctx, ext) //nolint

	d.FirePercept("discord", "message", "hello world", nil)
	d.FirePercept("discord", "message", "bye world", nil) // should not match

	if runner.count() != 1 {
		t.Errorf("expected 1 workflow call for pattern_match, got %d", runner.count())
	}
	call, _ := runner.last()
	if call.name != "handle-hello" {
		t.Errorf("workflow = %q, want %q", call.name, "handle-hello")
	}
}

// TestOnResult_ParseJsonCondition verifies on_result with parse=json and a condition.
func TestOnResult_ParseJsonCondition(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	cfg := extensions.OnResultConfig{
		Parse:     "json",
		Condition: `result["status"] == "ok"`,
		Action:    "invoke",
		Workflow:  "on-success",
	}

	// Should fire — condition is true.
	d.HandleOnResult(ctx, []extensions.OnResultConfig{cfg}, `{"status":"ok","value":42}`, nil)
	if runner.count() != 1 {
		t.Errorf("expected 1 invoke for true condition, got %d", runner.count())
	}

	// Should NOT fire — condition is false.
	d.HandleOnResult(ctx, []extensions.OnResultConfig{cfg}, `{"status":"error"}`, nil)
	if runner.count() != 1 {
		t.Errorf("expected no additional invoke for false condition, got %d", runner.count())
	}
}

func TestOnResult_ParseJsonFails(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	cfg := extensions.OnResultConfig{
		Parse:    "json",
		Action:   "invoke",
		Workflow: "on-success",
	}

	// Non-JSON string — should skip the handler without error.
	d.HandleOnResult(ctx, []extensions.OnResultConfig{cfg}, "not json at all", nil)
	if runner.count() != 0 {
		t.Errorf("expected 0 invokes when json parse fails, got %d", runner.count())
	}
}

func TestOnResult_NoCondition(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	cfg := extensions.OnResultConfig{
		Action:   "invoke",
		Workflow: "always-run",
	}

	d.HandleOnResult(ctx, []extensions.OnResultConfig{cfg}, "any result", nil)
	if runner.count() != 1 {
		t.Errorf("expected 1 invoke when no condition, got %d", runner.count())
	}
}

func TestOnResult_Notify(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)

	var notified []string
	d.SetTalkToUser(&fakeTalker{fn: func(msg string) error {
		notified = append(notified, msg)
		return nil
	}})

	ctx := context.Background()

	cfg := extensions.OnResultConfig{
		Action:  "notify",
		Message: "done: {{result}}",
	}

	d.HandleOnResult(ctx, []extensions.OnResultConfig{cfg}, "success", map[string]any{})

	if len(notified) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notified))
	}
	if notified[0] != "done: success" {
		t.Errorf("notification = %q, want %q", notified[0], "done: success")
	}
}

func TestOnResult_Log(t *testing.T) {
	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)

	var logged []string
	d.SetSaveThought(&fakeLogger{fn: func(msg string) error {
		logged = append(logged, msg)
		return nil
	}})

	ctx := context.Background()

	cfg := extensions.OnResultConfig{
		Action:  "log",
		Message: "logged: {{result}}",
	}

	d.HandleOnResult(ctx, []extensions.OnResultConfig{cfg}, "my-result", map[string]any{})

	if len(logged) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(logged))
	}
	if logged[0] != "logged: my-result" {
		t.Errorf("log = %q, want %q", logged[0], "logged: my-result")
	}
}

// TestDispatcher_Schedule exercises the schedule trigger by creating one and
// verifying it fires within the expected time window.
// This test runs a fast-ticking cron (*/1 * * * *) but shortcircuits by
// testing the matching logic directly rather than waiting 90 seconds.
func TestDispatcher_ScheduleFires(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping schedule integration test in short mode")
	}

	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name:    "every-minute",
			Trigger: map[string]any{"schedule": "* * * * *"},
			Workflow: "scheduled-task",
		},
	})

	if err := d.RegisterExtension(ctx, ext); err != nil {
		t.Fatalf("RegisterExtension: %v", err)
	}

	// Wait for at least one firing within 90s.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if runner.count() > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if runner.count() == 0 {
		t.Error("schedule trigger did not fire within 90 seconds")
	}

	d.Stop()
}

// TestDispatcher_Condition verifies the condition trigger fires when the expression
// evaluates to true.
func TestDispatcher_Condition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping condition integration test in short mode")
	}

	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	reg := newMinimalRegistry(t)
	d := extensions.NewDispatcher(reg, bus, runner)

	var fired int64

	// Override runner to count directly.
	countRunner := &countingRunner{fn: func(name string) { atomic.AddInt64(&fired, 1) }}

	d2 := extensions.NewDispatcher(reg, bus, countRunner)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ext := makeTestExtension(t, []extensions.Behavior{
		{
			Name: "always-true",
			Trigger: map[string]any{
				"condition": map[string]any{
					"expr":     "true",
					"interval": "1s",
				},
			},
			Workflow: "cond-task",
		},
	})

	_ = d // d is unused here; d2 is used
	if err := d2.RegisterExtension(ctx, ext); err != nil {
		t.Fatalf("RegisterExtension: %v", err)
	}

	// Wait for at least one fire.
	for i := 0; i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		if atomic.LoadInt64(&fired) > 0 {
			break
		}
	}

	if atomic.LoadInt64(&fired) == 0 {
		t.Error("condition trigger did not fire within 2 seconds")
	}

	d2.Stop()
}

// --- fakes ---

type fakeTalker struct{ fn func(string) error }

func (f *fakeTalker) Notify(msg string) error { return f.fn(msg) }

type fakeLogger struct{ fn func(string) error }

func (f *fakeLogger) Log(msg string) error { return f.fn(msg) }

type countingRunner struct{ fn func(name string) }

func (r *countingRunner) RunWorkflow(_ context.Context, name string, _ map[string]any) (any, error) {
	r.fn(name)
	return nil, nil
}

// newMinimalRegistry creates an empty registry for tests that need one.
func newMinimalRegistry(t *testing.T) *extensions.Registry {
	t.Helper()
	dir := t.TempDir()
	reg, err := extensions.LoadAll(dir, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return reg
}

// TestBudOps_TriggerDispatcher verifies behavior registration for the bud-ops
// fixture (which has no behaviors) doesn't fail and returns cleanly.
func TestBudOps_TriggerDispatcher(t *testing.T) {
	root := repoRoot(t)
	budOpsPlugin := root + "/state-defaults/system/plugins/bud-ops"
	if _, err := os.Stat(budOpsPlugin); err != nil {
		t.Skipf("bud-ops plugin not found: %v", err)
	}

	outDir := t.TempDir()
	runMigratePlugin(t, budOpsPlugin, outDir)

	ext, err := extensions.LoadExtension(outDir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}

	bus := extensions.NewEventBus()
	runner := &fakeRunner{}
	systemDir := t.TempDir()
	reg, err := extensions.LoadAll(systemDir, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	d := extensions.NewDispatcher(reg, bus, runner)
	ctx := context.Background()

	if err := d.RegisterExtension(ctx, ext); err != nil {
		t.Fatalf("RegisterExtension for bud-ops: %v", err)
	}

	// bud-ops has no behaviors, so no triggers should be registered.
	// Just verify no panic and clean teardown.
	d.Stop()
}
