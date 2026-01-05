package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/vthunder/bud2/internal/attention"
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

	if discordToken == "" {
		log.Fatal("DISCORD_TOKEN environment variable required")
	}

	// Ensure state directory exists
	os.MkdirAll(statePath, 0755)

	// Initialize memory pools
	perceptPool := memory.NewPerceptPool(filepath.Join(statePath, "percepts.json"))
	threadPool := memory.NewThreadPool(filepath.Join(statePath, "threads.json"))
	outbox := memory.NewOutbox(filepath.Join(statePath, "outbox.jsonl"))

	// Load persisted state
	if err := perceptPool.Load(); err != nil {
		log.Printf("Warning: failed to load percepts: %v", err)
	}
	if err := threadPool.Load(); err != nil {
		log.Printf("Warning: failed to load threads: %v", err)
	}
	if err := outbox.Load(); err != nil {
		log.Printf("Warning: failed to load outbox: %v", err)
	}

	// Initialize executive
	exec := executive.New(perceptPool, threadPool, outbox, executive.ExecutiveConfig{
		Model:          claudeModel,
		WorkDir:        ".",
		UseInteractive: useInteractive,
	})
	if err := exec.Start(); err != nil {
		log.Fatalf("Failed to start executive: %v", err)
	}

	// Initialize attention
	attn := attention.New(perceptPool, threadPool, func(thread *types.Thread) {
		log.Printf("[main] Active thread changed: %s - %s", thread.ID, thread.Goal)
		// Invoke executive to process the thread
		ctx := context.Background()
		if err := exec.ProcessThread(ctx, thread); err != nil {
			log.Printf("[main] Executive error: %v", err)
		}
	})

	// Initialize Discord sense
	discordSense, err := senses.NewDiscordSense(senses.DiscordConfig{
		Token:     discordToken,
		ChannelID: discordChannel,
		OwnerID:   discordOwner,
	}, func(percept *types.Percept) {
		// Add percept to pool
		perceptPool.Add(percept)

		// For now, auto-create a thread for each message
		// TODO: smarter thread assignment based on relevance
		content := percept.Data["content"].(string)
		goal := "respond to: " + truncate(content, 50)
		attn.CreateThread(goal, []string{percept.ID})
	})
	if err != nil {
		log.Fatalf("Failed to create Discord sense: %v", err)
	}

	// Start Discord sense
	if err := discordSense.Start(); err != nil {
		log.Fatalf("Failed to start Discord sense: %v", err)
	}

	// Initialize Discord effector (shares session with sense)
	discordEffector := effectors.NewDiscordEffector(
		discordSense.Session(),
		func() []*types.Action { return outbox.GetPending() },
		func(id string) { outbox.MarkComplete(id) },
	)
	discordEffector.Start()

	// Start attention
	attn.Start()

	log.Println("[main] All subsystems started. Press Ctrl+C to stop.")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("[main] Shutting down...")

	// Stop subsystems
	attn.Stop()
	discordEffector.Stop()
	discordSense.Stop()

	// Persist state
	if err := perceptPool.Save(); err != nil {
		log.Printf("Warning: failed to save percepts: %v", err)
	}
	if err := threadPool.Save(); err != nil {
		log.Printf("Warning: failed to save threads: %v", err)
	}
	if err := outbox.Save(); err != nil {
		log.Printf("Warning: failed to save outbox: %v", err)
	}

	log.Println("[main] Goodbye!")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
