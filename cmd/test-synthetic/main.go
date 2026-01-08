package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Scenario defines a test scenario
type Scenario struct {
	Name          string         `yaml:"name"`
	Description   string         `yaml:"description"`
	Conversations []Conversation `yaml:"conversations"`
}

// Conversation defines a sequence of messages
type Conversation struct {
	Name       string   `yaml:"name"`
	NewSession bool     `yaml:"new_session"` // restart bud before this conversation
	Messages   []string `yaml:"messages"`
	Expect     []Expect `yaml:"expect"` // expectations for the last response
}

// Expect defines an expectation for a response
type Expect struct {
	Contains    string   `yaml:"contains"`
	ContainsAny []string `yaml:"contains_any"`
	NotContains string   `yaml:"not_contains"`
}

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

var (
	statePath        string
	budProcess       *exec.Cmd
	lastOutboxOffset int64
	verbose          bool
)

func main() {
	// Parse flags
	scenarioPath := flag.String("scenario", "", "Path to scenario YAML file")
	scenarioDir := flag.String("dir", "tests/scenarios", "Directory containing scenario files")
	listScenarios := flag.Bool("list", false, "List available scenarios")
	runAll := flag.Bool("all", false, "Run all scenarios")
	flag.BoolVar(&verbose, "v", false, "Verbose output")
	flag.Parse()

	// Handle list
	if *listScenarios {
		scenarios, _ := filepath.Glob(filepath.Join(*scenarioDir, "*.yaml"))
		fmt.Println("Available scenarios:")
		for _, s := range scenarios {
			scenario, err := loadScenario(s)
			if err != nil {
				continue
			}
			fmt.Printf("  %s - %s\n", scenario.Name, scenario.Description)
		}
		return
	}

	// Handle run all
	if *runAll {
		scenarios, _ := filepath.Glob(filepath.Join(*scenarioDir, "*.yaml"))
		results := make(map[string]bool)
		for _, s := range scenarios {
			scenario, err := loadScenario(s)
			if err != nil {
				log.Printf("Failed to load %s: %v", s, err)
				continue
			}
			success := runScenario(scenario)
			results[scenario.Name] = success
		}

		// Summary
		fmt.Println("\n=== Summary ===")
		passed, failed := 0, 0
		for name, success := range results {
			if success {
				fmt.Printf("  ✓ %s\n", name)
				passed++
			} else {
				fmt.Printf("  ✗ %s\n", name)
				failed++
			}
		}
		fmt.Printf("\nPassed: %d, Failed: %d\n", passed, failed)
		if failed > 0 {
			os.Exit(1)
		}
		return
	}

	// Handle single scenario
	if *scenarioPath == "" {
		// Default to short-recall for backwards compatibility
		*scenarioPath = filepath.Join(*scenarioDir, "short-recall.yaml")
	}

	scenario, err := loadScenario(*scenarioPath)
	if err != nil {
		log.Fatalf("Failed to load scenario: %v", err)
	}

	if !runScenario(scenario) {
		os.Exit(1)
	}
}

func loadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var scenario Scenario
	if err := yaml.Unmarshal(data, &scenario); err != nil {
		return nil, err
	}

	return &scenario, nil
}

func runScenario(scenario *Scenario) bool {
	log.Printf("=== Scenario: %s ===", scenario.Name)
	log.Printf("Description: %s", scenario.Description)
	log.Println("")

	// Setup test environment
	statePath = fmt.Sprintf("/tmp/bud2-test-%s", scenario.Name)
	os.RemoveAll(statePath)
	os.MkdirAll(statePath, 0755)
	os.MkdirAll(filepath.Join(statePath, "notes"), 0755)

	// Copy core seed
	seedSrc := "state/core_seed.md"
	seedDst := filepath.Join(statePath, "core_seed.md")
	if seedData, err := os.ReadFile(seedSrc); err == nil {
		os.WriteFile(seedDst, seedData, 0644)
		if verbose {
			log.Printf("Copied core seed to %s", seedDst)
		}
	}

	// Reset state
	lastOutboxOffset = 0

	// Start bud
	if err := startBud(); err != nil {
		log.Fatalf("Failed to start bud: %v", err)
	}
	defer stopBud()

	time.Sleep(2 * time.Second)

	allPassed := true

	// Run each conversation
	for i, conv := range scenario.Conversations {
		log.Printf("\n--- %s ---\n", conv.Name)

		// Handle new session
		if conv.NewSession && i > 0 {
			log.Println("Restarting bud for new session...")
			stopBud()
			time.Sleep(1 * time.Second)

			// Clear per-session state but keep memory
			os.Remove(filepath.Join(statePath, "threads.json"))
			os.Remove(filepath.Join(statePath, "queues", "inbox.jsonl"))
			lastOutboxOffset = 0

			if err := startBud(); err != nil {
				log.Fatalf("Failed to restart bud: %v", err)
			}
			time.Sleep(2 * time.Second)
		}

		// Send messages and collect last response
		var lastResponse string
		for _, msg := range conv.Messages {
			sendMessage(msg)
			resp := waitForResponse(60 * time.Second)
			lastResponse = resp
			if verbose {
				log.Printf("[bud] %s", truncate(resp, 200))
			} else {
				log.Printf("[bud] %s", truncate(resp, 100))
			}
			// Wait for Claude to be idle before sending next message
			waitForClaudeIdle(10 * time.Second)
		}

		// Check expectations
		if len(conv.Expect) > 0 {
			passed := checkExpectations(lastResponse, conv.Expect)
			if passed {
				log.Printf("✓ Expectations passed")
			} else {
				log.Printf("✗ Expectations failed")
				allPassed = false
			}
		}
	}

	// Show traces
	if verbose {
		log.Println("\n--- Traces ---")
		showTraces()
	}

	return allPassed
}

func checkExpectations(response string, expects []Expect) bool {
	responseLower := strings.ToLower(response)
	allPassed := true

	for _, exp := range expects {
		if exp.Contains != "" {
			if !strings.Contains(responseLower, strings.ToLower(exp.Contains)) {
				log.Printf("  ✗ Expected to contain: %q", exp.Contains)
				allPassed = false
			} else if verbose {
				log.Printf("  ✓ Contains: %q", exp.Contains)
			}
		}

		if len(exp.ContainsAny) > 0 {
			found := false
			for _, s := range exp.ContainsAny {
				if strings.Contains(responseLower, strings.ToLower(s)) {
					found = true
					if verbose {
						log.Printf("  ✓ Contains one of: %q", s)
					}
					break
				}
			}
			if !found {
				log.Printf("  ✗ Expected to contain one of: %v", exp.ContainsAny)
				allPassed = false
			}
		}

		if exp.NotContains != "" {
			if strings.Contains(responseLower, strings.ToLower(exp.NotContains)) {
				log.Printf("  ✗ Expected NOT to contain: %q", exp.NotContains)
				allPassed = false
			} else if verbose {
				log.Printf("  ✓ Does not contain: %q", exp.NotContains)
			}
		}
	}

	return allPassed
}

func startBud() error {
	budProcess = exec.Command("./bin/bud")
	budProcess.Env = append(os.Environ(),
		"SYNTHETIC_MODE=true",
		fmt.Sprintf("STATE_PATH=%s", statePath),
	)
	if verbose {
		budProcess.Stdout = os.Stdout
		budProcess.Stderr = os.Stderr
	} else {
		// Suppress output in non-verbose mode
		budProcess.Stdout = io.Discard
		budProcess.Stderr = io.Discard
	}

	if err := budProcess.Start(); err != nil {
		return err
	}

	if verbose {
		log.Printf("Started bud (PID %d)", budProcess.Process.Pid)
	}
	return nil
}

func stopBud() {
	if budProcess != nil && budProcess.Process != nil {
		budProcess.Process.Signal(os.Interrupt)
		budProcess.Wait()
		if verbose {
			log.Println("Stopped bud")
		}
	}
}

func sendMessage(content string) {
	inboxPath := filepath.Join(statePath, "queues", "inbox.jsonl")

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

	// Truncate long messages in log
	displayContent := content
	if len(displayContent) > 80 {
		displayContent = displayContent[:80] + "..."
	}
	log.Printf("[user] %s", displayContent)
}

func waitForResponse(timeout time.Duration) string {
	outboxPath := filepath.Join(statePath, "queues", "outbox.jsonl")
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		f, err := os.Open(outboxPath)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

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
					newOffset, _ := f.Seek(0, io.SeekCurrent)
					lastOutboxOffset = newOffset
					f.Close()
					return content
				}
			}
		}

		newOffset, _ := f.Seek(0, io.SeekCurrent)
		lastOutboxOffset = newOffset
		f.Close()

		time.Sleep(500 * time.Millisecond)
	}

	return "(no response)"
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
			IsCore   bool   `json:"is_core"`
		} `json:"traces"`
	}

	if err := json.Unmarshal(data, &tracesFile); err != nil {
		log.Printf("Failed to parse traces: %v", err)
		return
	}

	log.Printf("Found %d traces:", len(tracesFile.Traces))
	for _, t := range tracesFile.Traces {
		coreMarker := ""
		if t.IsCore {
			coreMarker = " [core]"
		}
		log.Printf("  [strength=%d%s] %s", t.Strength, coreMarker, truncate(t.Content, 80))
	}
}

func truncate(s string, maxLen int) string {
	// Replace newlines for cleaner output
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// waitForClaudeIdle waits for Claude to show the prompt (not "Doing...")
func waitForClaudeIdle(timeout time.Duration) {
	deadline := time.Now().Add(timeout)

	// Get tmux window names
	cmd := exec.Command("tmux", "list-windows", "-t", "bud2", "-F", "#{window_name}")
	output, err := cmd.Output()
	if err != nil {
		// No tmux session, just use a small delay
		time.Sleep(2 * time.Second)
		return
	}

	// Find thread windows
	var threadWindows []string
	for _, name := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.HasPrefix(name, "thread-") {
			threadWindows = append(threadWindows, name)
		}
	}

	if len(threadWindows) == 0 {
		time.Sleep(2 * time.Second)
		return
	}

	// Wait for all thread windows to show the prompt (not busy)
	for time.Now().Before(deadline) {
		allIdle := true
		for _, window := range threadWindows {
			target := fmt.Sprintf("bud2:%s", window)
			cmd := exec.Command("tmux", "capture-pane", "-t", target, "-p", "-S", "-5")
			output, err := cmd.Output()
			if err != nil {
				continue
			}

			content := string(output)
			// Check if Claude is busy (showing "Doing..." status)
			if strings.Contains(content, "Doing...") || strings.Contains(content, "* Doing") {
				allIdle = false
				break
			}
		}

		if allIdle {
			// Small buffer to ensure Claude has fully finished
			time.Sleep(500 * time.Millisecond)
			return
		}

		time.Sleep(500 * time.Millisecond)
	}

	if verbose {
		log.Printf("Timeout waiting for Claude to be idle")
	}
}
