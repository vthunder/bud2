package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// InboxMessage matches memory.InboxMessage
type InboxMessage struct {
	ID        string         `json:"id"`
	Content   string         `json:"content"`
	ChannelID string         `json:"channel_id"`
	AuthorID  string         `json:"author_id,omitempty"`
	Author    string         `json:"author,omitempty"`
	Timestamp time.Time      `json:"timestamp,omitempty"`
	Status    string         `json:"status"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// Action matches types.Action
type Action struct {
	ID        string         `json:"id"`
	Effector  string         `json:"effector"`
	Type      string         `json:"type"`
	Payload   map[string]any `json:"payload"`
	Status    string         `json:"status"`
	Timestamp time.Time      `json:"timestamp"`
}

var statePath string
var budProcess *exec.Cmd
var lastOutboxOffset int64

func main() {
	log.Println("=== Synthetic Memory Test ===")
	log.Println("Tests memory consolidation and recall using inbox/outbox")
	log.Println("")

	// Use temp directory for test state
	statePath = "/tmp/bud2-synthetic-test"
	os.RemoveAll(statePath)
	os.MkdirAll(statePath, 0755)

	// Copy core seed file to test directory
	seedSrc := "state/core_seed.md"
	seedDst := filepath.Join(statePath, "core_seed.md")
	if seedData, err := os.ReadFile(seedSrc); err == nil {
		os.WriteFile(seedDst, seedData, 0644)
		log.Printf("Copied core seed to %s", seedDst)
	} else {
		log.Printf("Warning: could not copy core seed: %v", err)
	}

	log.Printf("State path: %s", statePath)

	// Start bud in synthetic mode
	if err := startBud(); err != nil {
		log.Fatalf("Failed to start bud: %v", err)
	}
	defer stopBud()

	// Wait for bud to initialize
	time.Sleep(2 * time.Second)

	log.Println("")
	log.Println("=== Conversation 1: Tell Bud a secret ===")
	log.Println("")

	sendMessage("Hi! I want to tell you something to remember.")
	resp1 := waitForResponse(30 * time.Second)
	log.Printf("[bud] %s", truncate(resp1, 100))

	sendMessage("The secret code word is 'pineapple submarine'. Remember that!")
	resp2 := waitForResponse(30 * time.Second)
	log.Printf("[bud] %s", truncate(resp2, 100))

	sendMessage("Great, I'll ask you about it later. Bye for now!")
	resp3 := waitForResponse(30 * time.Second)
	log.Printf("[bud] %s", truncate(resp3, 100))

	log.Println("")
	log.Println("=== Waiting for consolidation ===")
	log.Println("")

	// Backdate percepts to trigger consolidation (modify percepts.json timestamps)
	backdatePercepts()

	// Trigger consolidation by waiting for the consolidation ticker
	// Or we can restart bud to simulate a new session
	log.Println("Restarting bud to simulate new session...")
	stopBud()
	time.Sleep(1 * time.Second)

	// Clear threads to simulate fresh context (but keep traces)
	os.Remove(filepath.Join(statePath, "threads.json"))
	os.Remove(filepath.Join(statePath, "inbox.jsonl"))
	lastOutboxOffset = 0

	if err := startBud(); err != nil {
		log.Fatalf("Failed to restart bud: %v", err)
	}
	time.Sleep(2 * time.Second)

	log.Println("")
	log.Println("=== Conversation 2: Test recall ===")
	log.Println("")

	sendMessage("Do you remember the secret code word I told you earlier?")
	resp4 := waitForResponse(60 * time.Second)
	log.Printf("[bud] %s", resp4)

	log.Println("")
	log.Println("=== Results ===")
	log.Println("")

	if strings.Contains(strings.ToLower(resp4), "pineapple") {
		log.Println("✓ SUCCESS: Bud remembered 'pineapple submarine'!")
	} else {
		log.Println("✗ FAIL: Bud did not recall the secret code word")
	}

	// Show traces
	log.Println("")
	log.Println("=== Traces ===")
	showTraces()
}

func startBud() error {
	budProcess = exec.Command("./bin/bud")
	budProcess.Env = append(os.Environ(),
		"SYNTHETIC_MODE=true",
		fmt.Sprintf("STATE_PATH=%s", statePath),
	)
	budProcess.Stdout = os.Stdout
	budProcess.Stderr = os.Stderr

	if err := budProcess.Start(); err != nil {
		return err
	}

	log.Printf("Started bud (PID %d)", budProcess.Process.Pid)
	return nil
}

func stopBud() {
	if budProcess != nil && budProcess.Process != nil {
		budProcess.Process.Signal(os.Interrupt)
		budProcess.Wait()
		log.Println("Stopped bud")
	}
}

func sendMessage(content string) {
	inboxPath := filepath.Join(statePath, "inbox.jsonl")

	msg := InboxMessage{
		ID:        fmt.Sprintf("test-%d", time.Now().UnixNano()),
		Content:   content,
		ChannelID: "test-channel",
		Author:    "tester",
		Timestamp: time.Now(),
		Status:    "pending",
	}

	data, _ := json.Marshal(msg)

	f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Failed to write to inbox: %v", err)
		return
	}
	defer f.Close()

	f.Write(data)
	f.WriteString("\n")

	log.Printf("[user] %s", content)
}

func waitForResponse(timeout time.Duration) string {
	outboxPath := filepath.Join(statePath, "outbox.jsonl")
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Read new lines from outbox
		f, err := os.Open(outboxPath)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// Seek to where we left off
		if lastOutboxOffset > 0 {
			f.Seek(lastOutboxOffset, io.SeekStart)
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var action Action
			if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
				continue
			}

			if action.Type == "send_message" {
				if content, ok := action.Payload["content"].(string); ok {
					// Update offset
					newOffset, _ := f.Seek(0, io.SeekCurrent)
					lastOutboxOffset = newOffset
					f.Close()
					return content
				}
			}
		}

		// Update offset even if no message found
		newOffset, _ := f.Seek(0, io.SeekCurrent)
		lastOutboxOffset = newOffset
		f.Close()

		time.Sleep(500 * time.Millisecond)
	}

	return "(no response)"
}

func backdatePercepts() {
	perceptsPath := filepath.Join(statePath, "percepts.json")

	data, err := os.ReadFile(perceptsPath)
	if err != nil {
		log.Printf("Could not read percepts: %v", err)
		return
	}

	// For now, just log the percepts file size
	log.Printf("Percepts file: %d bytes", len(data))
	log.Println("(Consolidation will happen on restart)")
}

func showTraces() {
	tracesPath := filepath.Join(statePath, "traces.json")

	data, err := os.ReadFile(tracesPath)
	if err != nil {
		log.Printf("No traces found: %v", err)
		return
	}

	var tracesFile struct {
		Traces []struct {
			Content  string `json:"content"`
			Strength int    `json:"strength"`
		} `json:"traces"`
	}

	if err := json.Unmarshal(data, &tracesFile); err != nil {
		log.Printf("Failed to parse traces: %v", err)
		return
	}

	log.Printf("Found %d traces:", len(tracesFile.Traces))
	for _, t := range tracesFile.Traces {
		log.Printf("  [strength=%d] %s", t.Strength, truncate(t.Content, 80))
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
