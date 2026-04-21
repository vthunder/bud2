package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"github.com/shirou/gopsutil/v3/process"
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/config"
	"github.com/vthunder/bud2/internal/effectors"
	"github.com/vthunder/bud2/internal/embedding"
	"github.com/vthunder/bud2/internal/engram"
	"github.com/vthunder/bud2/internal/eval"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/executive/provider"
	"github.com/vthunder/bud2/internal/extensions"
	"github.com/vthunder/bud2/internal/focus"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/integrations/github"
	"github.com/vthunder/bud2/internal/logging"
	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/mcp/tools"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/paths"
	"github.com/vthunder/bud2/internal/profiling"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/senses"
	"github.com/vthunder/bud2/internal/state"
	"github.com/vthunder/bud2/internal/terminal"
	"github.com/vthunder/bud2/internal/tmux"
	"github.com/vthunder/bud2/internal/types"
	"github.com/vthunder/bud2/internal/zellij"
)

const Version = "2026-01-13-v2-focus-cutover"

// checkPidFile checks for an existing bud process and handles it
// Returns a cleanup function to remove the pid file on exit
func checkPidFile(statePath string) func() {
	pidFile := filepath.Join(statePath, "system", "bud.pid")

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

	// Load bud config from --config flag or BUD_CONFIG env var
	var budCfg *config.BudConfig
	configPath := os.Getenv("BUD_CONFIG")
	for i, arg := range os.Args[1:] {
		if arg == "--config" && i+1 < len(os.Args)-1 {
			configPath = os.Args[i+2]
			break
		}
		if strings.HasPrefix(arg, "--config=") {
			configPath = strings.TrimPrefix(arg, "--config=")
			break
		}
	}
	if configPath != "" {
		loaded, err := config.Load(configPath)
		if err != nil {
			log.Fatalf("[config] Failed to load config from %s: %v", configPath, err)
		}
		budCfg = loaded
		log.Printf("[config] Loaded config from %s", configPath)
	} else {
		budCfg = config.DefaultConfig()
		log.Println("[config] Using default config (no --config flag or BUD_CONFIG env)")
	}

	// Initialize terminal window manager based on config
	var termManager terminal.Manager
	switch budCfg.GetTerminalManager() {
	case "tmux":
		termManager = tmux.NewManager()
		log.Println("[config] Using tmux terminal manager")
	default:
		termManager = zellij.NewManager()
		log.Println("[config] Using zellij terminal manager")
	}

	// Config from environment
	discordToken := os.Getenv("DISCORD_TOKEN")
	discordChannel := os.Getenv("DISCORD_CHANNEL_ID")
	discordOwner := os.Getenv("DISCORD_OWNER_ID")
	discordGuildID := os.Getenv("DISCORD_GUILD_ID") // For slash command registration
	statePath := os.Getenv("STATE_PATH")
	if statePath == "" {
		home, _ := os.UserHomeDir()
		if runtime.GOOS == "darwin" {
			statePath = filepath.Join(home, "Documents", "bud-state")
		} else if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			statePath = filepath.Join(xdg, "bud", "state")
		} else {
			statePath = filepath.Join(home, ".local", "share", "bud", "state")
		}
	}
	if abs, err := filepath.Abs(statePath); err == nil {
		statePath = abs
	}
	providerName, modelID, err := budCfg.ResolveModel("executive")
	if err != nil {
		log.Fatalf("[config] Failed to resolve executive model: %v", err)
	}
	log.Printf("[config] Executive provider: %s, model: %s", providerName, modelID)
	claudeModel := os.Getenv("CLAUDE_MODEL")
	if claudeModel == "" && providerName == "claude-code" {
		claudeModel = modelID
	}
	if claudeModel == "" {
		claudeModel = "claude-sonnet-4-20250514"
	}
	mcpHTTPPort := os.Getenv("MCP_HTTP_PORT")
	if mcpHTTPPort == "" {
		mcpHTTPPort = "8066" // Default MCP HTTP port
	}

	// Create the appropriate provider based on config
	var executiveProvider provider.Provider
	var agentProvider provider.Provider
	switch providerName {
	case "claude-code":
		executiveProvider = provider.NewClaudeCodeProvider(claudeModel)
		log.Printf("[config] Using claude-code provider with model %s", claudeModel)
	case "opencode-serve":
		apiKey, _ := budCfg.APIKey(providerName)
		ocModel := modelID
		ocBinPath := "" // will use PATH lookup
		ocProvider := provider.NewOpenCodeServeProvider(ocBinPath, apiKey, ocModel, "")
		if cw := budCfg.ContextWindow(providerName, modelID); cw > 0 {
			ocProvider.WithContextWindow(cw)
			log.Printf("[config] Using opencode-serve provider with model %s (context window: %d)", ocModel, cw)
		} else {
			log.Printf("[config] Using opencode-serve provider with model %s (default context window)", ocModel)
		}
		executiveProvider = ocProvider
	default:
		log.Fatalf("[config] Unsupported provider type: %s", providerName)
	}

	// Resolve agent provider (defaults to executive provider if not specified)
	agentProviderName, agentModelID, err := budCfg.ResolveModel("agent")
	if err != nil {
		// No agent-specific config, use executive provider
		agentProvider = executiveProvider
	} else {
		switch agentProviderName {
		case "claude-code":
			agentProvider = provider.NewClaudeCodeProvider(agentModelID)
			log.Printf("[config] Using claude-code provider for agents with model %s", agentModelID)
		case "opencode-serve":
			apiKey, _ := budCfg.APIKey(agentProviderName)
			ocModel := agentModelID
			ocBinPath := ""
			ocAgentProvider := provider.NewOpenCodeServeProvider(ocBinPath, apiKey, ocModel, "")
			if cw := budCfg.ContextWindow(agentProviderName, agentModelID); cw > 0 {
				ocAgentProvider.WithContextWindow(cw)
			}
			agentProvider = ocAgentProvider
			log.Printf("[config] Using opencode-serve provider for agents with model %s", ocModel)
		default:
			log.Printf("[config] Warning: unsupported agent provider type %s, falling back to executive provider", agentProviderName)
			agentProvider = executiveProvider
		}
	}

	// Check for existing bud process (before creating state directory)
	os.MkdirAll(filepath.Join(statePath, "system"), 0755) // Ensure system/ dir exists for pid file
	cleanupPidFile := checkPidFile(statePath)
	defer cleanupPidFile()
	autonomousEnabled := os.Getenv("AUTONOMOUS_ENABLED") == "true"
	autonomousIntervalStr := os.Getenv("AUTONOMOUS_INTERVAL")
	autonomousIdleRequiredStr := os.Getenv("AUTONOMOUS_IDLE_REQUIRED")
	autonomousSessionCapStr := os.Getenv("AUTONOMOUS_SESSION_CAP")
	dailyTokenBudgetStr := os.Getenv("DAILY_OUTPUT_TOKEN_BUDGET")
	userTimezoneStr := os.Getenv("USER_TIMEZONE") // e.g., "Europe/Berlin"

	// Parse autonomous interval (default 2 hours)
	autonomousInterval := 2 * time.Hour
	if autonomousIntervalStr != "" {
		if d, err := time.ParseDuration(autonomousIntervalStr); err == nil {
			autonomousInterval = d
		}
	}

	// Parse idle required before autonomous wake (default 0 = no idle gate).
	// Set AUTONOMOUS_IDLE_REQUIRED=15m to skip wakes while user is recently active.
	autonomousIdleRequired := time.Duration(0)
	if autonomousIdleRequiredStr != "" {
		if d, err := time.ParseDuration(autonomousIdleRequiredStr); err == nil {
			autonomousIdleRequired = d
		}
	}

	// Parse autonomous session cap (default 8 minutes).
	// Executive wake sessions are capped at this duration so they stay short
	// and delegate real work to subagents rather than doing it inline.
	autonomousSessionCap := 8 * time.Minute
	if autonomousSessionCapStr != "" {
		if d, err := time.ParseDuration(autonomousSessionCapStr); err == nil {
			autonomousSessionCap = d
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

	if discordToken == "" {
		log.Fatal("DISCORD_TOKEN environment variable required")
	}

	// Ensure state directory exists
	os.MkdirAll(statePath, 0755)

	// Initialize profiler (disabled 2026-04-07; re-enable via BUD_PROFILE env var)
	profileLevel := profiling.LevelOff
	// if pl := os.Getenv("BUD_PROFILE"); pl != "" {
	// 	switch pl {
	// 	case "minimal":
	// 		profileLevel = profiling.LevelMinimal
	// 	case "detailed":
	// 		profileLevel = profiling.LevelDetailed
	// 	case "trace":
	// 		profileLevel = profiling.LevelTrace
	// 	}
	// }
	if err := profiling.Init(profileLevel, filepath.Join(statePath, "system", "profiling.jsonl")); err != nil {
		log.Printf("Warning: failed to initialize profiler: %v", err)
	} else if profiling.Get().IsEnabled() {
		log.Printf("[profiling] Enabled at level: %s", profileLevel)
		defer profiling.Get().Close()
	}

	// Ensure state directory structure exists (but don't copy from defaults —
	// ResolveFile/MergeDir handle the overlay at read time).
	paths.EnsureStateSystemDirs(statePath)

	// Initialize paths
	systemPath := filepath.Join(statePath, "system")
	queuesPath := filepath.Join(systemPath, "queues")
	os.MkdirAll(queuesPath, 0755)

	// Engram HTTP client - used by executive for memory retrieval
	engramURL := os.Getenv("ENGRAM_URL")
	engramAPIKey := os.Getenv("ENGRAM_API_KEY")
	var engramClient *engram.Client
	if engramURL != "" {
		engramClient = engram.NewClient(engramURL, engramAPIKey)
		log.Printf("[main] Engram client initialized: %s", engramURL)
		if err := engramClient.Health(); err != nil {
			log.Printf("[main] Warning: engram health check failed: %v", err)
		} else {
			log.Printf("[main] Engram health check OK")
		}
	} else {
		log.Println("[main] Warning: ENGRAM_URL not set, executive memory retrieval disabled")
	}

	// Ollama embedding client - used by stateInspector and memoryJudge
	ollamaClient := embedding.NewClient("", "") // defaults: localhost:11434, nomic-embed-text

	// Load wakeup instructions (state override, then defaults)
	wakeupContent, _ := paths.ResolveFile(statePath, "wakeup.md")
	wakeupInstructions := wakeupContent

	// Load startup instructions (state override, then defaults)
	startupContent, _ := paths.ResolveFile(statePath, "startup-instructions.md")
	startupInstructions := startupContent

	// Initialize activity logger for observability
	activityLog := activity.New(statePath)

	// Initialize session tracker and signal processor for thinking time budget
	sessionTracker := budget.NewSessionTracker(statePath)
	thinkingBudget := budget.NewThinkingBudget(sessionTracker)
	thinkingBudget.DailyOutputTokens = dailyOutputTokenBudget

	todayUsage := sessionTracker.TodayTokenUsage()
	log.Printf("[main] Session tracker initialized (output tokens today: %dk, budget: %dk, sessions: %d)",
		todayUsage.OutputTokens/1000, dailyOutputTokenBudget/1000, todayUsage.SessionCount)
	if sessionTracker.HasActiveSessions() {
		log.Printf("[main] Warning: session tracker has active sessions at startup — possible unclean shutdown")
	}

	// Initialize reflex engine
	reflexEngine := reflex.NewEngine(statePath)
	if err := reflexEngine.Load(); err != nil {
		log.Printf("Warning: failed to load reflexes: %v", err)
	}
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
	stateInspector := state.NewInspector(statePath)

	// Initialize memory judge for MCP eval tools
	memoryJudge := eval.NewJudge(ollamaClient, engramClient)

	// Initialize MCP HTTP server (for Claude Code integration)
	mcpServer := mcp.NewServer()
	var mcpSendMessage func(channelID, message string) error          // Will be wired to Discord effector
	var mcpAddReaction func(channelID, messageID, emoji string) error // Will be wired to Discord effector
	var mcpSendFile func(channelID, filePath, message string) error   // Will be wired to Discord effector

	// Initialize GK process pool if GK_PATH is configured.
	// GK_PATH should point to the gk project directory (e.g. ~/src/gk).
	var gkPool *mcp.GKPool
	if gkPath := os.Getenv("GK_PATH"); gkPath != "" {
		gkPool = mcp.NewGKPool(gkPath, statePath)
		defer gkPool.Close()
		log.Printf("[main] GK pool initialized (path=%s)", gkPath)
		// Register GK as the MCP resource provider so resources/list and resources/read
		// are transparently proxied through the HTTP MCP server with session-domain routing.
		mcpServer.RegisterResourceProvider(
			func(domain string) ([]mcp.ResourceInfo, error) {
				return gkPool.ListResources(domain)
			},
			func(domain, uri string) (string, error) {
				return gkPool.ReadResource(domain, uri)
			},
		)
	}

	// Load extension registry from system (state-defaults) and user (statePath) extension dirs.
	// Extensions missing from either dir are silently skipped. A failed load is non-fatal.
	var extensionRegistry *extensions.Registry
	{
		sysExtDir := filepath.Join(paths.DefaultsDir, "system", "extensions")
		userExtDir := filepath.Join(statePath, "system", "extensions")
		reg, regErr := extensions.LoadAll(sysExtDir, userExtDir)
		if regErr != nil {
			log.Printf("[main] Warning: failed to load extension registry: %v", regErr)
		} else {
			extensionRegistry = reg
			log.Printf("[main] Extension registry loaded: %d extension(s)", reg.Len())
		}
	}

	// Initialize the Dispatcher to fire extension behaviors (schedule, slash_command, pattern_match, etc).
	// Must be declared here so processPercept (closure) and slash command registration can capture it.
	var dispatcher *extensions.Dispatcher
	if extensionRegistry != nil {
		runner := &extWorkflowRunner{engine: reflexEngine, registry: extensionRegistry}
		eventBus := extensions.NewEventBus()
		dispatcher = extensions.NewDispatcher(extensionRegistry, eventBus, runner)
		dispatcher.SetTalkToUser(&dispatcherTalker{
			send: func(msg string) error {
				if mcpSendMessage != nil {
					return mcpSendMessage(discordChannel, msg)
				}
				return nil
			},
		})
		dispatcher.SetSaveThought(&dispatcherLogger{
			log: func(msg string) {
				activityLog.LogAction(msg, "dispatcher", "", "")
			},
		})
		dispatcher.RegisterAll(context.Background())
		log.Printf("[main] Dispatcher registered behaviors for %d extension(s)", extensionRegistry.Len())
	}

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
		MemoryJudge:    memoryJudge,
		CalendarClient: calendarClient,
		GitHubClient:   githubClient,
		VMControlURL:      os.Getenv("VM_CONTROL_URL"), // defaults to http://127.0.0.1:3099 in vm_browser.go
		ExtensionRegistry: extensionRegistry,
		GKCallTool: func() func(domain, toolName string, args map[string]any) (string, error) {
			if gkPool == nil {
				return nil
			}
			return func(domain, toolName string, args map[string]any) (string, error) {
				return gkPool.CallTool(domain, toolName, args)
			}
		}(),
		ReadResource: func() func(domain, uri string) (string, error) {
			if gkPool == nil {
				return nil
			}
			return func(domain, uri string) (string, error) {
				return gkPool.ReadResource(domain, uri)
			}
		}(),
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
		SendFile: func(channelID, filePath, message string) error {
			if mcpSendFile != nil {
				return mcpSendFile(channelID, filePath, message)
			}
			return fmt.Errorf("Discord effector not yet initialized")
		},
		MCPBaseURL: fmt.Sprintf("http://127.0.0.1:%s", mcpHTTPPort),
		RegisterSession: func(token, agentID, domain string) {
			mcpServer.RegisterSession(token, agentID, domain)
		},
		AddThought: nil, // Will be set after processInboxMessage is defined
		OnMCPToolCall: func(toolName string) {
			if exec != nil {
				exec.GetMCPToolCallback()(toolName)
			}
		},
		SpawnSubagent: func(task, systemPromptAppend, profile, workflowInstanceID, workflowStep, mcpURL string) (string, string, error) {
			if exec == nil {
				return "", "", fmt.Errorf("executive not yet initialized")
			}
			spawnFn, _, _, _, _, _, _, _, _ := exec.SubagentCallbacks()
			id, logPath, err := spawnFn(task, systemPromptAppend, profile, workflowInstanceID, workflowStep, mcpURL)
			if err == nil && logPath != "" {
				go termManager.OpenSubagentWindow(id, logPath)
			}
			return id, logPath, err
		},
		ListSubagents: func() []map[string]any {
			if exec == nil {
				return nil
			}
			_, listFn, _, _, _, _, _, _, _ := exec.SubagentCallbacks()
			return listFn()
		},
		AnswerSubagent: func(sessionID, answer string) error {
			if exec == nil {
				return fmt.Errorf("executive not yet initialized")
			}
			_, _, answerFn, _, _, _, _, _, _ := exec.SubagentCallbacks()
			return answerFn(sessionID, answer)
		},
		GetSubagentStatus: func(sessionID string) (string, string, string, string, error) {
			if exec == nil {
				return "", "", "", "", fmt.Errorf("executive not yet initialized")
			}
			_, _, _, statusFn, _, _, _, _, _ := exec.SubagentCallbacks()
			return statusFn(sessionID)
		},
		StopSubagent: func(sessionID string) error {
			if exec == nil {
				return fmt.Errorf("executive not yet initialized")
			}
			_, _, _, _, stopFn, _, _, _, _ := exec.SubagentCallbacks()
			return stopFn(sessionID)
		},
		GetSubagentLog: func(sessionID string, lastN int) ([]map[string]any, error) {
			if exec == nil {
				return nil, fmt.Errorf("executive not yet initialized")
			}
			_, _, _, _, _, getLogFn, _, _, _ := exec.SubagentCallbacks()
			return getLogFn(sessionID, lastN)
		},
		DrainSubagentMemories: func(sessionID string) ([]string, error) {
			if exec == nil {
				return nil, fmt.Errorf("executive not yet initialized")
			}
			_, _, _, _, _, _, drainFn, _, _ := exec.SubagentCallbacks()
			return drainFn(sessionID)
		},
		PeekSubagentMemories: func(sessionID string) int {
			if exec == nil {
				return 0
			}
			_, _, _, _, _, _, _, peekFn, _ := exec.SubagentCallbacks()
			return peekFn(sessionID)
		},
		ListSubagentMemories: func(sessionID string) []string {
			if exec == nil {
				return nil
			}
			_, _, _, _, _, _, _, _, listMemsFn := exec.SubagentCallbacks()
			return listMemsFn(sessionID)
		},
		ListJobs: func(project string) ([]any, error) {
			listings, err := executive.ListJobs(statePath, project)
			if err != nil {
				return nil, err
			}
			result := make([]any, len(listings))
			for i, l := range listings {
				result[i] = l
			}
			return result, nil
		},
	}

	// Register all MCP tools
	tools.RegisterAll(mcpServer, mcpDeps)
	log.Printf("[main] MCP server initialized with %d tools", mcpServer.ToolCount())

	// Start stdio MCP proxy servers from state/system/mcp.json and register their tools
	// This makes things-mcp (and any other stdio servers) available to both
	// Claude sessions (via HTTP) and the reflex engine (via call_tool action).
	mcpConfigPath := filepath.Join(statePath, "system", "mcp.json")
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
		statePath,    // Executive will construct paths like state/system/core.md from this
		executive.ExecutiveV2Config{
			Provider:                     executiveProvider,
			AgentProvider:                agentProvider,
			ProviderName:                 providerName,
			ProviderConfig:               budCfg,
			Model:                        claudeModel,
			WorkDir:                      statePath, // Run Claude from state/ directory
			MCPServerURL:                 fmt.Sprintf("http://127.0.0.1:%s/mcp", mcpHTTPPort),
			BotAuthor:                    "Bud", // Kept for compatibility, but no longer used
			SessionTracker:               sessionTracker,
			WakeupInstructions:           wakeupInstructions,
			StartupInstructions:          startupInstructions,
			DefaultChannelID:             discordChannel,
			MaxAutonomousSessionDuration: autonomousSessionCap,
			ExtensionRegistry:            extensionRegistry,
			SendMessageFallback: func(channelID, message string) error {
				if fallbackSendMessage != nil {
					return fallbackSendMessage(channelID, message)
				}
				log.Printf("[fallback] ERROR: discordEffector not yet initialized")
				return fmt.Errorf("effector not initialized")
			},
			OnExecWake: func(focusID, context, existingClaudeSessionID string) {
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
	// Set known MCP tool names so tool_grants wildcards (e.g. mcp__bud2__gk_*)
	// can be expanded when loading agent definitions from plugins.
	{
		raw := mcpServer.ToolNames()
		prefixed := make([]string, len(raw))
		for i, name := range raw {
			prefixed[i] = "mcp__bud2__" + name
		}
		exec.SetKnownMCPTools(prefixed)
	}

	// Wire PostToolUse lifecycle hooks into the MCP server so hook scripts
	// receive the actual tool output (not fired from the observer path).
	mcpServer.SetPostToolHook(exec.PostToolUseHook())

	if err := exec.Start(); err != nil {
		log.Fatalf("Failed to start executive: %v", err)
	}
	log.Printf("[main] Executive started (active_sessions=%v, thinking_min_today=%.1f)",
		exec.HasActiveSessions(), exec.TodayThinkingMinutes())

	// Open the single persistent executive log pane exactly once at startup.
	// All wakes append to the same file; context clears do not open a new pane.
	go termManager.EnsureExecWindow(filepath.Join(paths.LogDir(), "exec", "executive.log"))

	// Clean up old terminal panes/windows periodically.
	termManager.StartCleanupLoop(2*time.Hour, 24*time.Hour)

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

						// Feed ratings back to engram to close the quality feedback loop
						if engramClient != nil {
							if err := engramClient.RateEngrams(resolved); err != nil {
								log.Printf("[memory_eval] Failed to rate engrams: %v", err)
							}
						}
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
			// Also terminate the current subprocess cleanly (same as signal_done).
			// Without this, the session runs until the 30-minute timeout after a reset.
			exec.SignalDone()
		}
	}

	// Track last Engram episode ID per channel for FOLLOWS edges
	var lastEpisodeMu sync.Mutex
	lastEngramIDByChannel := make(map[string]string) // channel → Engram episode ID (ep-{uuid})

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
		var attachments []map[string]any
		if msg.Extra != nil {
			if r, ok := msg.Extra["reply_to"].(string); ok {
				replyTo = r
			}
			if attsRaw, ok := msg.Extra["attachments"]; ok {
				if atts, ok := attsRaw.([]map[string]any); ok {
					attachments = atts
				} else if attsIface, ok := attsRaw.([]interface{}); ok {
					for _, a := range attsIface {
						if m, ok := a.(map[string]any); ok {
							attachments = append(attachments, m)
						}
					}
				}
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
			Attachments:    attachments,
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
			if profiling.Get().ShouldProfile(profiling.LevelDetailed) {
				defer profiling.Get().Start(perceptID, "percept.reflex_check")()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			handled, results = reflexEngine.Process(ctx, percept.Source, percept.Type, content, percept.Data)
		}()

		// Also fire extension pattern_match triggers via the Dispatcher.
		if dispatcher != nil {
			dispatcher.FirePercept(percept.Source, percept.Type, content, percept.Data)
		}

		// If a reflex escalated, forward its accumulated context into percept.Data
		// so the executive receives pre-fetched vars rather than starting blind.
		for _, r := range results {
			if r.Escalate {
				if percept.Data == nil {
					percept.Data = make(map[string]any)
				}
				percept.Data["_reflex_escalated"] = true
				percept.Data["_reflex_name"] = r.ReflexName
				percept.Data["_reflex_step"] = r.EscalateStep
				if r.EscalateMessage != "" {
					percept.Data["_escalate_message"] = r.EscalateMessage
				}
				if len(r.EscalateVars) > 0 {
					percept.Data["_escalate_vars"] = r.EscalateVars
				}
				break
			}
		}

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
					activityLog.LogDecision("Skip autonomous work", reason, "budget check", "blocked")
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
		// Kill the Claude subprocess immediately when signal_done fires so it
		// doesn't keep running up to the 30-minute hard timeout.
		if signalType == "done" && exec != nil {
			exec.SignalDone()
		}
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
		connected, disconnectCount, lastConnected, lastDisconnected := discordSense.ConnectionHealth()
		activityLog.LogError(
			fmt.Sprintf("Discord disconnected for %v, triggering hard reset", duration.Round(time.Second)),
			fmt.Errorf("prolonged disconnection"),
			map[string]any{
				"duration":          duration.String(),
				"connected":         connected,
				"disconnect_count":  disconnectCount,
				"last_connected":    lastConnected,
				"last_disconnected": lastDisconnected,
			},
		)
	})
	discordSense.StartHealthMonitor()
	log.Printf("[discord] initial connected=%v", discordSense.IsConnected())

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

	// Wire MCP send_image to effector directly (bypasses outbox file)
	mcpSendFile = func(channelID, filePath, message string) error {
		log.Printf("[mcp] Sending file %s", filePath)
		action := &types.Action{
			ID:       fmt.Sprintf("mcp-file-%d", time.Now().UnixNano()),
			Type:     "send_file",
			Effector: "discord",
			Payload: map[string]any{
				"channel_id": channelID,
				"file_path":  filePath,
				"message":    message,
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

	// Wire /stop to kill whatever session is currently running
	discordSense.SetOnStop(exec.InterruptCurrentSession)

	// Wire /debug-executive to toggle the live debug stream
	dbg := newExecutiveDebugger(exec, discordSense.Session())
	discordSense.SetOnDebugExecutive(dbg.Toggle)

	// Collect slash commands from extension reflexes and register with Discord.
	extReflexCmds := reflexEngine.ListSlashCommands()
	extDiscordCmds := make([]senses.SlashCommandInfo, len(extReflexCmds))
	for i, rc := range extReflexCmds {
		extDiscordCmds[i] = senses.SlashCommandInfo{
			Command:     rc.Command,
			Description: rc.Description,
		}
	}
	if dispatcher != nil {
		for _, dc := range dispatcher.ListSlashCommands() {
			extDiscordCmds = append(extDiscordCmds, senses.SlashCommandInfo{
				Command:     dc.Command,
				Description: dc.Description,
			})
		}
	}
	if err := discordSense.RegisterSlashCommands(discordGuildID, extDiscordCmds); err != nil {
		log.Printf("Warning: failed to register slash commands: %v", err)
	}

	log.Println("[main] Discord sense and effector started")

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

	// Inject startup impulse so the executive runs startup housekeeping.
	go func() {
		time.Sleep(3 * time.Second) // Let executive initialize
		processInboxMessage(&memory.InboxMessage{
			ID:        "startup-" + time.Now().Format("20060102T150405"),
			Type:      "impulse",
			Subtype:   "system",
			Content:   "impulse:startup",
			Priority:  3, // P3ActiveWork
			Timestamp: time.Now(),
			Status:    "pending",
		})
	}()

	// Start calendar sense (optional, independent of Discord)
	var calendarSense *senses.CalendarSense
	if calendarClient != nil {
		calendarSense = senses.NewCalendarSense(senses.CalendarConfig{
			Client:    calendarClient,
			Timezone:  userTimezone,
			StatePath: filepath.Join(statePath, "system", "calendar_state.json"),
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

	// Start MCP HTTP server (for Claude Code integration).
	// Bind the port synchronously so it is available before the P1 handler goroutine
	// starts — a queued message could otherwise cause Claude to connect before the
	// server is listening, producing intermittent "MCP not available" errors.
	{
		addr := "127.0.0.1:" + mcpHTTPPort
		mcpListener, err := mcpServer.Listen(addr)
		if err != nil {
			log.Fatalf("[main] Failed to bind MCP HTTP port: %v", err)
		}
		go func() {
			if err := mcpServer.Serve(mcpListener); err != nil {
				log.Printf("[main] MCP HTTP server error: %v", err)
			}
		}()
	}

	// P1 goroutine: event-driven, fires immediately on user message arrival.
	// Drains all P0/P1 items after each notification, serializing user sessions.
	go func() {
		notifyCh := exec.GetQueue().NotifyChannel()
		for {
			select {
			case <-stopChan:
				return
			case <-notifyCh:
				exec.RequestBackgroundInterrupt() // interrupt any running background session
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

	// Start autonomous wake-up goroutine (periodic self-initiated work).
	// Uses adaptive intervals: active mode (base interval) when the user has been
	// present recently, quiet mode (2× interval) when idle for 4+ hours.
	if autonomousEnabled {
		log.Printf("[main] Autonomous mode enabled (base interval: %v)", autonomousInterval)
		go func() {
			time.Sleep(10 * time.Second)

			for {
				// Adaptive interval: double base if no user input for 4+ hours.
				interval := autonomousInterval
				lastInput := activityLog.LastUserInputTime()
				if !lastInput.IsZero() && time.Since(lastInput) > 4*time.Hour {
					interval = autonomousInterval * 2
					log.Printf("[autonomous] Quiet mode (last input %v ago) — next wake in %v", time.Since(lastInput).Round(time.Minute), interval)
				}

				timer := time.NewTimer(interval)
				select {
				case <-stopChan:
					timer.Stop()
					return

				case <-timer.C:
					// Idle gate: skip if user was active too recently or a P1 session is running.
					lastInput := activityLog.LastUserInputTime()
					if !lastInput.IsZero() && time.Since(lastInput) < autonomousIdleRequired {
						log.Printf("[autonomous] User active %v ago (< %v required) — skipping wake",
							time.Since(lastInput).Round(time.Second), autonomousIdleRequired)
						continue
					}
					if exec.IsP1Active() {
						log.Printf("[autonomous] P1 session active — skipping wake")
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
							"trigger":              "periodic",
							"last_user_session_ts": lastInput.Format(time.RFC3339),
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

	// Activation decay is handled automatically by Engram.

	// Periodic state sync: DISABLED — causes pathological git processes when large files
	// (memory.db, wasm blobs, etc.) are tracked. Re-enable after gitignore cleanup.
	// See: state-sync goroutine was running git add -A && git push every hour.
	_ = statePath // suppress unused warning
	log.Printf("[main] State sync disabled (git bloat prevention)")

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

