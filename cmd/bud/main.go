package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/attention"
	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/effectors"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/senses"
	"github.com/vthunder/bud2/internal/types"
)

const Version = "2026-01-09-v2-session-fix"

// checkPidFile checks for an existing bud process and handles it
// Returns a cleanup function to remove the pid file on exit
func checkPidFile(statePath string) func() {
	pidFile := filepath.Join(statePath, "bud.pid")

	// Check if pid file exists
	if data, err := os.ReadFile(pidFile); err == nil {
		pidStr := strings.TrimSpace(string(data))
		if pid, err := strconv.Atoi(pidStr); err == nil {
			// Check if process is running
			proc, err := process.NewProcess(int32(pid))
			if err == nil {
				running, _ := proc.IsRunning()
				if running {
					// Check if it's actually bud (not a recycled PID)
					name, _ := proc.Name()
					cmdline, _ := proc.Cmdline()
					if strings.Contains(name, "bud") || strings.Contains(cmdline, "bud") {
						// Another bud is running - ask user what to do
						fmt.Printf("\n⚠️  Another bud process is running (PID %d)\n", pid)
						fmt.Printf("   Started: %s\n", getProcessStartTime(proc))
						fmt.Printf("\nOptions:\n")
						fmt.Printf("  [k] Kill it and continue\n")
						fmt.Printf("  [q] Quit (let the other process run)\n")
						fmt.Printf("\nChoice [k/q]: ")

						reader := bufio.NewReader(os.Stdin)
						choice, _ := reader.ReadString('\n')
						choice = strings.TrimSpace(strings.ToLower(choice))

						if choice == "k" {
							log.Printf("[main] Killing existing bud process (PID %d)...", pid)
							proc.Kill()
							time.Sleep(500 * time.Millisecond) // Give it time to die
						} else {
							log.Println("[main] Exiting to let existing process run")
							os.Exit(0)
						}
					}
				}
			}
		}
		// Stale pid file - remove it
		os.Remove(pidFile)
	}

	// Write our PID
	myPid := os.Getpid()
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(myPid)), 0644); err != nil {
		log.Printf("Warning: failed to write pid file: %v", err)
	} else {
		log.Printf("[main] PID file created: %s (pid=%d)", pidFile, myPid)
	}

	// Return cleanup function
	return func() {
		os.Remove(pidFile)
		log.Printf("[main] PID file removed")
	}
}

func getProcessStartTime(proc *process.Process) string {
	createTime, err := proc.CreateTime()
	if err != nil {
		return "unknown"
	}
	return time.UnixMilli(createTime).Format("2006-01-02 15:04:05")
}

func main() {
	log.Printf("bud2 - subsumption-inspired agent [%s]", Version)
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

	// Check for existing bud process (before creating state directory)
	os.MkdirAll(statePath, 0755) // Ensure state dir exists for pid file
	cleanupPidFile := checkPidFile(statePath)
	defer cleanupPidFile()
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

	// Seed notes from defaults if missing
	seedNotes(statePath)

	// Generate .mcp.json with correct state path for MCP server
	if err := writeMCPConfig(statePath); err != nil {
		log.Printf("Warning: failed to write .mcp.json: %v", err)
	}

	// Initialize memory pools
	systemPath := filepath.Join(statePath, "system")
	queuesPath := filepath.Join(systemPath, "queues")
	os.MkdirAll(queuesPath, 0755)
	inbox := memory.NewInbox(filepath.Join(queuesPath, "inbox.jsonl"))
	perceptPool := memory.NewPerceptPool(filepath.Join(queuesPath, "percepts.json"))
	threadPool := memory.NewThreadPool(filepath.Join(systemPath, "threads.json"))
	tracePool := memory.NewTracePool(filepath.Join(systemPath, "traces.json"))
	outbox := memory.NewOutbox(filepath.Join(queuesPath, "outbox.jsonl"))

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
	thinkingBudget := budget.NewThinkingBudget(sessionTracker)
	thinkingBudget.DailyMinutes = dailyBudgetMinutes

	// CPU watcher writes signals to inbox when it detects idle sessions
	// The unified inbox loop will complete the session
	cpuWatcher := budget.NewCPUWatcher(sessionTracker)
	cpuWatcher.SetOnComplete(func(session *budget.Session, summary string) {
		msg := &memory.InboxMessage{
			ID:        fmt.Sprintf("signal-cpu-%d", time.Now().UnixNano()),
			Type:      "signal",
			Subtype:   "done",
			Content:   summary,
			Timestamp: time.Now(),
			Status:    "pending",
			Extra: map[string]any{
				"session_id": session.ID,
				"thread_id":  session.ThreadID,
				"source":     "cpu_watcher",
			},
		}
		inbox.Add(msg)
	})
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
	reflexEngine.SetBudTaskStore(taskStore)

	// Initialize calendar client (optional - only if credentials are configured)
	var calendarClient *calendar.Client
	if os.Getenv("GOOGLE_CALENDAR_CREDENTIALS_FILE") != "" && os.Getenv("GOOGLE_CALENDAR_ID") != "" {
		var err error
		calendarClient, err = calendar.NewClient()
		if err != nil {
			log.Printf("Warning: failed to create calendar client: %v", err)
		} else {
			log.Println("[main] Google Calendar integration enabled")
			reflexEngine.SetCalendarClient(calendarClient)
		}
	} else {
		log.Println("[main] Google Calendar integration disabled (credentials not configured)")
	}

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

	// handleSignal processes a signal-type inbox message (budget tracking, no percept)
	handleSignal := func(msg *memory.InboxMessage) {
		if msg.Subtype == "done" {
			// Session completion signal
			sessionID := ""
			source := "signal_done"
			if msg.Extra != nil {
				sessionID, _ = msg.Extra["session_id"].(string)
				if s, ok := msg.Extra["source"].(string); ok {
					source = s
				}
			}
			if sessionID != "" {
				session := sessionTracker.CompleteSession(sessionID)
				if session != nil {
					activityLog.LogExecDone(msg.Content, session.ThreadID, session.DurationSec, source)
					log.Printf("[main] Signal: session %s completed via %s (%.1f sec)", sessionID, source, session.DurationSec)
				}
			}
		}
		// Other signal subtypes can be added here
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

		// No reflex handled it - check budget before routing to executive
		// (Reflexes already ran above - they're cheap. Executive is expensive.)
		isAutonomous := percept.Source == "impulse" || percept.Source == "system"
		if isAutonomous {
			// Check if this is a high-priority urgent task that bypasses budget
			priority, _ := percept.Data["priority"].(int)
			impulseType, _ := percept.Data["type"].(string)
			isUrgent := priority == 1 && (impulseType == "due" || impulseType == "upcoming")

			if !isUrgent {
				if ok, reason := thinkingBudget.CanDoAutonomousWork(); !ok {
					log.Printf("[main] Autonomous percept blocked from executive: %s", reason)
					return
				}
			} else {
				log.Printf("[main] High-priority urgent task bypassing budget check")
			}
		}

		// Route to attention/executive
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
			discordSense.Session, // getter function, not called value
			func() (int, error) { return outbox.Poll() },
			func() []*types.Action { return outbox.GetPending() },
			func(id string) { outbox.MarkComplete(id) },
			func(id string) { outbox.MarkFailed(id) },
		)
		discordEffector.SetOnSend(captureResponse)
		discordEffector.SetOnAction(func(actionType, channelID, content, source string) {
			activityLog.LogAction(fmt.Sprintf("%s: %s", actionType, truncate(content, 80)), source, channelID, content)
		})
		discordEffector.SetOnError(func(actionID, actionType, errMsg string) {
			activityLog.LogError(
				fmt.Sprintf("Discord %s failed: %s", actionType, truncate(errMsg, 100)),
				fmt.Errorf("%s", errMsg),
				map[string]any{"action_id": actionID, "action_type": actionType},
			)
		})
		discordEffector.SetOnRetry(func(actionID, actionType, errMsg string, attempt int, nextRetry time.Duration) {
			activityLog.Log(activity.Entry{
				Type:    activity.TypeAction,
				Summary: fmt.Sprintf("Discord %s retry (attempt %d, next in %v): %s", actionType, attempt, nextRetry, truncate(errMsg, 80)),
				Data: map[string]any{
					"action_id":   actionID,
					"action_type": actionType,
					"attempt":     attempt,
					"next_retry":  nextRetry.String(),
					"error":       errMsg,
					"retrying":    true,
				},
			})
		})
		discordEffector.Start()

		// Start health monitor for connection resilience
		discordSense.SetOnProlongedOutage(func(duration time.Duration) {
			activityLog.LogError(
				fmt.Sprintf("Discord disconnected for %v, triggering hard reset", duration.Round(time.Second)),
				fmt.Errorf("prolonged disconnection"),
				map[string]any{"duration": duration.String()},
			)
		})
		discordSense.StartHealthMonitor()

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

	// Start calendar sense (optional, independent of Discord)
	var calendarSense *senses.CalendarSense
	if calendarClient != nil {
		calendarSense = senses.NewCalendarSense(senses.CalendarConfig{
			Client: calendarClient,
		}, inbox)

		if err := calendarSense.Start(); err != nil {
			log.Printf("Warning: failed to start calendar sense: %v", err)
		} else {
			log.Println("[main] Calendar sense started")
		}
	}

	// Start inbox polling (unified queue: messages, signals, impulses)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				// Poll inbox file for external writes (synthetic mode, MCP tools)
				newMsgs, err := inbox.Poll()
				if err != nil {
					log.Printf("[main] Inbox poll error: %v", err)
					continue
				}
				if len(newMsgs) > 0 {
					log.Printf("[main] Found %d new items in inbox", len(newMsgs))
				}

				// Process all pending items
				pending := inbox.GetPending()
				for _, msg := range pending {
					inbox.MarkProcessed(msg.ID) // Mark BEFORE processing to prevent race
				}
				for _, msg := range pending {
					switch msg.Type {
					case "signal":
						// Signals don't become percepts - handle directly
						handleSignal(msg)

					case "impulse":
						// Impulses may become percepts
						percept := msg.ToPercept()
						if percept != nil {
							processPercept(percept)
						}

					default: // "message" or empty (backward compat)
						// Messages become percepts
						percept := msg.ToPercept()
						if percept != nil {
							processPercept(percept)
						}
					}
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

				// Write the highest priority impulse to inbox
				// Budget is checked when processing from inbox
				impulse := impulses[0]
				log.Printf("[autonomous] Queueing task impulse: %s", impulse.Description)

				// Convert to inbox message and add to queue
				inboxMsg := memory.NewInboxMessageFromImpulse(impulse)
				inbox.Add(inboxMsg)

				// Auto-complete recurring tasks after queueing
				// The task's job is to remind on a schedule - once queued, it's done for this interval
				if impulse.Type == "recurring" {
					if taskID, ok := impulse.Data["task_id"].(string); ok && taskID != "" {
						taskStore.Complete(taskID)
						if err := taskStore.Save(); err != nil {
							log.Printf("[autonomous] Failed to auto-complete recurring task %s: %v", taskID, err)
						} else {
							log.Printf("[autonomous] Auto-completed recurring task: %s", taskID)
						}
					}
				}
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

					log.Printf("[autonomous] Queueing periodic wake-up impulse")
					inboxMsg := memory.NewInboxMessageFromImpulse(impulse)
					inbox.Add(inboxMsg)
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
		discordSense.StopHealthMonitor()
		discordSense.Stop()
	}
	if calendarSense != nil {
		calendarSense.Stop()
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

// seedNotes copies seed/notes/ to state/notes/ if the directory doesn't exist
func seedNotes(statePath string) {
	notesDir := filepath.Join(statePath, "notes")

	// Only seed if directory doesn't exist
	if _, err := os.Stat(notesDir); !os.IsNotExist(err) {
		return
	}

	// Create directory
	if err := os.MkdirAll(notesDir, 0755); err != nil {
		log.Printf("[main] Warning: failed to create notes dir: %v", err)
		return
	}

	// Copy all files from seed/notes/
	seedDir := "seed/notes"
	entries, err := os.ReadDir(seedDir)
	if err != nil {
		log.Printf("[main] Warning: failed to read seed notes: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(seedDir, entry.Name())
		dst := filepath.Join(notesDir, entry.Name())

		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("[main] Warning: failed to read %s: %v", src, err)
			continue
		}

		if err := os.WriteFile(dst, data, 0644); err != nil {
			log.Printf("[main] Warning: failed to write %s: %v", dst, err)
			continue
		}

		log.Printf("[main] Seeded: %s", entry.Name())
	}
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
