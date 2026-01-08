package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/attention"
	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/effectors"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/senses"
	"github.com/vthunder/bud2/internal/types"
)

func main() {
	log.Println("bud2 - subsumption-inspired agent")
	log.Println("==================================")

	// Load .env file (optional - won't error if missing)
	if err := godotenv.Load(); err != nil {
		log.Println("[config] No .env file found, using environment variables")
	} else {
		log.Println("[config] Loaded .env file")
	}

	// Config from environment
	discordToken := os.Getenv("DISCORD_TOKEN")
	discordChannel := os.Getenv("DISCORD_CHANNEL_ID")
	discordOwner := os.Getenv("DISCORD_OWNER_ID")
	statePath := os.Getenv("STATE_PATH")
	if statePath == "" {
		statePath = "state"
	}
	claudeModel := os.Getenv("CLAUDE_MODEL")
	useInteractive := os.Getenv("EXECUTIVE_INTERACTIVE") == "true"
	syntheticMode := os.Getenv("SYNTHETIC_MODE") == "true"
	autonomousEnabled := os.Getenv("AUTONOMOUS_ENABLED") == "true"
	autonomousIntervalStr := os.Getenv("AUTONOMOUS_INTERVAL")
	dailyBudgetStr := os.Getenv("DAILY_THINKING_BUDGET")

	// Parse autonomous interval (default 2 hours)
	autonomousInterval := 2 * time.Hour
	if autonomousIntervalStr != "" {
		if d, err := time.ParseDuration(autonomousIntervalStr); err == nil {
			autonomousInterval = d
		}
	}

	// Parse daily budget (default 30 minutes)
	dailyBudgetMinutes := 30.0
	if dailyBudgetStr != "" {
		if v, err := strconv.ParseFloat(dailyBudgetStr, 64); err == nil {
			dailyBudgetMinutes = v
		}
	}

	// In synthetic mode, Discord is not required
	if !syntheticMode && discordToken == "" {
		log.Fatal("DISCORD_TOKEN environment variable required (or set SYNTHETIC_MODE=true)")
	}

	// Ensure state directory exists
	os.MkdirAll(statePath, 0755)

	// Generate .mcp.json with correct state path for MCP server
	if err := writeMCPConfig(statePath); err != nil {
		log.Printf("Warning: failed to write .mcp.json: %v", err)
	}

	// Initialize memory pools
	inbox := memory.NewInbox(filepath.Join(statePath, "inbox.jsonl"))
	perceptPool := memory.NewPerceptPool(filepath.Join(statePath, "percepts.json"))
	threadPool := memory.NewThreadPool(filepath.Join(statePath, "threads.json"))
	tracePool := memory.NewTracePool(filepath.Join(statePath, "traces.json"))
	outbox := memory.NewOutbox(filepath.Join(statePath, "outbox.jsonl"))

	// Load persisted state
	if err := inbox.Load(); err != nil {
		log.Printf("Warning: failed to load inbox: %v", err)
	}
	if err := perceptPool.Load(); err != nil {
		log.Printf("Warning: failed to load percepts: %v", err)
	}
	if err := threadPool.Load(); err != nil {
		log.Printf("Warning: failed to load threads: %v", err)
	}
	if err := tracePool.Load(); err != nil {
		log.Printf("Warning: failed to load traces: %v", err)
	}
	if err := outbox.Load(); err != nil {
		log.Printf("Warning: failed to load outbox: %v", err)
	}
	log.Printf("[main] Loaded %d traces from memory", tracePool.Count())

	// Initialize motivation stores (for autonomous impulses)
	taskStore := motivation.NewTaskStore(statePath)
	if err := taskStore.Load(); err != nil {
		log.Printf("Warning: failed to load tasks: %v", err)
	}

	// Initialize GTD store
	gtdStore := gtd.NewGTDStore(statePath)
	if err := gtdStore.Load(); err != nil {
		log.Printf("Warning: failed to load GTD store: %v", err)
	}

	// Initialize activity logger for observability
	activityLog := activity.New(statePath)

	// Initialize session tracker and signal processor for thinking time budget
	sessionTracker := budget.NewSessionTracker(statePath)
	signalProcessor := budget.NewSignalProcessor(statePath, sessionTracker)
	thinkingBudget := budget.NewThinkingBudget(sessionTracker)
	thinkingBudget.DailyMinutes = dailyBudgetMinutes

	// Set up session completion callback for activity logging
	sessionCompleteCallback := func(session *budget.Session, summary string) {
		activityLog.LogExecDone(summary, session.ThreadID, session.DurationSec, "signal_done")
	}

	// Start signal processor (polls signals.jsonl for session completions)
	signalProcessor.SetOnComplete(sessionCompleteCallback)
	signalProcessor.Start(500 * time.Millisecond)

	// Start CPU watcher as fallback for signal_done
	cpuWatcher := budget.NewCPUWatcher(sessionTracker)
	cpuWatcher.SetOnComplete(sessionCompleteCallback)
	cpuWatcher.Start()

	todayUsed := sessionTracker.TodayThinkingMinutes()
	log.Printf("[main] Session tracker initialized (used today: %.1f min, budget: %.0f min, remaining: %.1f min)",
		todayUsed, dailyBudgetMinutes, dailyBudgetMinutes-todayUsed)

	// Initialize attention first (executive needs it for trace retrieval)
	var attn *attention.Attention
	var exec *executive.Executive

	// Create attention with callback that will use exec (set below)
	attn = attention.New(perceptPool, threadPool, tracePool, func(thread *types.Thread) {
		log.Printf("[main] Active thread changed: %s - %s", thread.ID, thread.Goal)
		// Invoke executive to process the thread
		ctx := context.Background()
		if err := exec.ProcessThread(ctx, thread); err != nil {
			log.Printf("[main] Executive error: %v", err)
		}
	})

	// Bootstrap core identity traces from seed file (if no core traces exist)
	seedPath := filepath.Join(statePath, "core_seed.md")
	if err := attn.BootstrapCore(seedPath); err != nil {
		log.Printf("Warning: failed to bootstrap core traces: %v", err)
	}

	// Initialize reflex engine
	reflexEngine := reflex.NewEngine(statePath)
	if err := reflexEngine.Load(); err != nil {
		log.Printf("Warning: failed to load reflexes: %v", err)
	}
	reflexEngine.SetGTDStore(gtdStore)

	// Initialize reflex log for short-term context
	reflexLog := reflex.NewLog(20) // Keep last 20 reflex interactions

	// Set up reflex reply callback (will wire to outbox after effector is created)
	var reflexReplyCallback func(channelID, message string) error
	reflexEngine.SetReplyCallback(func(channelID, message string) error {
		if reflexReplyCallback != nil {
			return reflexReplyCallback(channelID, message)
		}
		return nil
	})

	// Initialize executive with trace retrieval functions
	exec = executive.New(perceptPool, threadPool, outbox, executive.ExecutiveConfig{
		Model:           claudeModel,
		WorkDir:         ".",
		UseInteractive:  useInteractive,
		GetActiveTraces: attn.GetActivatedTraces,
		GetCoreTraces:   attn.GetCoreTraces,
		GetUnsentReflex: func() []executive.ReflexLogEntry {
			entries := reflexLog.GetUnsent()
			result := make([]executive.ReflexLogEntry, len(entries))
			for i, e := range entries {
				result[i] = executive.ReflexLogEntry{
					Query:    e.Query,
					Response: e.Response,
					Intent:   e.Intent,
				}
			}
			return result
		},
		SessionTracker: sessionTracker,
		OnExecWake: func(threadID, context string) {
			activityLog.LogExecWake("Executive processing", threadID, context)
		},
	})
	if err := exec.Start(); err != nil {
		log.Fatalf("Failed to start executive: %v", err)
	}

	// Process percept helper - checks reflexes first, then routes to attention
	processPercept := func(percept *types.Percept) {
		// Extract content for reflex matching
		content := ""
		if c, ok := percept.Data["content"].(string); ok {
			content = c
		}

		// Extract author for activity logging
		author := ""
		if a, ok := percept.Data["author"].(string); ok {
			author = a
		}
		channelID := ""
		if ch, ok := percept.Data["channel_id"].(string); ok {
			channelID = ch
		}

		// Log input event
		inputSummary := content
		if author != "" {
			inputSummary = fmt.Sprintf("%s: %s", author, truncate(content, 100))
		}
		activityLog.LogInput(inputSummary, percept.Source, channelID)

		// Try reflexes first
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		handled, results := reflexEngine.Process(ctx, percept.Source, percept.Type, content, percept.Data)

		if handled && len(results) > 0 {
			result := results[0]
			// Success && !Stopped means reflex actually handled it
			// Stopped means gate fired (e.g., not_gtd) - let executive handle
			if result.Stopped {
				intent, _ := result.Output["intent"].(string)
				log.Printf("[main] Reflex %s passed (gate stopped) - routing to executive", result.ReflexName)
				activityLog.LogReflexPass(
					fmt.Sprintf("Not handled by %s, routing to executive", result.ReflexName),
					intent,
					content,
				)
			} else if result.Success {
				response, _ := result.Output["response"].(string)
				intent, _ := result.Output["intent"].(string)

				// Log to activity log
				activityLog.LogReflex(
					fmt.Sprintf("Handled %s query", intent),
					intent,
					content,
					response,
				)

				// Add to reflex log for short-term context (always)
				reflexLog.Add(content, response, intent, result.ReflexName)

				// Only create traces for mutations (state changes worth remembering)
				if reflex.IsMutation(intent) {
					attn.CreateImmediateTrace(
						fmt.Sprintf("User added via reflex: %s", content),
						"reflex-mutation",
					)
					if response != "" {
						attn.CreateImmediateTrace(
							fmt.Sprintf("Bud: %s", response),
							"reflex-mutation-response",
						)
					}
				}

				// Mark percept as processed by reflex
				percept.RawInput = content
				percept.ProcessedBy = []string{result.ReflexName}
				percept.Tags = append(percept.Tags, "reflex-handled")
				percept.Intensity *= 0.3 // Lower intensity since reflex handled it

				log.Printf("[main] Percept %s handled by reflex %s (intent: %s)", percept.ID, result.ReflexName, intent)

				// Reflex handled it - add to pool for memory but skip executive
				perceptPool.Add(percept)
				return
			}
		}

		// No reflex handled it - route to attention/executive
		perceptPool.Add(percept)
		threads := attn.RoutePercept(percept, func(content string) string {
			return "respond to: " + truncate(content, 50)
		})
		log.Printf("[main] Percept %s routed to %d thread(s)", percept.ID, len(threads))
	}

	// Capture outgoing response helper
	captureResponse := func(channelID, content string) {
		percept := &types.Percept{
			ID:        fmt.Sprintf("bud-response-%d", time.Now().UnixNano()),
			Source:    "bud",
			Type:      "response",
			Intensity: 0.3, // lower intensity for own responses
			Timestamp: time.Now(),
			Tags:      []string{"outgoing"},
			Data: map[string]any{
				"channel_id": channelID,
				"content":    content,
			},
		}
		// Compute embedding for consolidation (but don't route - would cause feedback loop)
		attn.EmbedPercept(percept)
		perceptPool.Add(percept)
		log.Printf("[main] Captured Bud response as percept (for consolidation)")
	}

	// Stop channel for all polling goroutines
	stopChan := make(chan struct{})

	// Discord mode: start sense and effector
	var discordSense *senses.DiscordSense
	var discordEffector *effectors.DiscordEffector

	if !syntheticMode {
		var err error
		discordSense, err = senses.NewDiscordSense(senses.DiscordConfig{
			Token:     discordToken,
			ChannelID: discordChannel,
			OwnerID:   discordOwner,
		}, inbox)
		if err != nil {
			log.Fatalf("Failed to create Discord sense: %v", err)
		}

		if err := discordSense.Start(); err != nil {
			log.Fatalf("Failed to start Discord sense: %v", err)
		}

		discordEffector = effectors.NewDiscordEffector(
			discordSense.Session(),
			func() (int, error) { return outbox.Poll() },
			func() []*types.Action { return outbox.GetPending() },
			func(id string) { outbox.MarkComplete(id) },
		)
		discordEffector.SetOnSend(captureResponse)
		discordEffector.SetOnAction(func(actionType, channelID, content, source string) {
			activityLog.LogAction(fmt.Sprintf("%s: %s", actionType, truncate(content, 80)), source, channelID, content)
		})
		discordEffector.Start()

		// Wire up typing indicator to executive
		exec.SetTypingCallbacks(discordEffector.StartTyping, discordEffector.StopTyping)

		// Wire reflex reply callback to outbox
		reflexReplyCallback = func(channelID, message string) error {
			log.Printf("[reflex] Sending reply to channel %s: %s", channelID, truncate(message, 50))
			action := &types.Action{
				ID:       fmt.Sprintf("reflex-reply-%d", time.Now().UnixNano()),
				Type:     "send_message",
				Effector: "discord",
				Payload: map[string]any{
					"channel_id": channelID,
					"content":    message,
				},
				Status:    "pending",
				Timestamp: time.Now(),
			}
			outbox.Add(action)
			return nil
		}

		log.Println("[main] Discord sense and effector started")
	} else {
		log.Println("[main] SYNTHETIC_MODE enabled - Discord disabled")
		log.Println("[main] Write to inbox.jsonl, read from outbox.jsonl")
	}

	// Start inbox polling (processes messages from both Discord and synthetic sources)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// Poll inbox file for external writes (synthetic mode)
				newMsgs, err := inbox.Poll()
				if err != nil {
					log.Printf("[main] Inbox poll error: %v", err)
					continue
				}
				if len(newMsgs) > 0 {
					log.Printf("[main] Found %d new messages in inbox", len(newMsgs))
				}

				// Process all pending messages
				for _, msg := range inbox.GetPending() {
					percept := msg.ToPercept()
					processPercept(percept)
					inbox.MarkProcessed(msg.ID)
				}
			}
		}
	}()

	// Start attention
	attn.Start()

	// Start consolidation goroutine (memory consolidation during idle)
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				n := attn.Consolidate()
				if n > 0 {
					log.Printf("[main] Consolidated %d percepts into traces (total: %d)", n, attn.TraceCount())
				}
				// Periodic save of traces (prevents loss on crash)
				if err := tracePool.Save(); err != nil {
					log.Printf("[main] Warning: failed to save traces: %v", err)
				}
			}
		}
	}()

	// Start autonomous wake-up goroutine (periodic self-initiated work)
	if autonomousEnabled {
		log.Printf("[main] Autonomous mode enabled (interval: %v)", autonomousInterval)
		go func() {
			// Check for task impulses more frequently (every minute)
			taskCheckInterval := 1 * time.Minute
			taskTicker := time.NewTicker(taskCheckInterval)
			defer taskTicker.Stop()

			// Periodic wake-up on longer interval
			periodicTicker := time.NewTicker(autonomousInterval)
			defer periodicTicker.Stop()

			// Wait a bit before first check
			time.Sleep(10 * time.Second)

			// Do initial task check
			checkTaskImpulses := func() {
				// Reload tasks in case they changed
				taskStore.Load()
				impulses := taskStore.GenerateImpulses()
				if len(impulses) == 0 {
					return
				}

				// Check budget before triggering
				if ok, reason := thinkingBudget.CanDoAutonomousWork(); !ok {
					log.Printf("[autonomous] Task impulse blocked: %s", reason)
					return
				}

				// Process the highest priority impulse
				impulse := impulses[0]
				log.Printf("[autonomous] Triggering wake-up via task impulse: %s", impulse.Description)
				thinkingBudget.LogStatus()

				// Convert to percept (Bud knows owner's default channel from core memory)
				processPercept(impulse.ToPercept())
			}

			// Check immediately on startup
			checkTaskImpulses()

			for {
				select {
				case <-stopChan:
					return
				case <-taskTicker.C:
					// Check for due/upcoming tasks
					checkTaskImpulses()

				case <-periodicTicker.C:
					// Periodic wake-up (even without specific tasks)
					if ok, reason := thinkingBudget.CanDoAutonomousWork(); !ok {
						log.Printf("[autonomous] Skipping periodic wake-up: %s", reason)
						continue
					}

					thinkingBudget.LogStatus()

					impulse := &types.Impulse{
						ID:          fmt.Sprintf("impulse-wake-%d", time.Now().UnixNano()),
						Source:      types.ImpulseSystem,
						Type:        "wake",
						Intensity:   0.5,
						Timestamp:   time.Now(),
						Description: "Periodic autonomous wake-up. Check for pending tasks, review commitments, or do background work.",
						Data: map[string]any{
							"trigger": "periodic",
						},
					}

					log.Printf("[autonomous] Triggering periodic wake-up")
					processPercept(impulse.ToPercept())
				}
			}
		}()
	} else {
		log.Println("[main] Autonomous mode disabled (set AUTONOMOUS_ENABLED=true to enable)")
	}

	log.Println("[main] All subsystems started. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("[main] Shutting down...")

	// Stop subsystems
	close(stopChan)
	cpuWatcher.Stop()
	attn.Stop()
	if discordEffector != nil {
		discordEffector.StopAllTyping()
		discordEffector.Stop()
	}
	if discordSense != nil {
		discordSense.Stop()
	}

	// Final consolidation before shutdown (consolidate ALL percepts regardless of age)
	n := attn.ConsolidateAll()
	if n > 0 {
		log.Printf("[main] Final consolidation: %d percepts", n)
	}

	// Persist state
	if err := inbox.Save(); err != nil {
		log.Printf("Warning: failed to save inbox: %v", err)
	}
	if err := perceptPool.Save(); err != nil {
		log.Printf("Warning: failed to save percepts: %v", err)
	}
	if err := threadPool.Save(); err != nil {
		log.Printf("Warning: failed to save threads: %v", err)
	}
	if err := tracePool.Save(); err != nil {
		log.Printf("Warning: failed to save traces: %v", err)
	}
	if err := outbox.Save(); err != nil {
		log.Printf("Warning: failed to save outbox: %v", err)
	}
	log.Printf("[main] Saved %d traces to memory", tracePool.Count())

	log.Println("[main] Goodbye!")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// writeMCPConfig generates .mcp.json with the correct state path
func writeMCPConfig(statePath string) error {
	// Get absolute path for state
	absStatePath, err := filepath.Abs(statePath)
	if err != nil {
		return err
	}

	// Get the path to bud-mcp binary (same directory as bud)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	budMCPPath := filepath.Join(filepath.Dir(exe), "bud-mcp")

	// Build config
	config := map[string]any{
		"mcpServers": map[string]any{
			"bud2": map[string]any{
				"type":    "stdio",
				"command": budMCPPath,
				"args":    []string{},
				"env": map[string]string{
					"BUD_STATE_PATH": absStatePath,
				},
			},
		},
	}

	// Write to .mcp.json in current working directory
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(".mcp.json", data, 0644); err != nil {
		return err
	}

	log.Printf("[main] Generated .mcp.json with BUD_STATE_PATH=%s", absStatePath)
	return nil
}
