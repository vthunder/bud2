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
	"github.com/vthunder/bud2/internal/attention"
	"github.com/vthunder/bud2/internal/budget"
	"github.com/vthunder/bud2/internal/effectors"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/memory"
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

	// Initialize session tracker and signal processor for thinking time budget
	sessionTracker := budget.NewSessionTracker(statePath)
	signalProcessor := budget.NewSignalProcessor(statePath, sessionTracker)
	thinkingBudget := budget.NewThinkingBudget(sessionTracker)
	thinkingBudget.DailyMinutes = dailyBudgetMinutes
	thinkingBudget.MinIntervalBetween = autonomousInterval

	// Start signal processor (polls signals.jsonl for session completions)
	signalProcessor.Start(500 * time.Millisecond)

	// Start CPU watcher as fallback for signal_done
	cpuWatcher := budget.NewCPUWatcher(sessionTracker)
	cpuWatcher.Start()

	log.Printf("[main] Session tracker initialized (daily budget: %.0f min)", dailyBudgetMinutes)

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

	// Initialize executive with trace retrieval functions
	exec = executive.New(perceptPool, threadPool, outbox, executive.ExecutiveConfig{
		Model:           claudeModel,
		WorkDir:         ".",
		UseInteractive:  useInteractive,
		GetActiveTraces: attn.GetActivatedTraces,
		GetCoreTraces:   attn.GetCoreTraces,
		SessionTracker:  sessionTracker,
	})
	if err := exec.Start(); err != nil {
		log.Fatalf("Failed to start executive: %v", err)
	}

	// Process percept helper - used by both inbox polling and response capture
	processPercept := func(percept *types.Percept) {
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
		discordEffector.Start()

		// Wire up typing indicator to executive
		exec.SetTypingCallbacks(discordEffector.StartTyping, discordEffector.StopTyping)

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
			}
		}
	}()

	// Start autonomous wake-up goroutine (periodic self-initiated work)
	if autonomousEnabled {
		log.Printf("[main] Autonomous mode enabled (interval: %v)", autonomousInterval)
		go func() {
			// Wait a bit before first autonomous check
			time.Sleep(30 * time.Second)

			ticker := time.NewTicker(autonomousInterval)
			defer ticker.Stop()

			for {
				select {
				case <-stopChan:
					return
				case <-ticker.C:
					// Check if we can do autonomous work
					if ok, reason := thinkingBudget.CanDoAutonomousWork(); !ok {
						log.Printf("[autonomous] Skipping wake-up: %s", reason)
						continue
					}

					// Log budget status
					thinkingBudget.LogStatus()

					// Create an autonomous impulse (internal motivation)
					impulse := &types.Impulse{
						ID:          fmt.Sprintf("impulse-wake-%d", time.Now().UnixNano()),
						Source:      types.ImpulseSystem,
						Type:        "wake",
						Intensity:   0.5, // moderate intensity
						Timestamp:   time.Now(),
						Description: "Periodic autonomous wake-up. Check for pending tasks, review commitments, or do background work.",
						Data: map[string]any{
							"trigger": "periodic",
						},
					}

					log.Printf("[autonomous] Triggering wake-up via impulse")
					thinkingBudget.RecordAutonomousCall()
					// Convert impulse to percept for attention routing
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
