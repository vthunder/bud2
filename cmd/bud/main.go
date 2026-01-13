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
	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/buffer"
	"github.com/vthunder/bud2/internal/effectors"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/focus"
	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/senses"
	"github.com/vthunder/bud2/internal/types"
)

const Version = "2026-01-13-v2-focus-cutover"

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
	log.Printf("bud2 - focus-based agent v2 [%s]", Version)
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
	userTimezoneStr := os.Getenv("USER_TIMEZONE") // e.g., "Europe/Berlin"

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

	// Parse user timezone (default UTC)
	var userTimezone *time.Location
	if userTimezoneStr != "" {
		var err error
		userTimezone, err = time.LoadLocation(userTimezoneStr)
		if err != nil {
			log.Printf("Warning: invalid USER_TIMEZONE %q, using UTC: %v", userTimezoneStr, err)
			userTimezone = time.UTC
		} else {
			log.Printf("[config] User timezone: %s", userTimezone)
		}
	}

	// In synthetic mode, Discord is not required
	if !syntheticMode && discordToken == "" {
		log.Fatal("DISCORD_TOKEN environment variable required (or set SYNTHETIC_MODE=true)")
	}

	// Ensure state directory exists
	os.MkdirAll(statePath, 0755)

	// Seed guides (how-to docs) from defaults if missing
	seedGuides(statePath)

	// Generate .mcp.json with correct state path for MCP server
	if err := writeMCPConfig(statePath); err != nil {
		log.Printf("Warning: failed to write .mcp.json: %v", err)
	}

	// Initialize paths
	systemPath := filepath.Join(statePath, "system")
	queuesPath := filepath.Join(systemPath, "queues")
	os.MkdirAll(queuesPath, 0755)

	// Initialize v2 memory systems
	// Graph DB (SQLite) - replaces tracePool
	graphDB, err := graph.Open(systemPath)
	if err != nil {
		log.Fatalf("Failed to initialize graph database: %v", err)
	}
	defer graphDB.Close()
	log.Println("[main] Graph database initialized")

	// Conversation buffer - new in v2
	conversationBuffer := buffer.New(systemPath, nil) // No summarizer for now
	if err := conversationBuffer.Load(); err != nil {
		log.Printf("Warning: failed to load conversation buffer: %v", err)
	}
	log.Println("[main] Conversation buffer initialized")

	// Keep inbox/outbox for message queue (still useful)
	inbox := memory.NewInbox(filepath.Join(queuesPath, "inbox.jsonl"))
	outbox := memory.NewOutbox(filepath.Join(queuesPath, "outbox.jsonl"))

	// Load persisted state
	if err := inbox.Load(); err != nil {
		log.Printf("Warning: failed to load inbox: %v", err)
	}
	if err := outbox.Load(); err != nil {
		log.Printf("Warning: failed to load outbox: %v", err)
	}

	// Bootstrap core identity traces from seed file
	seedPath := "seed/core_seed.md"
	if err := bootstrapCoreTraces(graphDB, seedPath); err != nil {
		log.Printf("Warning: failed to bootstrap core traces: %v", err)
	}

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
				"source":     "cpu_watcher",
			},
		}
		inbox.Add(msg)
	})
	cpuWatcher.Start()

	todayUsed := sessionTracker.TodayThinkingMinutes()
	log.Printf("[main] Session tracker initialized (used today: %.1f min, budget: %.0f min, remaining: %.1f min)",
		todayUsed, dailyBudgetMinutes, dailyBudgetMinutes-todayUsed)

	// Initialize reflex engine
	reflexEngine := reflex.NewEngine(statePath)
	if err := reflexEngine.Load(); err != nil {
		log.Printf("Warning: failed to load reflexes: %v", err)
	}
	reflexEngine.SetGTDStore(gtdStore)
	reflexEngine.SetBudTaskStore(taskStore)
	reflexEngine.SetDefaultChannel(discordChannel)

	// Initialize calendar client (optional)
	var calendarClient *calendar.Client
	hasCalendarCreds := os.Getenv("GOOGLE_CALENDAR_CREDENTIALS") != "" || os.Getenv("GOOGLE_CALENDAR_CREDENTIALS_FILE") != ""
	hasCalendarIDs := os.Getenv("GOOGLE_CALENDAR_IDS") != "" || os.Getenv("GOOGLE_CALENDAR_ID") != ""
	if hasCalendarCreds && hasCalendarIDs {
		var err error
		calendarClient, err = calendar.NewClient()
		if err != nil {
			log.Printf("Warning: failed to create calendar client: %v", err)
		} else {
			log.Printf("[main] Google Calendar integration enabled (%d calendars)", len(calendarClient.CalendarIDs()))
			reflexEngine.SetCalendarClient(calendarClient)
		}
	} else {
		log.Println("[main] Google Calendar integration disabled (credentials not configured)")
	}

	// Initialize reflex log for short-term context
	reflexLog := reflex.NewLog(20)

	// Set up reflex reply callback (will wire to outbox after effector is created)
	var reflexReplyCallback func(channelID, message string) error
	reflexEngine.SetReplyCallback(func(channelID, message string) error {
		if reflexReplyCallback != nil {
			return reflexReplyCallback(channelID, message)
		}
		return nil
	})

	// Initialize v2 executive with focus-based attention
	exec := executive.NewExecutiveV2(
		graphDB,
		conversationBuffer,
		reflexLog,
		systemPath,
		executive.ExecutiveV2Config{
			Model:          claudeModel,
			WorkDir:        ".",
			UseInteractive: useInteractive,
			BotAuthor:      "Bud", // Filter out bot's own responses on incremental buffer sync
			SessionTracker: sessionTracker,
			OnExecWake: func(focusID, context string) {
				activityLog.LogExecWake("Executive processing", focusID, context)
			},
			OnExecDone: func(focusID, summary string, durationSec float64) {
				activityLog.LogExecDone(summary, focusID, durationSec, "executive")
			},
		},
	)
	if err := exec.Start(); err != nil {
		log.Fatalf("Failed to start executive: %v", err)
	}

	// Wire reflex engine to attention system for proactive mode
	reflexEngine.SetAttention(exec.GetAttention())

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
					activityLog.LogExecDone(msg.Content, "", session.DurationSec, source)
					log.Printf("[main] Signal: session %s completed via %s (%.1f sec)", sessionID, source, session.DurationSec)
				}
			}
		}
	}

	// Process percept helper - checks reflexes first, then routes to focus queue
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

		// Add to conversation buffer
		if channelID != "" && content != "" {
			bufferEntry := buffer.Entry{
				ID:        percept.ID,
				Author:    author,
				Content:   content,
				Timestamp: percept.Timestamp,
				ChannelID: channelID,
				ReplyTo:   percept.ReplyTo,
			}
			if err := conversationBuffer.Add(bufferEntry); err != nil {
				log.Printf("Warning: failed to add to buffer: %v", err)
			}
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

				// Add to reflex log for short-term context
				reflexLog.Add(content, response, intent, result.ReflexName)

				log.Printf("[main] Percept %s handled by reflex %s (intent: %s)", percept.ID, result.ReflexName, intent)
				return // Reflex handled it
			}
		}

		// No reflex handled it - check budget before routing to executive
		isAutonomous := percept.Source == "impulse" || percept.Source == "system"
		if isAutonomous {
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

		// Route to focus queue for executive processing
		item := &focus.PendingItem{
			ID:        percept.ID,
			Type:      percept.Type,
			Priority:  focus.P1UserInput, // User messages are high priority
			Source:    percept.Source,
			Content:   content,
			ChannelID: channelID,
			AuthorID:  author,
			Timestamp: percept.Timestamp,
			Data:      percept.Data,
		}

		// Adjust priority based on source
		if percept.Source == "impulse" || percept.Source == "system" {
			item.Priority = focus.P3ActiveWork
		}

		if err := exec.AddPending(item); err != nil {
			log.Printf("[main] Failed to add to focus queue: %v", err)
			return
		}

		log.Printf("[main] Percept %s added to focus queue (priority: %s)", percept.ID, item.Priority)

		// Process immediately if interactive mode
		if useInteractive {
			go func() {
				ctx := context.Background()
				if _, err := exec.ProcessNext(ctx); err != nil {
					log.Printf("[main] Executive error: %v", err)
				}
			}()
		}
	}

	// Capture outgoing response helper
	captureResponse := func(channelID, content string) {
		// Add Bud's response to conversation buffer
		bufferEntry := buffer.Entry{
			ID:        fmt.Sprintf("bud-response-%d", time.Now().UnixNano()),
			Author:    "Bud",
			Content:   content,
			Timestamp: time.Now(),
			ChannelID: channelID,
		}
		if err := conversationBuffer.Add(bufferEntry); err != nil {
			log.Printf("Warning: failed to add response to buffer: %v", err)
		}
		log.Printf("[main] Captured Bud response to conversation buffer")
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
			discordSense.Session,
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
			Client:   calendarClient,
			Timezone: userTimezone,
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
						handleSignal(msg)

					case "impulse":
						percept := msg.ToPercept()
						if percept != nil {
							processPercept(percept)
						}

					default: // "message" or empty (backward compat)
						percept := msg.ToPercept()
						if percept != nil {
							processPercept(percept)
						}
					}
				}
			}
		}
	}()

	// Start focus queue processing (if not in interactive mode)
	if !useInteractive {
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-stopChan:
					return
				case <-ticker.C:
					ctx := context.Background()
					processed, err := exec.ProcessNext(ctx)
					if err != nil {
						log.Printf("[main] Executive error: %v", err)
					}
					if processed {
						log.Printf("[main] Processed item from focus queue")
					}
				}
			}
		}()
	}

	// Start autonomous wake-up goroutine (periodic self-initiated work)
	if autonomousEnabled {
		log.Printf("[main] Autonomous mode enabled (interval: %v)", autonomousInterval)
		go func() {
			taskCheckInterval := 1 * time.Minute
			taskTicker := time.NewTicker(taskCheckInterval)
			defer taskTicker.Stop()

			periodicTicker := time.NewTicker(autonomousInterval)
			defer periodicTicker.Stop()

			time.Sleep(10 * time.Second)

			checkTaskImpulses := func() {
				taskStore.Load()
				impulses := taskStore.GenerateImpulses()
				if len(impulses) == 0 {
					return
				}

				impulse := impulses[0]
				log.Printf("[autonomous] Queueing task impulse: %s", impulse.Description)

				inboxMsg := memory.NewInboxMessageFromImpulse(impulse)
				inbox.Add(inboxMsg)

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

			checkTaskImpulses()

			for {
				select {
				case <-stopChan:
					return
				case <-taskTicker.C:
					checkTaskImpulses()

				case <-periodicTicker.C:
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
	exec.Stop()
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

	// Persist state
	if err := inbox.Save(); err != nil {
		log.Printf("Warning: failed to save inbox: %v", err)
	}
	if err := conversationBuffer.Save(); err != nil {
		log.Printf("Warning: failed to save conversation buffer: %v", err)
	}
	if err := outbox.Save(); err != nil {
		log.Printf("Warning: failed to save outbox: %v", err)
	}
	if err := exec.GetQueue().Save(); err != nil {
		log.Printf("Warning: failed to save focus queue: %v", err)
	}

	log.Println("[main] Goodbye!")
}

// bootstrapCoreTraces loads core identity traces from seed file if not already present
func bootstrapCoreTraces(db *graph.DB, seedPath string) error {
	// Check if core traces already exist
	existing, err := db.GetCoreTraces()
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		log.Printf("[main] Found %d existing core traces", len(existing))
		return nil
	}

	// Read seed file
	data, err := os.ReadFile(seedPath)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("[main] No core seed file found at %s", seedPath)
			return nil
		}
		return err
	}

	// Parse seed file - sections separated by "---"
	// Each section has a "# Header" followed by content
	sections := strings.Split(string(data), "\n---\n")
	count := 0
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}

		// Extract topic from header line (# Topic Name)
		lines := strings.SplitN(section, "\n", 2)
		topic := "identity"
		content := section

		if len(lines) >= 1 && strings.HasPrefix(lines[0], "# ") {
			topic = strings.TrimPrefix(lines[0], "# ")
			if len(lines) >= 2 {
				content = strings.TrimSpace(lines[1])
			}
		}

		if content == "" {
			continue
		}

		trace := &graph.Trace{
			ID:        fmt.Sprintf("core-%d", time.Now().UnixNano()+int64(count)),
			Summary:   content,
			Topic:     topic,
			IsCore:    true,
			Strength:  100,
			CreatedAt: time.Now(),
		}
		if err := db.AddTrace(trace); err != nil {
			log.Printf("Warning: failed to add core trace: %v", err)
			continue
		}
		count++
	}

	if count > 0 {
		log.Printf("[main] Bootstrapped %d core identity traces from %s", count, seedPath)
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// seedGuides copies seed/guides/ to state/system/guides/ if the directory doesn't exist
// These are "how to" docs that teach Bud how to use various capabilities
func seedGuides(statePath string) {
	guidesDir := filepath.Join(statePath, "system", "guides")

	if _, err := os.Stat(guidesDir); !os.IsNotExist(err) {
		return
	}

	if err := os.MkdirAll(guidesDir, 0755); err != nil {
		log.Printf("[main] Warning: failed to create guides dir: %v", err)
		return
	}

	seedDir := "seed/guides"
	entries, err := os.ReadDir(seedDir)
	if err != nil {
		log.Printf("[main] Warning: failed to read seed guides: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		src := filepath.Join(seedDir, entry.Name())
		dst := filepath.Join(guidesDir, entry.Name())

		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("[main] Warning: failed to read %s: %v", src, err)
			continue
		}

		if err := os.WriteFile(dst, data, 0644); err != nil {
			log.Printf("[main] Warning: failed to write %s: %v", dst, err)
			continue
		}

		log.Printf("[main] Seeded guide: %s", entry.Name())
	}
}

// writeMCPConfig generates .mcp.json with the correct state path
func writeMCPConfig(statePath string) error {
	absStatePath, err := filepath.Abs(statePath)
	if err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	budMCPPath := filepath.Join(filepath.Dir(exe), "bud-mcp")

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
