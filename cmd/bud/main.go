package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	osExec "os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/engram"
	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/effectors"
	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/eval"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/focus"
	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/integrations/github"
	"github.com/vthunder/bud2/internal/logging"
	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/mcp/tools"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/profiling"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/senses"
	"github.com/vthunder/bud2/internal/state"
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
						// Check if running as a service (BUD_SERVICE=1)
						isService := os.Getenv("BUD_SERVICE") == "1"

						if !isService {
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
	mcpHTTPPort := os.Getenv("MCP_HTTP_PORT")
	if mcpHTTPPort == "" {
		mcpHTTPPort = "8066" // Default MCP HTTP port
	}

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

	// Initialize profiler
	profileLevel := profiling.LevelOff
	if pl := os.Getenv("BUD_PROFILE"); pl != "" {
		switch pl {
		case "minimal":
			profileLevel = profiling.LevelMinimal
		case "detailed":
			profileLevel = profiling.LevelDetailed
		case "trace":
			profileLevel = profiling.LevelTrace
		}
	}
	if err := profiling.Init(profileLevel, filepath.Join(statePath, "system", "profiling.jsonl")); err != nil {
		log.Printf("Warning: failed to initialize profiler: %v", err)
	} else if profileLevel != profiling.LevelOff {
		log.Printf("[profiling] Enabled at level: %s", profileLevel)
		defer profiling.Get().Close()
	}

	// Seed guides (how-to docs) from defaults if missing
	seedGuides(statePath)

	// Generate .mcp.json in state/ directory for Claude MCP tools
	if err := writeMCPConfig(statePath, mcpHTTPPort); err != nil {
		log.Printf("Warning: failed to write .mcp.json: %v", err)
	}

	// Initialize paths
	systemPath := filepath.Join(statePath, "system")
	queuesPath := filepath.Join(systemPath, "queues")
	os.MkdirAll(queuesPath, 0755)

	// Initialize v2 memory systems
	// Graph DB (SQLite) - replaces tracePool
	graphDB, err := graph.Open(statePath)
	if err != nil {
		log.Fatalf("Failed to initialize graph database: %v", err)
	}
	defer graphDB.Close()
	log.Println("[main] Graph database initialized")

	// Engram HTTP client - used by executive for memory retrieval
	engramURL := os.Getenv("ENGRAM_URL")
	engramAPIKey := os.Getenv("ENGRAM_API_KEY")
	var engramClient *engram.Client
	if engramURL != "" {
		engramClient = engram.NewClient(engramURL, engramAPIKey)
		log.Printf("[main] Engram client initialized: %s", engramURL)
	} else {
		log.Println("[main] Warning: ENGRAM_URL not set, executive memory retrieval disabled")
	}

	// Ollama embedding client - used by stateInspector and memoryJudge
	ollamaClient := embedding.NewClient("", "") // defaults: localhost:11434, nomic-embed-text

	// Ensure core.md exists in state/system directory (copy from seed if missing)
	coreFile := filepath.Join(systemPath, "core.md")
	if _, err := os.Stat(coreFile); os.IsNotExist(err) {
		seedPath := "seed/core.md"
		if data, err := os.ReadFile(seedPath); err == nil {
			if err := os.WriteFile(coreFile, data, 0644); err != nil {
				log.Printf("Warning: failed to create core.md: %v", err)
			} else {
				log.Printf("[main] Created %s from seed", coreFile)
			}
		} else {
			log.Printf("Warning: failed to read core seed: %v", err)
		}
	}

	// Load wakeup instructions for autonomous wake prompts
	wakeupInstructions := ""
	if data, err := os.ReadFile("seed/wakeup.md"); err == nil {
		wakeupInstructions = string(data)
		log.Printf("[main] Loaded wakeup instructions (%d bytes)", len(data))
	} else {
		log.Printf("[main] No wakeup.md found (autonomous wakes will use default prompt)")
	}

	// Initialize GTD store (always using JSON store - Things integration via MCP)
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

	// Initialize GitHub client (optional)
	var githubClient *github.Client
	if os.Getenv("GITHUB_TOKEN") != "" && os.Getenv("GITHUB_ORG") != "" {
		var err error
		githubClient, err = github.NewClient()
		if err != nil {
			log.Printf("Warning: failed to create GitHub client: %v", err)
		} else {
			log.Printf("[main] GitHub integration enabled (org: %s)", githubClient.Org())
		}
	} else {
		log.Println("[main] GitHub integration disabled (GITHUB_TOKEN or GITHUB_ORG not set)")
	}

	// Initialize state inspector for MCP tools
	stateInspector := state.NewInspector(statePath, graphDB)
	stateInspector.SetEmbedder(ollamaClient)

	// Initialize memory judge for MCP eval tools
	memoryJudge := eval.NewJudge(ollamaClient, graphDB)

	// Initialize MCP HTTP server (for Claude Code integration)
	mcpServer := mcp.NewServer()
	var mcpSendMessage func(channelID, message string) error          // Will be wired to Discord effector
	var mcpAddReaction func(channelID, messageID, emoji string) error // Will be wired to Discord effector

	// Declare variable for executive (will be initialized after MCP deps are set up)
	var exec *executive.ExecutiveV2

	mcpDeps := &tools.Dependencies{
		EngramClient:   engramClient,
		ActivityLog:    activityLog,
		StateInspector: stateInspector,
		StatePath:      statePath,
		SystemPath:     systemPath,
		QueuesPath:     queuesPath,
		DefaultChannel: discordChannel,
		ReflexEngine:   reflexEngine,
		GTDStore:       gtdStore,
		MemoryJudge:    memoryJudge,
		CalendarClient: calendarClient,
		GitHubClient:   githubClient,
		SendMessage: func(channelID, message string) error {
			if mcpSendMessage != nil {
				return mcpSendMessage(channelID, message)
			}
			return fmt.Errorf("Discord effector not yet initialized")
		},
		AddReaction: func(channelID, messageID, emoji string) error {
			if mcpAddReaction != nil {
				return mcpAddReaction(channelID, messageID, emoji)
			}
			return fmt.Errorf("Discord effector not yet initialized")
		},
		AddThought: nil, // Will be set after processInboxMessage is defined
		OnMCPToolCall: func(toolName string) {
			if exec != nil {
				exec.GetMCPToolCallback()(toolName)
			}
		},
	}

	// Register all MCP tools
	tools.RegisterAll(mcpServer, mcpDeps)
	log.Printf("[main] MCP server initialized with %d tools", mcpServer.ToolCount())

	// Start stdio MCP proxy servers from .mcp.json and register their tools
	// This makes things-mcp (and any other stdio servers) available to both
	// Claude sessions (via HTTP) and the reflex engine (via call_tool action).
	mcpConfigPath := filepath.Join(statePath, ".mcp.json")
	if proxyClients, proxyErr := mcp.StartProxiesFromConfig(mcpConfigPath, mcpServer); proxyErr != nil {
		log.Printf("[main] Warning: failed to start MCP proxies: %v", proxyErr)
	} else if len(proxyClients) > 0 {
		log.Printf("[main] Started %d MCP proxy server(s), total tools: %d", len(proxyClients), mcpServer.ToolCount())
		defer func() {
			for _, p := range proxyClients {
				p.Close()
			}
		}()
	}

	// Wire the MCP server as the tool caller for the reflex engine
	// This lets call_tool pipeline actions invoke any registered MCP tool
	reflexEngine.SetToolCaller(mcpServer)
	log.Printf("[main] Reflex engine wired to MCP tool dispatcher")

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

	// Declare variable for fallback callback (will be set after discordEffector is created)
	var fallbackSendMessage func(channelID, message string) error

	// Initialize v2 executive with focus-based attention
	// Note: exec is already declared above so OnMCPToolCall can reference it
	exec = executive.NewExecutiveV2(
		engramClient, // HTTP client for memory retrieval (nil = disabled)
		reflexLog,
		statePath, // Executive will construct paths like state/system/core.md from this
		executive.ExecutiveV2Config{
			Model:              claudeModel,
			WorkDir:            statePath, // Run Claude from state/ to pick up .mcp.json
			BotAuthor:          "Bud",     // Kept for compatibility, but no longer used
			SessionTracker:     sessionTracker,
			WakeupInstructions: wakeupInstructions,
			SendMessageFallback: func(channelID, message string) error {
				if fallbackSendMessage != nil {
					return fallbackSendMessage(channelID, message)
				}
				log.Printf("[fallback] ERROR: discordEffector not yet initialized")
				return fmt.Errorf("effector not initialized")
			},
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

					// Format memory eval for display
					evalStr := ""
					if memoryEval != nil && len(memoryEval) > 0 {
						evalStr = fmt.Sprintf(" Memory eval: %v", memoryEval)
					}

					logging.Info("executive", "Done: %s in %.0fs, %d tokens.%s",
						msg.Content, session.DurationSec, session.OutputTokens, evalStr)
				}
			}
			// Log memory eval separately for easier querying
			// Resolve M1/M2 display IDs to actual trace IDs for cross-session analysis
			if memoryEval != nil {
				evalData := map[string]any{"eval": memoryEval}
				if session := exec.GetSession(); session != nil {
					resolved := session.ResolveMemoryEval(memoryEval)
					if len(resolved) > 0 {
						evalData["resolved"] = resolved // trace_id -> rating
					}
				}
				activityLog.Log(activity.Entry{
					Type:    "memory_eval",
					Summary: "Memory self-evaluation via signal_done",
					Data:    evalData,
				})
			}

		case "reset_session":
			// Memory reset requested - generate new session ID so old context is not loaded
			log.Printf("[main] Signal: reset_session - generating new session ID")
			exec.ResetSession()
		}
	}

	// Track last Engram episode ID per channel for FOLLOWS edges
	var lastEpisodeMu sync.Mutex
	lastEngramIDByChannel := make(map[string]string) // channel → Engram episode ID (ep-{uuid})

	// Track last episode creation time for idle-based consolidation triggering (unix nanos, atomic)
	var lastEpisodeTimeNs int64 = time.Now().UnixNano()

	// Ingest message as episode into Engram (Tier 1). Engram handles NER entity linking on ingest.
	ingestToMemoryGraph := func(msg *memory.InboxMessage) {
		defer profiling.Get().Start(msg.ID, "ingest.total")()

		if msg == nil || msg.Content == "" {
			return
		}

		if engramClient == nil {
			logging.Debug("ingest", "ENGRAM_URL not set, skipping episode ingest")
			return
		}

		var replyTo string
		if msg.Extra != nil {
			if r, ok := msg.Extra["reply_to"].(string); ok {
				replyTo = r
			}
		}

		req := engram.IngestEpisodeRequest{
			Content:        msg.Content,
			Source:         "discord",
			Author:         msg.Author,
			AuthorID:       msg.AuthorID,
			Channel:        msg.ChannelID,
			TimestampEvent: msg.Timestamp,
			ReplyTo:        replyTo,
		}

		var result *engram.IngestResult
		var ingestErr error
		func() {
			defer profiling.Get().Start(msg.ID, "ingest.episode_store")()
			result, ingestErr = engramClient.IngestEpisode(req)
		}()
		if ingestErr != nil {
			log.Printf("[ingest] Failed to store episode: %v", ingestErr)
			return
		}
		atomic.StoreInt64(&lastEpisodeTimeNs, time.Now().UnixNano())

		// Create FOLLOWS edge from previous episode in same channel
		lastEpisodeMu.Lock()
		prevID := lastEngramIDByChannel[msg.ChannelID]
		lastEngramIDByChannel[msg.ChannelID] = result.ID
		lastEpisodeMu.Unlock()
		if prevID != "" {
			if err := engramClient.AddEpisodeEdge(prevID, result.ID, "follows", 1.0); err != nil {
				log.Printf("[ingest] Failed to add FOLLOWS edge: %v", err)
			}
		}
	}

	// Process percept helper - checks reflexes first, then routes to focus queue
	processPercept := func(percept *types.Percept) {
		// L1: Overall percept processing
		perceptID := fmt.Sprintf("percept-%d", time.Now().UnixNano())
		defer profiling.Get().Start(perceptID, "percept.total")()

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

		// Note: episodes are now used directly for conversation history (no separate buffer)

		// Log input event
		inputSummary := content
		if author != "" {
			inputSummary = fmt.Sprintf("%s: %s", author, truncate(content, 100))
		}
		activityLog.LogInput(inputSummary, percept.Source, channelID)

		// Check for reflex config updates (hot reload)
		if reloaded := reflexEngine.CheckForUpdates(); reloaded > 0 {
			logging.Debug("main", "Hot-reloaded %d reflex config(s)", reloaded)
		}

		// Try reflexes first
		var handled bool
		var results []*reflex.ReflexResult
		func() {
			defer profiling.Get().Start(perceptID, "percept.reflex_check")()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			handled, results = reflexEngine.Process(ctx, percept.Source, percept.Type, content, percept.Data)
		}()

		if handled && len(results) > 0 {
			result := results[0]
			if result.Stopped {
				intent, _ := result.Output["intent"].(string)
				// Will be logged when routed to executive
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

				logging.Info("main", "Message from %s: %s → handler: reflex %s", author, logging.Truncate(content, 40), result.ReflexName)
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
					logging.Debug("main", "Autonomous percept blocked: %s", reason)
					return
				}
			} else {
				logging.Debug("main", "High-priority urgent task bypassing budget")
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
			// Bud's thoughts are stored in memory but don't queue for focus
			return
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

		func() {
			defer profiling.Get().Start(perceptID, "percept.queue_add")()
			if err := exec.AddPending(item); err != nil {
				log.Printf("[main] Failed to add to focus queue: %v", err)
			}
		}()

		// Will be logged by executive when it starts processing
	}

	// processInboxMessage handles incoming messages from senses (Discord, Calendar, etc.)
	// This is called directly by the senses (no queueing/polling)
	processInboxMessage := func(msg *memory.InboxMessage) {
		// L1: Overall message processing
		defer profiling.Get().Start(msg.ID, "processInboxMessage")()

		switch msg.Type {
		case "signal":
			handleSignal(msg)

		case "impulse":
			percept := msg.ToPercept()
			if percept != nil {
				processPercept(percept)
			}

		default: // "message" or empty (backward compat)
			// Ingest to memory graph asynchronously (Tier 1: episode)
			// Fire-and-forget: episode store runs in background so processPercept runs immediately.
			// Executive reads from conversation history context, not the live episode store.
			go ingestToMemoryGraph(msg)

			percept := msg.ToPercept()
			if percept != nil {
				processPercept(percept)
			}
		}
	}

	// Wire AddThought callback now that processInboxMessage is defined
	mcpDeps.AddThought = func(content string) error {
		msg := &memory.InboxMessage{
			ID:        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
			Subtype:   "thought",
			Content:   content,
			Timestamp: time.Now(),
			Status:    "pending",
		}
		processInboxMessage(msg)
		return nil
	}

	// Wire SendSignal callback for signal_done and memory_reset
	mcpDeps.SendSignal = func(signalType, content string, extra map[string]any) error {
		msg := &memory.InboxMessage{
			ID:        fmt.Sprintf("signal-%d", time.Now().UnixNano()),
			Type:      "signal",
			Subtype:   signalType,
			Content:   content,
			Timestamp: time.Now(),
			Status:    "pending",
			Extra:     extra,
		}
		processInboxMessage(msg)
		return nil
	}

	// Capture outgoing response as episode in Engram
	captureResponse := func(channelID, content string) {
		if engramClient == nil {
			return
		}

		result, err := engramClient.IngestEpisode(engram.IngestEpisodeRequest{
			Content:        content,
			Source:         "bud",
			Author:         "Bud",
			AuthorID:       "bud",
			Channel:        channelID,
			TimestampEvent: time.Now(),
		})
		if err != nil {
			log.Printf("Warning: failed to store Bud episode: %v", err)
			return
		}
		logging.Debug("main", "Stored Bud response as episode")
		atomic.StoreInt64(&lastEpisodeTimeNs, time.Now().UnixNano())

		lastEpisodeMu.Lock()
		prevBudID := lastEngramIDByChannel[channelID]
		lastEngramIDByChannel[channelID] = result.ID
		lastEpisodeMu.Unlock()
		if prevBudID != "" {
			if err := engramClient.AddEpisodeEdge(prevBudID, result.ID, "follows", 1.0); err != nil {
				logging.Debug("main", "Failed to add FOLLOWS edge: %v", err)
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
		}, processInboxMessage)
		if err != nil {
			log.Fatalf("Failed to create Discord sense: %v", err)
		}

		if err := discordSense.Start(); err != nil {
			log.Fatalf("Failed to start Discord sense: %v", err)
		}

		discordEffector = effectors.NewDiscordEffector(
			discordSense.Session,
			nil, // No outbox polling - using direct Submit() only
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

		// Wire reflex reply callback to effector directly
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
				Timestamp: time.Now(),
			}
			discordEffector.Submit(action)
			return nil
		}

		// Wire MCP talk_to_user to effector directly (bypasses outbox file)
		mcpSendMessage = func(channelID, message string) error {
			logging.Info("main", "Sending message: %s", logging.Truncate(message, 50))
			action := &types.Action{
				ID:       fmt.Sprintf("mcp-reply-%d", time.Now().UnixNano()),
				Type:     "send_message",
				Effector: "discord",
				Payload: map[string]any{
					"channel_id": channelID,
					"content":    message,
				},
				Timestamp: time.Now(),
			}
			discordEffector.Submit(action)
			return nil
		}

		// Wire fallback callback to effector
		fallbackSendMessage = func(channelID, message string) error {
			log.Printf("[fallback] Sending fallback message to channel %s", channelID)
			action := &types.Action{
				ID:       fmt.Sprintf("fallback-%d", time.Now().UnixNano()),
				Type:     "send_message",
				Effector: "discord",
				Payload: map[string]any{
					"channel_id": channelID,
					"content":    message,
				},
				Timestamp: time.Now(),
			}
			discordEffector.Submit(action)
			return nil
		}

		// Wire MCP discord_react to effector directly (bypasses outbox file)
		mcpAddReaction = func(channelID, messageID, emoji string) error {
			log.Printf("[mcp] Reacting %s", emoji)
			action := &types.Action{
				ID:       fmt.Sprintf("mcp-react-%d", time.Now().UnixNano()),
				Type:     "add_reaction",
				Effector: "discord",
				Payload: map[string]any{
					"channel_id": channelID,
					"message_id": messageID,
					"emoji":      emoji,
				},
				Timestamp: time.Now(),
			}
			discordEffector.Submit(action)
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
		// SYNTHETIC_MODE: Create test effector that captures to file
		testEffector := effectors.NewTestEffector(statePath)
		testEffector.Start()

		// Wire MCP callbacks to test effector
		mcpSendMessage = func(channelID, message string) error {
			logging.Info("main", "Sending message (test): %s", logging.Truncate(message, 50))
			action := &types.Action{
				ID:       fmt.Sprintf("mcp-reply-%d", time.Now().UnixNano()),
				Type:     "send_message",
				Effector: "test",
				Payload: map[string]any{
					"channel_id": channelID,
					"content":    message,
				},
				Timestamp: time.Now(),
			}
			testEffector.Submit(action)
			return nil
		}

		mcpAddReaction = func(channelID, messageID, emoji string) error {
			log.Printf("[mcp-test] Adding reaction to message %s: %s", messageID, emoji)
			action := &types.Action{
				ID:       fmt.Sprintf("mcp-react-%d", time.Now().UnixNano()),
				Type:     "add_reaction",
				Effector: "test",
				Payload: map[string]any{
					"channel_id": channelID,
					"message_id": messageID,
					"emoji":      emoji,
				},
				Timestamp: time.Now(),
			}
			testEffector.Submit(action)
			return nil
		}

		log.Println("[main] SYNTHETIC_MODE enabled - using test effector")
		log.Println("[main] Write to inbox.jsonl, read from test_output.jsonl")
	}

	// Send startup message so future sessions can tell when a restart happened
	if mcpSendMessage != nil && discordChannel != "" {
		tz := userTimezone
		if tz == nil {
			tz = time.UTC
		}
		ts := time.Now().In(tz).Format("15:04 MST")
		startupMsg := fmt.Sprintf("♻️ Back at %s", ts)
		if err := mcpSendMessage(discordChannel, startupMsg); err != nil {
			log.Printf("[main] Warning: failed to send startup message: %v", err)
		}
	}

	// Start calendar sense (optional, independent of Discord)
	var calendarSense *senses.CalendarSense
	if calendarClient != nil {
		calendarSense = senses.NewCalendarSense(senses.CalendarConfig{
			Client:    calendarClient,
			Timezone:  userTimezone,
			StatePath: filepath.Join(statePath, "calendar_state.json"),
		}, processInboxMessage)

		// Load persisted state (prevents duplicate notifications across restarts)
		if err := calendarSense.Load(); err != nil {
			log.Printf("Warning: failed to load calendar state: %v", err)
		}

		if err := calendarSense.Start(); err != nil {
			log.Printf("Warning: failed to start calendar sense: %v", err)
		} else {
			log.Println("[main] Calendar sense started")
		}
	}

	// Register /log-tool endpoint for Claude Code PostToolUse hook observability
	// This receives tool call data from hooks and logs it to activity.jsonl
	mcpServer.RegisterHTTPHandler("/log-tool", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}

		toolName, _ := payload["tool_name"].(string)
		summary := fmt.Sprintf("tool_call: %s", toolName)
		activityLog.Log(activity.Entry{
			Type:    activity.TypeAction,
			Summary: summary,
			Data:    payload,
		})
		w.WriteHeader(http.StatusNoContent)
	})

	// Start MCP HTTP server (for Claude Code integration)
	go func() {
		addr := "127.0.0.1:" + mcpHTTPPort
		log.Printf("[main] Starting MCP HTTP server on %s", addr)
		if err := mcpServer.RunHTTP(addr); err != nil {
			log.Printf("[main] MCP HTTP server error: %v", err)
		}
	}()

	// P1 goroutine: event-driven, fires immediately on user message arrival.
	// Drains all P0/P1 items after each notification, serializing user sessions.
	go func() {
		notifyCh := exec.GetQueue().NotifyChannel()
		for {
			select {
			case <-stopChan:
				return
			case <-notifyCh:
				for {
					ctx := context.Background()
					processed, err := exec.ProcessNextP1(ctx)
					if err != nil {
						log.Printf("[main] P1 executive error: %v", err)
					}
					if !processed {
						break
					}
				}
			}
		}
	}()

	// Background goroutine: 500ms ticker for P2+ items (autonomous wakes, scheduled tasks).
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-ticker.C:
				ctx := context.Background()
				_, err := exec.ProcessNextBackground(ctx)
				if err != nil {
					log.Printf("[main] Background executive error: %v", err)
				}
			}
		}
	}()

	// Start autonomous wake-up goroutine (periodic self-initiated work)
	if autonomousEnabled {
		log.Printf("[main] Autonomous mode enabled (interval: %v)", autonomousInterval)
		go func() {
			periodicTicker := time.NewTicker(autonomousInterval)
			defer periodicTicker.Stop()

			time.Sleep(10 * time.Second)

			for {
				select {
				case <-stopChan:
					return

				case <-periodicTicker.C:
					// Skip wake during quiet hours (23:00–07:00 local time) to avoid
					// wasting resources while the user is sleeping.
					tz := userTimezone
					if tz == nil {
						tz = time.UTC
					}
					now := time.Now().In(tz)
					hour := now.Hour()
					if hour >= 23 || hour < 7 {
						log.Printf("[autonomous] Quiet hours (%02d:00 %s) — skipping wake", hour, tz)
						continue
					}

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
					processInboxMessage(inboxMsg)
				}
			}
		}()
	} else {
		log.Println("[main] Autonomous mode disabled (set AUTONOMOUS_ENABLED=true to enable)")
	}

	// Start trigger file watcher goroutine (runs immediately, no delay)
	// This handles consolidate.trigger signals from MCP
	consolidationTriggerFile := filepath.Join(statePath, "consolidate.trigger")
	go func() {
		triggerCheckTicker := time.NewTicker(500 * time.Millisecond)
		defer triggerCheckTicker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-triggerCheckTicker.C:
				// Check for consolidation trigger (written by MCP memory_flush)
				if _, err := os.Stat(consolidationTriggerFile); err == nil {
					os.Remove(consolidationTriggerFile)
					if engramClient == nil {
						log.Printf("[consolidate] Engram client not available, skipping consolidation")
						continue
					}
					log.Printf("[consolidate] Running memory consolidation (trigger: memory_flush)...")
					result, err := engramClient.Consolidate()
					if err != nil {
						log.Printf("[consolidate] Error: %v", err)
					} else if result.TracesCreated > 0 {
						log.Printf("[consolidate] Created %d traces from unconsolidated episodes", result.TracesCreated)
					} else {
						log.Println("[consolidate] No unconsolidated episodes found")
					}
				}
			}
		}
	}()
	log.Printf("[main] Trigger file watcher started (buffer.clear, consolidate.trigger)")

	// Start smart memory consolidation goroutine.
	// Triggers based on three conditions (checked every minute):
	//   (a) idle ≥ 20 min (no new episodes) AND pending ≥ 10
	//   (b) pending ≥ 100 (flush backlog regardless of idle state)
	//   (c) time since last consolidation > 4h AND pending ≥ 10 (safety fallback)
	go func() {
		checkTicker := time.NewTicker(1 * time.Minute)
		defer checkTicker.Stop()

		// Initial delay: let things settle before first check
		time.Sleep(2 * time.Minute)

		var lastConsolidationTime time.Time

		for {
			select {
			case <-stopChan:
				return
			case <-checkTicker.C:
				if engramClient == nil {
					continue
				}
				pendingCount, err := engramClient.GetUnconsolidatedEpisodeCount()
				if err != nil || pendingCount == 0 {
					continue
				}

				lastEpNs := atomic.LoadInt64(&lastEpisodeTimeNs)
				idleEnough := time.Since(time.Unix(0, lastEpNs)) >= 20*time.Minute
				backlogFull := pendingCount >= 100
				fallbackDue := !lastConsolidationTime.IsZero() &&
					time.Since(lastConsolidationTime) >= 4*time.Hour &&
					pendingCount >= 10

				if !((idleEnough && pendingCount >= 10) || backlogFull || fallbackDue) {
					continue
				}

				trigger := "idle"
				if backlogFull {
					trigger = "flush"
				} else if fallbackDue {
					trigger = "fallback"
				}

				log.Printf("[consolidate] Triggering consolidation (pending=%d, trigger=%s)", pendingCount, trigger)
				result, err := engramClient.Consolidate()
				if err != nil {
					log.Printf("[consolidate] Error: %v", err)
				} else if result.TracesCreated > 0 {
					log.Printf("[consolidate] Created %d traces from unconsolidated episodes", result.TracesCreated)
				} else {
					log.Println("[consolidate] No unconsolidated episodes found")
				}
				lastConsolidationTime = time.Now()
			}
		}
	}()

	// Activation decay on its own 1-hour schedule, decoupled from consolidation.
	// lambda=0.01 achieves ~21% decay per day: exp(-0.01*24) ≈ 0.787
	// Creates 7-day half-life for boosted traces (0.6→0.1 in ~7 days)
	// floor=0.1 prevents traces from fully disappearing
	go func() {
		decayTicker := time.NewTicker(1 * time.Hour)
		defer decayTicker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-decayTicker.C:
				if engramClient == nil {
					continue
				}
				result, err := engramClient.DecayActivation(0.01, 0.1)
				if err != nil {
					log.Printf("[consolidate] Activation decay error: %v", err)
				} else if result.Updated > 0 {
					log.Printf("[consolidate] Decayed activation for %d traces", result.Updated)
				}
			}
		}
	}()
	log.Printf("[main] Memory consolidation scheduled (smart trigger: idle=20m, flush=100, fallback=4h)")

	// Periodic state sync: commit and push state/ to bud2-state repo every hour.
	// This replaces the old impulse:task recurring mechanism removed in the Things 3 migration.
	stateSyncPath := statePath // capture for goroutine
	go func() {
		syncTicker := time.NewTicker(1 * time.Hour)
		defer syncTicker.Stop()

		for {
			select {
			case <-stopChan:
				return
			case <-syncTicker.C:
				cmd := osExec.Command("bash", "-c",
					`git add -A && git diff --cached --quiet || git commit -m "auto sync $(date +%Y-%m-%d-%H%M)" && git push`,
				)
				cmd.Dir = stateSyncPath
				if out, err := cmd.CombinedOutput(); err != nil {
					log.Printf("[state-sync] Error: %v — %s", err, strings.TrimSpace(string(out)))
				} else {
					log.Printf("[state-sync] State synced to git")
				}
			}
		}
	}()
	log.Printf("[main] State sync scheduled (every 1h)")

	// Outbox is append-only, no periodic save needed

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

	// Persist state
	if err := exec.GetQueue().Save(); err != nil {
		log.Printf("Warning: failed to save focus queue: %v", err)
	}

	log.Println("[main] Goodbye!")
}

// Note: Core identity is now loaded from state/core.md (file-based, not database)
// The bootstrapCoreTraces function has been removed in favor of simple file loading

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

// writeMCPConfig writes .mcp.json to the state directory with HTTP MCP server configuration
// This allows Claude sessions to discover and use the MCP tools
func writeMCPConfig(statePath, httpPort string) error {
	mcpConfigPath := filepath.Join(statePath, ".mcp.json")

	// Read existing config if present
	config := map[string]any{}
	if data, err := os.ReadFile(mcpConfigPath); err == nil {
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

	// Add/update bud2 server with HTTP transport
	// HTTP transport connects to the embedded MCP server in the main bud process
	servers["bud2"] = map[string]any{
		"type": "http",
		"url":  fmt.Sprintf("http://127.0.0.1:%s/mcp", httpPort),
	}

	// Use encoder with SetEscapeHTML(false) to preserve characters like > in paths
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(config); err != nil {
		return err
	}

	if err := os.WriteFile(mcpConfigPath, buf.Bytes(), 0644); err != nil {
		return err
	}

	log.Printf("[main] Wrote %s with HTTP MCP server at port %s", mcpConfigPath, httpPort)
	return nil
}
