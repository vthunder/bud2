package main

import (
	"bytes"
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

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/buffer"
	"github.com/vthunder/bud2/internal/consolidate"
	"github.com/vthunder/bud2/internal/effectors"
	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/extract"
	"github.com/vthunder/bud2/internal/filter"
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
						// Check if we're running interactively (TTY on stdin)
						fi, _ := os.Stdin.Stat()
						isInteractive := (fi.Mode() & os.ModeCharDevice) != 0

						if isInteractive {
							// Interactive: ask user what to do
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
						} else {
							// Non-interactive (launchd/service): auto-kill old process
							log.Printf("[main] Non-interactive mode: killing existing bud process (PID %d)...", pid)
							proc.Kill()
							time.Sleep(1 * time.Second) // Give it time to die
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
	discordGuildID := os.Getenv("DISCORD_GUILD_ID") // For slash command registration
	statePath := os.Getenv("STATE_PATH")
	if statePath == "" {
		statePath = "state"
	}
	claudeModel := os.Getenv("CLAUDE_MODEL")
	syntheticMode := os.Getenv("SYNTHETIC_MODE") == "true"

	// Check for existing bud process (before creating state directory)
	os.MkdirAll(statePath, 0755) // Ensure state dir exists for pid file
	cleanupPidFile := checkPidFile(statePath)
	defer cleanupPidFile()
	autonomousEnabled := os.Getenv("AUTONOMOUS_ENABLED") == "true"
	autonomousIntervalStr := os.Getenv("AUTONOMOUS_INTERVAL")
	dailyTokenBudgetStr := os.Getenv("DAILY_OUTPUT_TOKEN_BUDGET")
	userTimezoneStr := os.Getenv("USER_TIMEZONE") // e.g., "Europe/Berlin"

	// Parse autonomous interval (default 2 hours)
	autonomousInterval := 2 * time.Hour
	if autonomousIntervalStr != "" {
		if d, err := time.ParseDuration(autonomousIntervalStr); err == nil {
			autonomousInterval = d
		}
	}

	// Parse daily output token budget (default 1M tokens)
	dailyOutputTokenBudget := 1_000_000
	if dailyTokenBudgetStr != "" {
		if v, err := strconv.Atoi(dailyTokenBudgetStr); err == nil {
			dailyOutputTokenBudget = v
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

	// Entity extractor - using Ollama with qwen2.5:7b for NER
	ollamaClient := embedding.NewClient("", "") // defaults: localhost:11434, nomic-embed-text
	ollamaClient.SetGenerationModel("qwen2.5:7b")
	entityExtractor := extract.NewDeepExtractor(ollamaClient)
	invalidator := extract.NewInvalidator(ollamaClient)
	entityResolver := extract.NewResolver(graphDB, ollamaClient)
	log.Println("[main] Entity extractor initialized (Ollama qwen2.5:7b)")

	// Entropy filter - scores message quality for memory decisions
	entropyFilter := filter.NewEntropyFilter(ollamaClient)
	log.Println("[main] Entropy filter initialized")

	// Memory consolidator - groups related episodes into traces
	memoryConsolidator := consolidate.NewConsolidator(graphDB, ollamaClient)
	log.Println("[main] Memory consolidator initialized")

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

	// Load wakeup instructions for autonomous wake prompts
	wakeupInstructions := ""
	if data, err := os.ReadFile("seed/wakeup.md"); err == nil {
		wakeupInstructions = string(data)
		log.Printf("[main] Loaded wakeup instructions (%d bytes)", len(data))
	} else {
		log.Printf("[main] No wakeup.md found (autonomous wakes will use default prompt)")
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
	thinkingBudget.DailyOutputTokens = dailyOutputTokenBudget

	todayUsage := sessionTracker.TodayTokenUsage()
	log.Printf("[main] Session tracker initialized (output tokens today: %dk, budget: %dk, sessions: %d)",
		todayUsage.OutputTokens/1000, dailyOutputTokenBudget/1000, todayUsage.SessionCount)

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
		ollamaClient, // for query-based memory retrieval
		systemPath,
		executive.ExecutiveV2Config{
			Model:     claudeModel,
			WorkDir:   ".",
			BotAuthor: "Bud", // Filter out bot's own responses on incremental buffer sync
			SessionTracker:     sessionTracker,
			WakeupInstructions: wakeupInstructions,
			OnExecWake: func(focusID, context string) {
				activityLog.LogExecWake("Executive processing", focusID, context)
			},
			OnExecDone: func(focusID, summary string, durationSec float64, usage *executive.SessionUsage) {
				extra := map[string]any{}
				if usage != nil {
					extra["usage"] = usage
				}
				activityLog.LogExecDone(summary, focusID, durationSec, "executive", extra)
			},
			OnMemoryEval: func(eval string) {
				activityLog.Log(activity.Entry{
					Type:    "memory_eval",
					Summary: "Memory self-evaluation",
					Data: map[string]any{
						"eval": eval,
					},
				})
			},
		},
	)
	if err := exec.Start(); err != nil {
		log.Fatalf("Failed to start executive: %v", err)
	}

	// Wire reflex engine to attention system for proactive mode
	reflexEngine.SetAttention(exec.GetAttention())

	// handleSignal processes a signal-type inbox message (budget tracking, session control)
	handleSignal := func(msg *memory.InboxMessage) {
		switch msg.Subtype {
		case "done":
			// Session completion signal
			sessionID := ""
			source := "signal_done"
			var memoryEval map[string]any
			if msg.Extra != nil {
				sessionID, _ = msg.Extra["session_id"].(string)
				if s, ok := msg.Extra["source"].(string); ok {
					source = s
				}
				if eval, ok := msg.Extra["memory_eval"].(map[string]any); ok {
					memoryEval = eval
				}
			}
			if sessionID != "" {
				session := sessionTracker.CompleteSession(sessionID)
				if session != nil {
					var extraData map[string]any
					if memoryEval != nil {
						extraData = map[string]any{"memory_eval": memoryEval}
					}
					activityLog.LogExecDone(msg.Content, "", session.DurationSec, source, extraData)
					log.Printf("[main] Signal: session %s completed via %s (%.1f sec)", sessionID, source, session.DurationSec)
				}
			}
			// Log memory eval separately for easier querying
			if memoryEval != nil {
				activityLog.Log(activity.Entry{
					Type:    "memory_eval",
					Summary: "Memory self-evaluation via signal_done",
					Data:    map[string]any{"eval": memoryEval},
				})
			}

		case "reset_session":
			// Memory reset requested - generate new session ID so old context is not loaded
			log.Printf("[main] Signal: reset_session - generating new session ID")
			exec.ResetSession()
		}
	}

	// Track last episode per channel for FOLLOWS edges
	lastEpisodeByChannel := make(map[string]string) // channel → episode ID

	// Ingest message as episode and extract entities (Tier 1 + 2 of memory graph)
	ingestToMemoryGraph := func(msg *memory.InboxMessage) {
		if msg == nil || msg.Content == "" {
			return
		}

		// Create episode (Tier 1)
		episode := &graph.Episode{
			ID:                msg.ID,
			Content:           msg.Content,
			Source:            "discord",
			Author:            msg.Author,
			AuthorID:          msg.AuthorID,
			Channel:           msg.ChannelID,
			TimestampEvent:    msg.Timestamp,
			TimestampIngested: time.Now(),
			CreatedAt:         time.Now(),
		}

		// Extract v2 metadata from Extra
		if msg.Extra != nil {
			if dialogueAct, ok := msg.Extra["dialogue_act"].(string); ok {
				episode.DialogueAct = dialogueAct
			}
			if replyTo, ok := msg.Extra["reply_to"].(string); ok {
				episode.ReplyTo = replyTo
			}
		}

		// Score content entropy for quality filtering
		shouldExtract, entropyScore, _ := entropyFilter.ShouldCreateEpisode(msg.Content)
		episode.EntropyScore = entropyScore

		if err := graphDB.AddEpisode(episode); err != nil {
			log.Printf("[ingest] Failed to store episode: %v", err)
			return
		}

		// Create FOLLOWS edge from previous episode in same channel
		if prevID, ok := lastEpisodeByChannel[msg.ChannelID]; ok {
			if err := graphDB.AddEpisodeEdge(prevID, episode.ID, graph.EdgeFollows, 1.0); err != nil {
				log.Printf("[ingest] Failed to add FOLLOWS edge: %v", err)
			}
		}
		lastEpisodeByChannel[msg.ChannelID] = episode.ID

		// Skip entity extraction for low-entropy content (saves ~6s Ollama call)
		if !shouldExtract {
			log.Printf("[ingest] Skipped entity extraction for low-entropy content (score=%.2f): %s",
				entropyScore, truncate(msg.Content, 60))
			return
		}

		// Extract entities and relationships (Tier 2)
		result, err := entityExtractor.ExtractAll(msg.Content)
		if err != nil {
			log.Printf("[ingest] Entity extraction failed: %v", err)
			result = &extract.ExtractionResult{} // empty result
		}

		// Resolve and store entities using fuzzy matching
		entityIDMap := make(map[string]string) // name -> entityID for relationship linking
		for _, ext := range result.Entities {
			// Use resolver for fuzzy matching and deduplication
			resolveResult, err := entityResolver.Resolve(ext, extract.DefaultResolveConfig())
			if err != nil {
				log.Printf("[ingest] Failed to resolve entity %s: %v", ext.Name, err)
				continue
			}
			if resolveResult == nil || resolveResult.Entity == nil {
				log.Printf("[ingest] Failed to resolve entity %s: nil result", ext.Name)
				continue
			}

			entity := resolveResult.Entity
			entityIDMap[strings.ToLower(ext.Name)] = entity.ID

			// Log resolution method for debugging
			if resolveResult.MatchedBy == "embedding" {
				log.Printf("[ingest] Merged '%s' with existing entity '%s' via embedding similarity",
					ext.Name, entity.Name)
			}

			// Link episode to entity
			if err := graphDB.LinkEpisodeToEntity(msg.ID, entity.ID); err != nil {
				log.Printf("[ingest] Failed to link episode to entity: %v", err)
			}
		}

		// Store relationships with temporal invalidation detection
		for _, rel := range result.Relationships {
			subjectID := entityIDMap[strings.ToLower(rel.Subject)]
			objectID := entityIDMap[strings.ToLower(rel.Object)]

			// Handle "speaker" reference - map to owner entity
			speakerTerms := map[string]bool{"speaker": true, "user": true, "i": true, "me": true}
			if speakerTerms[strings.ToLower(rel.Subject)] {
				subjectID = "entity-PERSON-owner"
				// Ensure owner entity exists
				graphDB.AddEntity(&graph.Entity{
					ID:   "entity-PERSON-owner",
					Name: "Owner",
					Type: graph.EntityPerson,
				})
			}
			if speakerTerms[strings.ToLower(rel.Object)] {
				objectID = "entity-PERSON-owner"
				// Ensure owner entity exists
				graphDB.AddEntity(&graph.Entity{
					ID:   "entity-PERSON-owner",
					Name: "Owner",
					Type: graph.EntityPerson,
				})
			}

			// Skip if we still don't have both entities
			if subjectID == "" || objectID == "" {
				continue
			}

			edgeType := extract.PredicateToEdgeType(rel.Predicate)

			// Check for invalidation candidates (existing relations that might be contradicted)
			candidates, err := graphDB.FindInvalidationCandidates(subjectID, edgeType)
			if err != nil {
				log.Printf("[ingest] Failed to find invalidation candidates: %v", err)
			}

			// Add the new relationship first to get its ID
			newRelID, err := graphDB.AddEntityRelationWithSource(subjectID, objectID, edgeType, rel.Confidence, msg.ID)
			if err != nil {
				log.Printf("[ingest] Failed to store relationship %s->%s: %v", rel.Subject, rel.Object, err)
				continue
			}

			// If there are candidates and this is an exclusive relation type, check for contradictions
			if len(candidates) > 0 && extract.IsExclusiveRelation(edgeType) {
				// Build entity name map for the invalidation prompt
				entityNames := make(map[string]string)
				for name, id := range entityIDMap {
					entityNames[id] = name
				}

				invResult, err := invalidator.CheckInvalidation(
					rel.Subject, rel.Predicate, rel.Object,
					candidates, entityNames,
				)
				if err != nil {
					log.Printf("[ingest] Invalidation check failed: %v", err)
				} else if len(invResult.InvalidatedIDs) > 0 {
					for _, oldID := range invResult.InvalidatedIDs {
						if err := graphDB.InvalidateRelation(oldID, newRelID); err != nil {
							log.Printf("[ingest] Failed to invalidate relation %d: %v", oldID, err)
						} else {
							log.Printf("[ingest] Invalidated relation %d (reason: %s)", oldID, invResult.Reason)
						}
					}
				}
			}
		}

		if len(result.Entities) > 0 || len(result.Relationships) > 0 {
			names := make([]string, len(result.Entities))
			for i, e := range result.Entities {
				names[i] = e.Name
			}
			log.Printf("[ingest] Stored episode %s with %d entities, %d relationships: %v",
				msg.ID, len(result.Entities), len(result.Relationships), names)
		} else {
			log.Printf("[ingest] Stored episode %s (no entities/relationships extracted)", msg.ID)
		}

		// Traces (Tier 3) are created by consolidation, not on ingest.
		// Episodes stay unconsolidated until memory_flush or periodic consolidation runs.
		// This allows grouping related messages into single meaningful traces.
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

		// Check for reflex config updates (hot reload)
		if reloaded := reflexEngine.CheckForUpdates(); reloaded > 0 {
			log.Printf("[main] Hot-reloaded %d reflex config(s)", reloaded)
		}

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
		// Determine priority based on source and type
		priority := focus.P3ActiveWork // default for autonomous items
		switch percept.Source {
		case "discord", "inbox":
			// User messages are high priority
			priority = focus.P1UserInput
		case "impulse":
			// Check if it's a due/upcoming task (higher priority)
			impulseType, _ := percept.Data["impulse_type"].(string)
			if impulseType == "due" || impulseType == "upcoming" {
				priority = focus.P2DueTask
			} else {
				priority = focus.P3ActiveWork
			}
		case "bud":
			// Bud's own thoughts - lower priority than user input
			priority = focus.P3ActiveWork
		case "system":
			priority = focus.P3ActiveWork
		}

		item := &focus.PendingItem{
			ID:        percept.ID,
			Type:      percept.Type,
			Priority:  priority,
			Source:    percept.Source,
			Content:   content,
			ChannelID: channelID,
			AuthorID:  author,
			Timestamp: percept.Timestamp,
			Data:      percept.Data,
		}

		if err := exec.AddPending(item); err != nil {
			log.Printf("[main] Failed to add to focus queue: %v", err)
			return
		}

		log.Printf("[main] Percept %s added to focus queue (priority: %s)", percept.ID, item.Priority)
	}

	// Capture outgoing response helper
	captureResponse := func(channelID, content string) {
		now := time.Now()
		responseID := fmt.Sprintf("bud-response-%d", now.UnixNano())

		// Add Bud's response to conversation buffer
		bufferEntry := buffer.Entry{
			ID:        responseID,
			Author:    "Bud",
			Content:   content,
			Timestamp: now,
			ChannelID: channelID,
		}
		if err := conversationBuffer.Add(bufferEntry); err != nil {
			log.Printf("Warning: failed to add response to buffer: %v", err)
		}

		// Also store Bud's response as an episode in memory graph
		// This enables consolidation to capture Bud's observations and decisions
		if graphDB != nil {
			episode := &graph.Episode{
				ID:                responseID,
				Content:           content,
				Source:            "bud",
				Author:            "Bud",
				AuthorID:          "bud",
				Channel:           channelID,
				TimestampEvent:    now,
				TimestampIngested: now,
				DialogueAct:       "response",
				CreatedAt:         now,
			}
			if err := graphDB.AddEpisode(episode); err != nil {
				log.Printf("Warning: failed to store Bud episode: %v", err)
			} else {
				log.Printf("[main] Stored Bud response as episode for consolidation")
				// Create FOLLOWS edge from previous episode in same channel
				if prevID, ok := lastEpisodeByChannel[channelID]; ok {
					if err := graphDB.AddEpisodeEdge(prevID, episode.ID, graph.EdgeFollows, 1.0); err != nil {
						log.Printf("[main] Failed to add FOLLOWS edge for Bud response: %v", err)
					}
				}
				lastEpisodeByChannel[channelID] = episode.ID
			}
		}
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
		// Wire pending interaction callback for slash command followups
		discordEffector.SetPendingInteractionCallback(func(channelID string) *effectors.PendingInteraction {
			if interaction := discordSense.GetPendingInteraction(channelID); interaction != nil {
				return &effectors.PendingInteraction{
					Token: interaction.Token,
					AppID: interaction.AppID,
				}
			}
			return nil
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

		// Wire interaction reply callback for slash commands (edits deferred response)
		reflexEngine.SetInteractionReplyCallback(func(token, appID, message string) error {
			log.Printf("[reflex] Editing interaction response: %s", truncate(message, 50))
			_, err := discordSense.Session().InteractionResponseEdit(&discordgo.Interaction{
				AppID: appID,
				Token: token,
			}, &discordgo.WebhookEdit{
				Content: &message,
			})
			return err
		})

		// Register slash commands (guild-specific for fast updates, or global if no guild ID)
		if err := discordSense.RegisterSlashCommands(discordGuildID); err != nil {
			log.Printf("Warning: failed to register slash commands: %v", err)
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
						// Ingest to memory graph (Tier 1: episode, Tier 2: entities)
						ingestToMemoryGraph(msg)

						percept := msg.ToPercept()
						if percept != nil {
							processPercept(percept)
						}
					}
				}
			}
		}
	}()

	// Start focus queue processing
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

	// Start trigger file watcher goroutine (runs immediately, no delay)
	// This handles buffer.clear and consolidate.trigger signals from MCP
	consolidationTriggerFile := filepath.Join(statePath, "consolidate.trigger")
	bufferClearTriggerFile := filepath.Join(statePath, "buffer.clear")
	go func() {
		triggerCheckTicker := time.NewTicker(500 * time.Millisecond)
		defer triggerCheckTicker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-triggerCheckTicker.C:
				// Check for buffer clear trigger (written by MCP memory_reset)
				// This must be processed quickly for memory_reset to work correctly
				if _, err := os.Stat(bufferClearTriggerFile); err == nil {
					os.Remove(bufferClearTriggerFile)
					conversationBuffer.Clear()
					log.Println("[main] Conversation buffer cleared (memory_reset)")
				}
				// Check for consolidation trigger (written by MCP memory_flush)
				if _, err := os.Stat(consolidationTriggerFile); err == nil {
					os.Remove(consolidationTriggerFile)
					log.Printf("[consolidate] Running memory consolidation (trigger: memory_flush)...")
					count, err := memoryConsolidator.Run()
					if err != nil {
						log.Printf("[consolidate] Error: %v", err)
					} else if count > 0 {
						log.Printf("[consolidate] Created %d traces from unconsolidated episodes", count)
					} else {
						log.Println("[consolidate] No unconsolidated episodes found")
					}
				}
			}
		}
	}()
	log.Printf("[main] Trigger file watcher started (buffer.clear, consolidate.trigger)")

	// Start periodic memory consolidation goroutine (runs every 10 minutes)
	consolidationInterval := 10 * time.Minute
	go func() {
		consolidationTicker := time.NewTicker(consolidationInterval)
		defer consolidationTicker.Stop()

		// Initial delay before first periodic consolidation
		time.Sleep(2 * time.Minute)

		for {
			select {
			case <-stopChan:
				return
			case <-consolidationTicker.C:
				log.Printf("[consolidate] Running memory consolidation (trigger: periodic)...")
				count, err := memoryConsolidator.Run()
				if err != nil {
					log.Printf("[consolidate] Error: %v", err)
				} else if count > 0 {
					log.Printf("[consolidate] Created %d traces from unconsolidated episodes", count)
				} else {
					log.Println("[consolidate] No unconsolidated episodes found")
				}

				// Apply time-based activation decay
				// lambda=0.005 means ~12% decay per day, ~50% after ~6 days
				// floor=0.1 prevents traces from fully disappearing
				decayed, err := graphDB.DecayActivationByAge(0.005, 0.1)
				if err != nil {
					log.Printf("[consolidate] Activation decay error: %v", err)
				} else if decayed > 0 {
					log.Printf("[consolidate] Decayed activation for %d traces", decayed)
				}
			}
		}
	}()
	log.Printf("[main] Memory consolidation scheduled (interval: %v)", consolidationInterval)

	log.Println("[main] All subsystems started. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("[main] Shutting down...")

	// Stop subsystems
	close(stopChan)
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

	// Run final consolidation before shutdown
	log.Println("[main] Running final memory consolidation...")
	if count, err := memoryConsolidator.Run(); err != nil {
		log.Printf("Warning: final consolidation failed: %v", err)
	} else {
		log.Printf("[main] Final consolidation created %d traces", count)
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

// writeMCPConfig ensures .mcp.json has the bud2 server configured, preserving other servers
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

	// Read existing config if present
	config := map[string]any{}
	if data, err := os.ReadFile(".mcp.json"); err == nil {
		if err := json.Unmarshal(data, &config); err != nil {
			log.Printf("[main] Warning: existing .mcp.json invalid, will overwrite: %v", err)
			config = map[string]any{}
		}
	}

	// Ensure mcpServers map exists
	servers, ok := config["mcpServers"].(map[string]any)
	if !ok {
		servers = map[string]any{}
		config["mcpServers"] = servers
	}

	// Add/update bud2 server
	servers["bud2"] = map[string]any{
		"type":    "stdio",
		"command": budMCPPath,
		"args":    []string{},
		"env": map[string]string{
			"BUD_STATE_PATH": absStatePath,
		},
	}

	// Use encoder with SetEscapeHTML(false) to preserve characters like > in paths
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(config); err != nil {
		return err
	}

	if err := os.WriteFile(".mcp.json", buf.Bytes(), 0644); err != nil {
		return err
	}

	log.Printf("[main] Updated .mcp.json with BUD_STATE_PATH=%s (preserved %d other servers)", absStatePath, len(servers)-1)
	return nil
}
