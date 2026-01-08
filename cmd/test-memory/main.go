package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/attention"
	"github.com/vthunder/bud2/internal/executive"
	"github.com/vthunder/bud2/internal/memory"
	"github.com/vthunder/bud2/internal/types"
)

func main() {
	log.Println("=== Memory Integration Test ===")
	log.Println("This test exercises the full system including Claude")
	log.Println("")

	// Use temp directory for test state
	statePath := "/tmp/bud2-memory-test"
	os.RemoveAll(statePath)
	os.MkdirAll(statePath, 0755)
	systemPath := filepath.Join(statePath, "system")
	queuesPath := filepath.Join(systemPath, "queues")
	os.MkdirAll(queuesPath, 0755)

	// Initialize pools
	perceptPool := memory.NewPerceptPool(filepath.Join(queuesPath, "percepts.json"))
	threadPool := memory.NewThreadPool(filepath.Join(systemPath, "threads.json"))
	tracePool := memory.NewTracePool(filepath.Join(systemPath, "traces.json"))
	outbox := memory.NewOutbox(filepath.Join(queuesPath, "outbox.jsonl"))

	// Track responses from Claude
	var responses []string

	// Initialize attention with callback
	var attn *attention.Attention
	var exec *executive.Executive

	attn = attention.New(perceptPool, threadPool, tracePool, func(thread *types.Thread) {
		log.Printf("[callback] Processing thread: %s", thread.Goal)
		ctx := context.Background()
		if err := exec.ProcessThread(ctx, thread); err != nil {
			log.Printf("[callback] Error: %v", err)
		}
	})

	// Initialize executive (non-interactive mode for programmatic use)
	exec = executive.New(perceptPool, threadPool, outbox, executive.ExecutiveConfig{
		Model:           os.Getenv("CLAUDE_MODEL"),
		WorkDir:         ".",
		UseInteractive:  false, // Programmatic mode
		GetActiveTraces: attn.GetActivatedTraces,
	})
	if err := exec.Start(); err != nil {
		log.Fatalf("Failed to start executive: %v", err)
	}

	// Start attention loop
	attn.Start()

	// Helper to send a message and wait for response
	sendMessage := func(msg string) {
		percept := &types.Percept{
			ID:        fmt.Sprintf("test-msg-%d", time.Now().UnixNano()),
			Source:    "test",
			Type:      "message",
			Intensity: 0.5,
			Timestamp: time.Now(),
			Data: map[string]any{
				"content":    msg,
				"channel_id": "test-channel",
			},
		}
		perceptPool.Add(percept)
		attn.RoutePercept(percept, func(content string) string {
			return "respond to: " + truncate(content, 50)
		})
		log.Printf("[user] %s", msg)

		// Wait for response (poll outbox)
		time.Sleep(2 * time.Second) // Give Claude time to respond
		for i := 0; i < 30; i++ {   // Wait up to 30 seconds
			actions := outbox.GetPending()
			for _, a := range actions {
				if a.Type == "send_message" {
					if content, ok := a.Payload["content"].(string); ok {
						log.Printf("[bud] %s", truncate(content, 100))
						responses = append(responses, content)
						outbox.MarkComplete(a.ID)

						// Capture as percept for memory
						budPercept := &types.Percept{
							ID:        fmt.Sprintf("bud-response-%d", time.Now().UnixNano()),
							Source:    "bud",
							Type:      "response",
							Intensity: 0.3,
							Timestamp: time.Now(),
							Data: map[string]any{
								"content":    content,
								"channel_id": "test-channel",
							},
						}
						attn.EmbedPercept(budPercept)
						perceptPool.Add(budPercept)
						return
					}
				}
			}
			time.Sleep(1 * time.Second)
		}
		log.Println("[warning] No response received")
	}

	log.Println("")
	log.Println("=== Conversation 1: Discussing memory systems ===")
	log.Println("")

	sendMessage("Hi! I want to tell you about something so you can remember it later.")
	sendMessage("The secret code word is 'pineapple submarine'.")
	sendMessage("Remember that for later!")

	log.Println("")
	log.Println("=== Waiting for consolidation ===")
	log.Println("")

	// Backdate percepts to trigger consolidation
	for _, p := range perceptPool.All() {
		p.Timestamp = time.Now().Add(-2 * time.Minute)
	}
	n := attn.Consolidate()
	log.Printf("Consolidated %d percepts into %d traces", n, attn.TraceCount())

	log.Println("")
	log.Println("=== Conversation 2: Testing recall ===")
	log.Println("")

	// Clear the thread's ProcessedAt to force re-processing
	for _, t := range threadPool.All() {
		t.ProcessedAt = nil
		t.Status = types.StatusPaused
	}

	sendMessage("Do you remember the secret code word I told you earlier?")

	log.Println("")
	log.Println("=== Results ===")
	log.Println("")

	// Check if the response mentions the secret
	lastResponse := ""
	if len(responses) > 0 {
		lastResponse = responses[len(responses)-1]
	}

	if strings.Contains(strings.ToLower(lastResponse), "pineapple") {
		log.Println("✓ SUCCESS: Claude remembered 'pineapple submarine'!")
	} else {
		log.Println("✗ FAIL: Claude did not recall the secret code word")
		log.Println("")
		log.Println("Last response was:")
		log.Println(lastResponse)
	}

	log.Println("")
	log.Println("=== Trace Contents ===")
	for _, t := range tracePool.All() {
		log.Printf("  [strength=%d] %s", t.Strength, truncate(t.Content, 80))
	}

	// Cleanup
	attn.Stop()
	log.Println("")
	log.Println("=== Test Complete ===")

	// Interactive mode - let user chat
	if len(os.Args) > 1 && os.Args[1] == "--interactive" {
		log.Println("")
		log.Println("Entering interactive mode. Type messages, 'quit' to exit.")
		scanner := bufio.NewScanner(os.Stdin)
		for {
			fmt.Print("> ")
			if !scanner.Scan() {
				break
			}
			msg := scanner.Text()
			if msg == "quit" {
				break
			}
			attn.Start()
			sendMessage(msg)
			attn.Stop()
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
