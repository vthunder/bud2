package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/types"
)

func main() {
	// Log to stderr so stdout is clean for JSON-RPC
	log.SetOutput(os.Stderr)
	log.SetPrefix("[bud-mcp] ")

	log.Println("Starting bud2 MCP server...")

	// Get state path from environment
	statePath := os.Getenv("BUD_STATE_PATH")
	if statePath == "" {
		statePath = "state"
	}

	outboxPath := filepath.Join(statePath, "outbox.jsonl")
	log.Printf("Outbox path: %s", outboxPath)

	// Create MCP server
	server := mcp.NewServer()

	// Register talk_to_user tool
	server.RegisterTool("talk_to_user", func(ctx any, args map[string]any) (string, error) {
		message, ok := args["message"].(string)
		if !ok {
			return "", fmt.Errorf("message is required")
		}

		channelID, ok := args["channel_id"].(string)
		if !ok {
			return "", fmt.Errorf("channel_id is required")
		}

		log.Printf("talk_to_user: channel=%s message=%s", channelID, truncate(message, 50))

		// Write action to outbox
		action := map[string]any{
			"id":       fmt.Sprintf("action-%d", time.Now().UnixNano()),
			"effector": "discord",
			"type":     "send_message",
			"payload": map[string]any{
				"channel_id": channelID,
				"content":    message,
			},
			"status":     "pending",
			"created_at": time.Now().Format(time.RFC3339),
		}

		// Append to outbox file
		f, err := os.OpenFile(outboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open outbox: %w", err)
		}
		defer f.Close()

		data, err := json.Marshal(action)
		if err != nil {
			return "", fmt.Errorf("failed to marshal action: %w", err)
		}

		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write to outbox: %w", err)
		}

		log.Printf("Action queued: %s", action["id"])
		return "Message queued for sending to Discord", nil
	})

	tracesPath := filepath.Join(statePath, "traces.json")

	// Register list_traces tool (for discovering trace IDs)
	server.RegisterTool("list_traces", func(ctx any, args map[string]any) (string, error) {
		traces, err := loadTraces(tracesPath)
		if err != nil {
			return "", fmt.Errorf("failed to load traces: %w", err)
		}

		// Build summary
		var result []map[string]any
		for _, t := range traces {
			result = append(result, map[string]any{
				"id":       t.ID,
				"content":  truncate(t.Content, 100),
				"is_core":  t.IsCore,
				"strength": t.Strength,
			})
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// Register mark_core tool
	server.RegisterTool("mark_core", func(ctx any, args map[string]any) (string, error) {
		traceID, ok := args["trace_id"].(string)
		if !ok {
			return "", fmt.Errorf("trace_id is required")
		}

		isCore := true // default to marking as core
		if val, ok := args["is_core"].(bool); ok {
			isCore = val
		}

		traces, err := loadTraces(tracesPath)
		if err != nil {
			return "", fmt.Errorf("failed to load traces: %w", err)
		}

		// Find and update the trace
		found := false
		for _, t := range traces {
			if t.ID == traceID {
				t.IsCore = isCore
				found = true
				break
			}
		}

		if !found {
			return "", fmt.Errorf("trace %s not found", traceID)
		}

		// Save back
		if err := saveTraces(tracesPath, traces); err != nil {
			return "", fmt.Errorf("failed to save traces: %w", err)
		}

		action := "marked as core"
		if !isCore {
			action = "unmarked as core"
		}
		log.Printf("Trace %s %s", traceID, action)
		return fmt.Sprintf("Trace %s %s. Changes will take effect on next bud restart.", traceID, action), nil
	})

	// Register save_thought tool (save a thought to memory via inbox)
	server.RegisterTool("save_thought", func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		// Write thought to inbox as a special message type
		thought := map[string]any{
			"id":        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
			"type":      "thought",
			"content":   content,
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
		}

		// Append to inbox file
		f, err := os.OpenFile(filepath.Join(statePath, "inbox.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open inbox: %w", err)
		}
		defer f.Close()

		data, err := json.Marshal(thought)
		if err != nil {
			return "", fmt.Errorf("failed to marshal thought: %w", err)
		}

		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write thought: %w", err)
		}

		log.Printf("Saved thought: %s", truncate(content, 50))
		return "Thought saved to memory. It will be consolidated with other memories over time.", nil
	})

	// Register create_core tool (create a new core trace directly)
	server.RegisterTool("create_core", func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		traces, err := loadTraces(tracesPath)
		if err != nil {
			// If file doesn't exist, start with empty list
			traces = []*types.Trace{}
		}

		// Create new core trace
		trace := &types.Trace{
			ID:         fmt.Sprintf("core-%d", time.Now().UnixNano()),
			Content:    content,
			Activation: 1.0,
			Strength:   100,
			IsCore:     true,
			CreatedAt:  time.Now(),
			LastAccess: time.Now(),
		}
		traces = append(traces, trace)

		// Save
		if err := saveTraces(tracesPath, traces); err != nil {
			return "", fmt.Errorf("failed to save traces: %w", err)
		}

		log.Printf("Created core trace: %s", truncate(content, 50))
		return fmt.Sprintf("Created core trace %s. Changes will take effect on next bud restart.", trace.ID), nil
	})

	// Run server
	if err := server.Run(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// loadTraces reads traces from the JSON file
func loadTraces(path string) ([]*types.Trace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var file struct {
		Traces []*types.Trace `json:"traces"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}

	return file.Traces, nil
}

// saveTraces writes traces to the JSON file
func saveTraces(path string, traces []*types.Trace) error {
	file := struct {
		Traces []*types.Trace `json:"traces"`
	}{
		Traces: traces,
	}

	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
