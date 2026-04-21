package extensions

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/expr-lang/expr"
)

// WorkflowRunner executes a named workflow with given params and returns the result.
// The Dispatcher calls this to fire behaviors.
type WorkflowRunner interface {
	RunWorkflow(ctx context.Context, name string, params map[string]any) (any, error)
}

// PatternMatcher checks whether content from a given source/type matches a reflex trigger.
// The Dispatcher delegates pattern_match triggers to this.
type PatternMatcher interface {
	// MatchAndRun attempts to match content against the named reflex (by name) and execute it.
	// Returns (matched, error).
	MatchAndRun(ctx context.Context, name string, source, typ, content string, data map[string]any) (bool, error)
}

// TalkToUser sends a notification message to the user (used by on_result:action=notify).
type TalkToUser interface {
	Notify(message string) error
}

// SaveThought persists a log entry (used by on_result:action=log).
type SaveThought interface {
	Log(message string) error
}

// Dispatcher registers and fires behaviors from all loaded extensions.
// It owns the EventBus and is the authority on trigger lifecycle.
type Dispatcher struct {
	registry *Registry
	bus      *EventBus
	runner   WorkflowRunner
	talker   TalkToUser
	logger   SaveThought

	mu          sync.Mutex
	cancelFuncs map[string][]func() // extName → cancel/unsubscribe funcs
	stopCh      chan struct{}
	wg          sync.WaitGroup
}

// NewDispatcher creates a Dispatcher. Call RegisterAll or RegisterExtension to wire
// behaviors, then Start to begin condition-polling loops.
func NewDispatcher(registry *Registry, bus *EventBus, runner WorkflowRunner) *Dispatcher {
	return &Dispatcher{
		registry:    registry,
		bus:         bus,
		runner:      runner,
		cancelFuncs: make(map[string][]func()),
		stopCh:      make(chan struct{}),
	}
}

// SetTalkToUser wires the on_result:action=notify callback.
func (d *Dispatcher) SetTalkToUser(t TalkToUser) { d.talker = t }

// SetSaveThought wires the on_result:action=log callback.
func (d *Dispatcher) SetSaveThought(s SaveThought) { d.logger = s }

// RegisterAll registers behaviors for every extension currently in the registry.
// Call this once after initial load.
func (d *Dispatcher) RegisterAll(ctx context.Context) {
	for _, ext := range d.registry.All() {
		if err := d.RegisterExtension(ctx, ext); err != nil {
			log.Printf("[triggers] %s: registration failed: %v", ext.Manifest.Name, err)
		}
	}
}

// RegisterExtension registers all behaviors declared in ext.Manifest.Behaviors.
// Idempotent — existing registrations for the extension are removed first.
func (d *Dispatcher) RegisterExtension(ctx context.Context, ext *Extension) error {
	d.UnregisterExtension(ext.Manifest.Name)

	// Skip if extension is disabled.
	if !ext.Enabled() {
		log.Printf("[triggers] %s: disabled, skipping behavior registration", ext.Manifest.Name)
		return nil
	}

	var cancels []func()

	for _, beh := range ext.Manifest.Behaviors {
		beh := beh // capture
		extName := ext.Manifest.Name
		workflow := beh.Workflow

		triggerType, triggerVal := parseTriggerType(beh.Trigger)
		switch triggerType {

		case "schedule":
			cronExpr, _ := triggerVal.(string)
			if cronExpr == "" {
				log.Printf("[triggers] %s/%s: schedule trigger missing cron expression", extName, beh.Name)
				continue
			}
			sched, err := parseCron(cronExpr)
			if err != nil {
				log.Printf("[triggers] %s/%s: invalid cron %q: %v", extName, beh.Name, cronExpr, err)
				continue
			}

			stopCh := make(chan struct{})
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				runSchedule(ctx, sched, stopCh, func() {
					log.Printf("[triggers] %s/%s: schedule fired, running %q", extName, beh.Name, workflow)
					if _, err := d.runner.RunWorkflow(ctx, workflow, nil); err != nil {
						log.Printf("[triggers] %s/%s: workflow %q error: %v", extName, beh.Name, workflow, err)
					}
				})
			}()
			cancels = append(cancels, func() { close(stopCh) })

		case "slash_command":
			cmdName, _ := triggerVal.(string)
			if cmdName == "" {
				// Default: <ext>-<behavior>
				cmdName = extName + "-" + beh.Name
			}

			unsub := d.bus.Subscribe("slash_command:"+cmdName+":invoke", func(e Event) {
				log.Printf("[triggers] %s/%s: slash_command /%s fired, running %q", extName, beh.Name, cmdName, workflow)
				params, _ := e.Payload["data"].(map[string]any)
				if _, err := d.runner.RunWorkflow(ctx, workflow, params); err != nil {
					log.Printf("[triggers] %s/%s: workflow %q error: %v", extName, beh.Name, workflow, err)
				}
			})
			cancels = append(cancels, unsub)
			log.Printf("[triggers] %s/%s: registered slash command /%s → %q", extName, beh.Name, cmdName, workflow)

		case "event":
			topic, _ := triggerVal.(string)
			if topic == "" {
				log.Printf("[triggers] %s/%s: event trigger missing topic", extName, beh.Name)
				continue
			}

			unsub := d.bus.Subscribe(topic, func(e Event) {
				log.Printf("[triggers] %s/%s: event %q fired, running %q", extName, beh.Name, e.Topic, workflow)
				if _, err := d.runner.RunWorkflow(ctx, workflow, e.Payload); err != nil {
					log.Printf("[triggers] %s/%s: workflow %q error: %v", extName, beh.Name, workflow, err)
				}
			})
			cancels = append(cancels, unsub)
			log.Printf("[triggers] %s/%s: subscribed to event %q → %q", extName, beh.Name, topic, workflow)

		case "condition":
			condMap, _ := triggerVal.(map[string]any)
			if condMap == nil {
				log.Printf("[triggers] %s/%s: condition trigger missing map", extName, beh.Name)
				continue
			}
			exprStr, _ := condMap["expr"].(string)
			intervalStr, _ := condMap["interval"].(string)
			if exprStr == "" {
				log.Printf("[triggers] %s/%s: condition trigger missing expr", extName, beh.Name)
				continue
			}

			interval := time.Second
			if intervalStr != "" {
				if d, err := time.ParseDuration(intervalStr); err == nil && d >= time.Second {
					interval = d
				}
			}

			stopCh := make(chan struct{})
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				runCondition(ctx, exprStr, interval, stopCh, func(condVars map[string]any) {
					log.Printf("[triggers] %s/%s: condition %q matched, running %q", extName, beh.Name, exprStr, workflow)
					if _, err := d.runner.RunWorkflow(ctx, workflow, condVars); err != nil {
						log.Printf("[triggers] %s/%s: workflow %q error: %v", extName, beh.Name, workflow, err)
					}
				})
			}()
			cancels = append(cancels, func() { close(stopCh) })

		case "pattern_match":
			// pattern_match triggers are handled by the existing reflex engine.
			// We register them as event subscriptions on the internal pattern-match bus.
			patternMap, _ := triggerVal.(map[string]any)
			if patternMap == nil {
				log.Printf("[triggers] %s/%s: pattern_match trigger missing map", extName, beh.Name)
				continue
			}
			pattern, _ := patternMap["pattern"].(string)
			if pattern == "" {
				log.Printf("[triggers] %s/%s: pattern_match trigger missing pattern", extName, beh.Name)
				continue
			}

			// Compile pattern at registration time to detect errors early.
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				log.Printf("[triggers] %s/%s: invalid regex %q: %v", extName, beh.Name, pattern, err)
				continue
			}

			source, _ := patternMap["source"].(string)
			typ, _ := patternMap["type"].(string)
			classifier, _ := patternMap["classifier"].(string)
			if classifier == "" {
				classifier = "regex"
			}

			unsub := d.bus.Subscribe("percept:message:received", func(e Event) {
				content, _ := e.Payload["content"].(string)
				src, _ := e.Payload["source"].(string)
				t, _ := e.Payload["type"].(string)

				// Apply source/type filters.
				if source != "" && src != source {
					return
				}
				if typ != "" && t != typ {
					return
				}

				// Regex classifier.
				if classifier == "regex" {
					if !compiled.MatchString(content) {
						return
					}
				}
				// For "none", always match. "ollama" not implemented here (would need spawner).
				// classifier="none" falls through.

				log.Printf("[triggers] %s/%s: pattern matched, running %q", extName, beh.Name, workflow)
				data := make(map[string]any, len(e.Payload))
				for k, v := range e.Payload {
					data[k] = v
				}
				if _, err := d.runner.RunWorkflow(ctx, workflow, data); err != nil {
					log.Printf("[triggers] %s/%s: workflow %q error: %v", extName, beh.Name, workflow, err)
				}
			})
			cancels = append(cancels, unsub)
			log.Printf("[triggers] %s/%s: registered pattern_match %q → %q", extName, beh.Name, pattern, workflow)

		case "manual":
			// No registration needed — manual behaviors are invoked explicitly.
			log.Printf("[triggers] %s/%s: manual trigger (no auto-registration)", extName, beh.Name)

		default:
			log.Printf("[triggers] %s/%s: unknown trigger type %q", extName, beh.Name, triggerType)
		}
	}

	d.mu.Lock()
	d.cancelFuncs[ext.Manifest.Name] = cancels
	d.mu.Unlock()

	return nil
}

// UnregisterExtension removes all trigger registrations for the named extension.
func (d *Dispatcher) UnregisterExtension(name string) {
	d.mu.Lock()
	cancels := d.cancelFuncs[name]
	delete(d.cancelFuncs, name)
	d.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

// Stop cancels all trigger registrations and waits for background goroutines to exit.
func (d *Dispatcher) Stop() {
	// Collect all cancel funcs under the lock, then call them without holding it.
	d.mu.Lock()
	var allCancels []func()
	for name, cancels := range d.cancelFuncs {
		allCancels = append(allCancels, cancels...)
		delete(d.cancelFuncs, name)
	}
	d.mu.Unlock()

	for _, cancel := range allCancels {
		cancel()
	}
	d.wg.Wait()
}

// EmitCapabilityEvent fires before/after events for a capability invocation.
// The before event is fire-and-forget (goroutine); the after event is synchronous
// so on_result handlers complete before the caller proceeds.
func (d *Dispatcher) EmitCapabilityEvent(phase, extName, capName string, payload map[string]any) {
	topic := extName + ":" + capName + ":" + phase
	event := Event{Topic: topic, Payload: payload}
	if phase == "before" {
		// Fire-and-forget; never blocks.
		go d.bus.Publish(event)
	} else {
		d.bus.Publish(event)
	}
}

// FireSlashCommand manually fires a slash_command event (called by the runtime when
// a Discord slash command arrives that should route through the extension system).
func (d *Dispatcher) FireSlashCommand(name string, data map[string]any) {
	d.bus.Publish(Event{
		Topic:   "slash_command:" + name + ":invoke",
		Payload: map[string]any{"data": data},
	})
}

// FirePercept routes an incoming message to pattern_match triggers.
func (d *Dispatcher) FirePercept(source, typ, content string, extra map[string]any) {
	payload := make(map[string]any, len(extra)+3)
	for k, v := range extra {
		payload[k] = v
	}
	payload["source"] = source
	payload["type"] = typ
	payload["content"] = content
	d.bus.Publish(Event{Topic: "percept:message:received", Payload: payload})
}

// --- on_result handler ---

// OnResultConfig is the parsed on_result block from a behavior or capability definition.
type OnResultConfig struct {
	// Parse pre-processes the workflow result before evaluation.
	// Supported: "json" (extract JSON from the result string).
	Parse string `yaml:"parse,omitempty"`

	// Condition is an expr-lang expression evaluated against the (possibly parsed) result.
	// The handler fires only when the condition is true or when Condition is empty.
	Condition string `yaml:"condition,omitempty"`

	// Action determines what to do when the handler fires.
	// "notify" → talk_to_user, "log" → save_thought, "invoke" → run a workflow.
	Action string `yaml:"action"`

	// Message is the notification message for action=notify. Supports {{var}} templates.
	Message string `yaml:"message,omitempty"`

	// Workflow is the workflow name for action=invoke. Supports {{var}} templates.
	Workflow string `yaml:"workflow,omitempty"`

	// Params are passed to the invoked workflow. Values support {{var}} templates.
	Params map[string]any `yaml:"params,omitempty"`
}

// HandleOnResult applies a list of on_result handlers to the output of a workflow run.
// result is the raw output (may be string, map, or nil).
// vars provides template resolution context.
func (d *Dispatcher) HandleOnResult(ctx context.Context, configs []OnResultConfig, result any, vars map[string]any) {
	for _, cfg := range configs {
		if err := d.applyOnResult(ctx, cfg, result, vars); err != nil {
			log.Printf("[triggers] on_result handler %q failed: %v", cfg.Action, err)
		}
	}
}

func (d *Dispatcher) applyOnResult(ctx context.Context, cfg OnResultConfig, result any, vars map[string]any) error {
	// Step 1: parse the result if requested.
	parsed := result
	if cfg.Parse == "json" {
		str, ok := result.(string)
		if !ok {
			// Not a string — skip this handler (with a warning, not an error).
			log.Printf("[triggers] on_result: parse=json but result is %T, skipping", result)
			return nil
		}
		var extracted any
		if err := json.Unmarshal([]byte(str), &extracted); err != nil {
			log.Printf("[triggers] on_result: parse=json failed to parse result: %v, skipping", err)
			return nil
		}
		parsed = extracted
	}

	// Step 2: build an evaluation environment for expr.
	env := make(map[string]any, len(vars)+2)
	for k, v := range vars {
		env[k] = v
	}
	env["result"] = parsed
	env["output"] = parsed // alias

	// Step 3: evaluate condition (if any).
	if cfg.Condition != "" {
		program, err := expr.Compile(cfg.Condition, expr.Env(env), expr.AsBool())
		if err != nil {
			return fmt.Errorf("on_result: compiling condition %q: %w", cfg.Condition, err)
		}
		out, err := expr.Run(program, env)
		if err != nil {
			return fmt.Errorf("on_result: evaluating condition %q: %w", cfg.Condition, err)
		}
		if !out.(bool) {
			return nil // condition not met — skip
		}
	}

	// Step 4: execute the action.
	switch cfg.Action {
	case "notify":
		if d.talker == nil {
			return fmt.Errorf("on_result: notify action requires TalkToUser to be configured")
		}
		msg, err := renderOnResultTemplate(cfg.Message, env)
		if err != nil {
			return fmt.Errorf("on_result: rendering message: %w", err)
		}
		return d.talker.Notify(msg)

	case "log":
		if d.logger == nil {
			return fmt.Errorf("on_result: log action requires SaveThought to be configured")
		}
		msg, err := renderOnResultTemplate(cfg.Message, env)
		if err != nil {
			return fmt.Errorf("on_result: rendering message: %w", err)
		}
		return d.logger.Log(msg)

	case "invoke":
		if d.runner == nil {
			return fmt.Errorf("on_result: invoke action requires WorkflowRunner to be configured")
		}
		wfName, err := renderOnResultTemplate(cfg.Workflow, env)
		if err != nil {
			return fmt.Errorf("on_result: rendering workflow name: %w", err)
		}
		params := make(map[string]any, len(cfg.Params))
		for k, v := range cfg.Params {
			if str, ok := v.(string); ok {
				rendered, rerr := renderOnResultTemplate(str, env)
				if rerr == nil {
					params[k] = rendered
					continue
				}
			}
			params[k] = v
		}
		_, err = d.runner.RunWorkflow(ctx, wfName, params)
		return err

	default:
		return fmt.Errorf("on_result: unknown action %q", cfg.Action)
	}
}

// renderOnResultTemplate is a thin wrapper around the reflex package's template
// renderer. We reproduce a minimal version here to avoid an import cycle.
// Supports {{var}} and {{namespace.key}} syntax with the given env map.
func renderOnResultTemplate(tmpl string, env map[string]any) (string, error) {
	if !strings.Contains(tmpl, "{{") {
		return tmpl, nil
	}
	re := onResultVarRe
	var firstErr error
	result := re.ReplaceAllStringFunc(tmpl, func(match string) string {
		inner := match[2 : len(match)-2]
		parts := strings.SplitN(inner, ".", 2)
		var val any
		var ok bool
		if len(parts) == 2 {
			if nsMap, nok := env[parts[0]].(map[string]any); nok {
				val, ok = nsMap[parts[1]]
			}
		} else {
			val, ok = env[inner]
		}
		if !ok {
			if firstErr == nil {
				firstErr = fmt.Errorf("undefined variable: %s", inner)
			}
			return ""
		}
		return fmt.Sprintf("%v", val)
	})
	return result, firstErr
}

var onResultVarRe = regexp.MustCompile(`\{\{[^}]+\}\}`)

// --- helpers ---

// parseTriggerType extracts the trigger type key and its value from a raw trigger map.
// Returns ("", nil) for an empty or unrecognised map.
func parseTriggerType(trigger map[string]any) (typ string, val any) {
	// Recognised trigger type keys in preference order.
	for _, k := range []string{"schedule", "slash_command", "event", "condition", "pattern_match", "manual", "webhook"} {
		if v, ok := trigger[k]; ok {
			return k, v
		}
	}
	// Fall back to first key found.
	for k, v := range trigger {
		return k, v
	}
	return "", nil
}

// --- cron scheduler ---

// cronField represents one field of a cron expression.
type cronField struct {
	// values holds the set of allowed values (nil = any).
	values map[int]bool
	step   int // for */N expressions
}

// ParsedCron holds the 5 fields: minute, hour, dom, month, dow.
// Exported for use in tests.
type ParsedCron struct {
	minute []*cronField
	hour   []*cronField
	dom    []*cronField
	month  []*cronField
	dow    []*cronField
}

// CronMatches returns true if all fields of the parsed cron expression match t.
// Exported for use in tests.
func CronMatches(c *ParsedCron, t time.Time) bool {
	return c.matchesFields(t)
}

// ParseCron parses a 5-field cron expression. Exported for use in tests.
func ParseCron(expr string) (*ParsedCron, error) {
	return parseCron(expr)
}

// parseCron parses a 5-field cron expression.
func parseCron(expr string) (*ParsedCron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("expected 5 fields, got %d", len(fields))
	}
	type rangeLimit struct{ min, max int }
	limits := []rangeLimit{
		{0, 59}, // minute
		{0, 23}, // hour
		{1, 31}, // dom
		{1, 12}, // month
		{0, 6},  // dow
	}
	parse := func(s string, lim rangeLimit) ([]*cronField, error) {
		var out []*cronField
		for _, part := range strings.Split(s, ",") {
			f, err := parseCronPart(part, lim.min, lim.max)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
		}
		return out, nil
	}

	c := &ParsedCron{}
	var err error
	c.minute, err = parse(fields[0], limits[0])
	if err != nil {
		return nil, fmt.Errorf("minute field: %w", err)
	}
	c.hour, err = parse(fields[1], limits[1])
	if err != nil {
		return nil, fmt.Errorf("hour field: %w", err)
	}
	c.dom, err = parse(fields[2], limits[2])
	if err != nil {
		return nil, fmt.Errorf("dom field: %w", err)
	}
	c.month, err = parse(fields[3], limits[3])
	if err != nil {
		return nil, fmt.Errorf("month field: %w", err)
	}
	c.dow, err = parse(fields[4], limits[4])
	if err != nil {
		return nil, fmt.Errorf("dow field: %w", err)
	}
	return c, nil
}

func parseCronPart(s string, min, max int) (*cronField, error) {
	// Handle */N
	if strings.HasPrefix(s, "*/") {
		n, err := strconv.Atoi(s[2:])
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid step %q", s)
		}
		return &cronField{step: n}, nil
	}
	// Handle *
	if s == "*" {
		return &cronField{}, nil
	}
	// Handle range a-b
	if idx := strings.Index(s, "-"); idx >= 0 {
		lo, err1 := strconv.Atoi(s[:idx])
		hi, err2 := strconv.Atoi(s[idx+1:])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("invalid range %q", s)
		}
		vals := make(map[int]bool, hi-lo+1)
		for i := lo; i <= hi; i++ {
			vals[i] = true
		}
		return &cronField{values: vals}, nil
	}
	// Handle literal
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("invalid value %q", s)
	}
	if n < min || n > max {
		return nil, fmt.Errorf("value %d out of range [%d, %d]", n, min, max)
	}
	return &cronField{values: map[int]bool{n: true}}, nil
}

// matches returns true if v matches this cron field.
func (f *cronField) matches(v int) bool {
	if f.values == nil && f.step == 0 {
		return true // wildcard *
	}
	if f.step > 0 {
		return v%f.step == 0
	}
	return f.values[v]
}

// matchesFields returns true if all cron fields match the given time.
func (c *ParsedCron) matchesFields(t time.Time) bool {
	matchAny := func(fields []*cronField, v int) bool {
		for _, f := range fields {
			if f.matches(v) {
				return true
			}
		}
		return false
	}
	return matchAny(c.minute, t.Minute()) &&
		matchAny(c.hour, t.Hour()) &&
		matchAny(c.dom, t.Day()) &&
		matchAny(c.month, int(t.Month())) &&
		matchAny(c.dow, int(t.Weekday()))
}

// runSchedule fires action on each minute boundary that matches the cron expression.
// It ticks every 30 seconds aligned to the minute start to avoid double-fires.
func runSchedule(ctx context.Context, sched *ParsedCron, stop <-chan struct{}, action func()) {
	lastFired := time.Time{}

	tick := func() {
		now := time.Now().Truncate(time.Minute)
		if sched.matchesFields(now) && now != lastFired {
			lastFired = now
			action()
		}
	}

	// Align to next 30-second boundary.
	now := time.Now()
	untilNext := time.Duration(30-now.Second()%30) * time.Second
	select {
	case <-time.After(untilNext):
	case <-stop:
		return
	case <-ctx.Done():
		return
	}

	tick()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tick()
		case <-stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

// runCondition evaluates an expr expression on each interval and fires action when true.
func runCondition(ctx context.Context, exprStr string, interval time.Duration, stop <-chan struct{}, action func(vars map[string]any)) {
	// Pre-compile the expression with a generic environment.
	// We use an empty env at compile time; runtime env is provided per-tick.
	// Use AsBool to validate the expression returns a bool.
	program, err := expr.Compile(exprStr)
	if err != nil {
		log.Printf("[triggers] condition: compile error for %q: %v", exprStr, err)
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case t := <-ticker.C:
			env := buildSystemEnv(t)
			out, err := expr.Run(program, env)
			if err != nil {
				log.Printf("[triggers] condition: runtime error for %q: %v", exprStr, err)
				continue
			}
			if b, ok := out.(bool); ok && b {
				action(env)
			}
		case <-stop:
			return
		case <-ctx.Done():
			return
		}
	}
}

// buildSystemEnv returns the system variables available to condition expressions.
func buildSystemEnv(t time.Time) map[string]any {
	return map[string]any{
		"time":    t.Format("15:04"),
		"hour":    t.Hour(),
		"minute":  t.Minute(),
		"weekday": t.Weekday().String(),
	}
}
