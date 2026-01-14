package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/integrations/github"
	"github.com/vthunder/bud2/internal/integrations/notion"
	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
	"github.com/vthunder/bud2/internal/state"
	"github.com/vthunder/bud2/internal/types"
)

func main() {
	// Log to stderr so stdout is clean for JSON-RPC
	log.SetOutput(os.Stderr)
	log.SetPrefix("[bud-mcp] ")

	// Load .env file if present (don't error if missing)
	// Try multiple locations: cwd, then relative to executable
	if err := godotenv.Load(); err == nil {
		log.Println("Loaded .env file from cwd")
	} else {
		// Try to find .env relative to the executable (bin/bud-mcp -> .env)
		if exe, err := os.Executable(); err == nil {
			projectRoot := filepath.Dir(filepath.Dir(exe)) // bin/bud-mcp -> bin -> project
			envPath := filepath.Join(projectRoot, ".env")
			if err := godotenv.Load(envPath); err == nil {
				log.Printf("Loaded .env file from %s", envPath)
			}
		}
	}

	log.Println("Starting bud2 MCP server...")

	// Get state path from environment
	statePath := os.Getenv("BUD_STATE_PATH")
	if statePath == "" {
		statePath = "state"
	}

	systemPath := filepath.Join(statePath, "system")
	queuesPath := filepath.Join(systemPath, "queues")
	os.MkdirAll(queuesPath, 0755)
	outboxPath := filepath.Join(queuesPath, "outbox.jsonl")
	log.Printf("Outbox path: %s", outboxPath)

	// Open graph database for memory
	graphDB, err := graph.Open(systemPath)
	if err != nil {
		log.Fatalf("Failed to open graph database: %v", err)
	}
	defer graphDB.Close()
	log.Printf("Graph database opened at %s/memory.db", systemPath)

	// Create MCP server
	server := mcp.NewServer()

	// Get default channel from environment
	defaultChannel := os.Getenv("DISCORD_CHANNEL_ID")

	// Register talk_to_user tool
	server.RegisterTool("talk_to_user", mcp.ToolDef{
		Description: "Send a message to the user via Discord. Use this to respond to questions, share observations, ask clarifying questions, or give status updates.",
		Properties: map[string]mcp.PropDef{
			"message":    {Type: "string", Description: "The message to send to the user"},
			"channel_id": {Type: "string", Description: "The Discord channel ID to send to. Optional - if not provided, uses the default channel from DISCORD_CHANNEL_ID."},
		},
		Required: []string{"message"},
	}, func(ctx any, args map[string]any) (string, error) {
		message, ok := args["message"].(string)
		if !ok {
			return "", fmt.Errorf("message is required")
		}

		// Use provided channel_id or fall back to default
		channelID, _ := args["channel_id"].(string)
		if channelID == "" {
			channelID = defaultChannel
		}
		if channelID == "" {
			return "", fmt.Errorf("channel_id required (none provided and DISCORD_CHANNEL_ID not set)")
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

	// Register list_traces tool (for discovering trace IDs)
	server.RegisterTool("list_traces", mcp.ToolDef{
		Description: "List all memory traces with their IDs, content preview, and core status. Use this to discover trace IDs before marking them as core.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		traces, err := graphDB.GetAllTraces()
		if err != nil {
			return "", fmt.Errorf("failed to load traces: %w", err)
		}

		// Build summary
		var result []map[string]any
		for _, t := range traces {
			result = append(result, map[string]any{
				"id":       t.ID,
				"content":  truncate(t.Summary, 100),
				"is_core":  t.IsCore,
				"strength": t.Strength,
			})
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// Register mark_core tool
	server.RegisterTool("mark_core", mcp.ToolDef{
		Description: "Mark a memory trace as core (part of identity) or remove core status. Core traces are always included in prompts and define Bud's identity.",
		Properties: map[string]mcp.PropDef{
			"trace_id": {Type: "string", Description: "The ID of the trace to mark as core"},
			"is_core":  {Type: "boolean", Description: "Whether to mark as core (true) or remove core status (false). Defaults to true."},
		},
		Required: []string{"trace_id"},
	}, func(ctx any, args map[string]any) (string, error) {
		traceID, ok := args["trace_id"].(string)
		if !ok {
			return "", fmt.Errorf("trace_id is required")
		}

		isCore := true // default to marking as core
		if val, ok := args["is_core"].(bool); ok {
			isCore = val
		}

		if err := graphDB.SetTraceCore(traceID, isCore); err != nil {
			return "", err
		}

		action := "marked as core"
		if !isCore {
			action = "unmarked as core"
		}
		log.Printf("Trace %s %s", traceID, action)
		return fmt.Sprintf("Trace %s %s.", traceID, action), nil
	})

	// Register save_thought tool (save a thought to memory via inbox)
	server.RegisterTool("save_thought", mcp.ToolDef{
		Description: "Save a thought or observation to memory. Use this to remember decisions, observations, or anything worth recalling later. These get consolidated with other memories over time.",
		Properties: map[string]mcp.PropDef{
			"content": {Type: "string", Description: "The thought or observation to save (e.g., 'User prefers morning check-ins')"},
		},
		Required: []string{"content"},
	}, func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		// Write thought to inbox as a special message type
		thought := map[string]any{
			"id":        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
			"subtype":   "thought",
			"content":   content,
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
		}

		// Append to inbox file
		f, err := os.OpenFile(filepath.Join(queuesPath, "inbox.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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

	// Register signal_done tool (signal that Claude is done thinking)
	// Writes to inbox.jsonl as a signal-type message (unified inbox)
	server.RegisterTool("signal_done", mcp.ToolDef{
		Description: "Signal that you have finished processing and are ready for new prompts. IMPORTANT: Always call this when you have completed responding to a message or finishing a task. This helps track thinking time and enables autonomous work scheduling.",
		Properties: map[string]mcp.PropDef{
			"session_id": {Type: "string", Description: "The current session ID (if known)"},
			"summary":    {Type: "string", Description: "Brief summary of what was accomplished (optional)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		sessionID, _ := args["session_id"].(string)
		summary, _ := args["summary"].(string)

		// Write as inbox message with type=signal
		msg := map[string]any{
			"id":        fmt.Sprintf("signal-%d", time.Now().UnixNano()),
			"type":      "signal",
			"subtype":   "done",
			"content":   summary,
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
			"extra": map[string]any{
				"session_id": sessionID,
			},
		}

		// Write to inbox file (unified queue)
		inboxPath := filepath.Join(queuesPath, "inbox.jsonl")
		f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open inbox file: %w", err)
		}
		defer f.Close()

		data, _ := json.Marshal(msg)
		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write signal: %w", err)
		}

		log.Printf("Session done signal: session=%s summary=%s", sessionID, truncate(summary, 50))
		return "Done signal recorded. Ready for new prompts.", nil
	})

	// Register trigger_redeploy tool - allows bud to request its own redeployment
	// Runs deploy.sh directly in background (no watcher needed)
	server.RegisterTool("trigger_redeploy", mcp.ToolDef{
		Description: "Trigger a redeployment of the bud service. Use this after code changes have been pushed.",
		Properties: map[string]mcp.PropDef{
			"reason": {Type: "string", Description: "Reason for redeployment (optional)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		reason, _ := args["reason"].(string)
		if reason == "" {
			reason = "Redeploy requested"
		}

		// statePath is "state" relative to bud dir, so go up one level
		budDir := filepath.Dir(statePath)
		if budDir == "." {
			// statePath is just "state", so we're already in bud dir
			budDir = "."
		}
		deployScript := filepath.Join(budDir, "deploy", "deploy.sh")
		if _, err := os.Stat(deployScript); os.IsNotExist(err) {
			return "", fmt.Errorf("deploy script not found: %s", deployScript)
		}

		// Run deploy.sh in background so we can return before being killed
		cmd := exec.Command("bash", "-c", fmt.Sprintf("nohup %s > /dev/null 2>&1 &", deployScript))
		if err := cmd.Start(); err != nil {
			return "", fmt.Errorf("failed to start deploy: %w", err)
		}

		log.Printf("Redeploy triggered: %s", reason)
		return "Redeploy started. Service will restart momentarily.", nil
	})

	// Register close_claude_sessions tool - closes all Claude Code sessions in tmux
	// This allows Bud to restart Claude itself
	server.RegisterTool("close_claude_sessions", mcp.ToolDef{
		Description: "Close all Claude Code sessions running in tmux. Use this before trigger_redeploy to cleanly restart Claude. Returns list of closed session names.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		// List all tmux windows with their current command
		cmd := exec.Command("tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}:#{pane_current_command}")
		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("failed to list tmux windows: %w", err)
		}

		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var closedWindows []string
		var errors []string

		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 4)
			if len(parts) < 4 {
				continue
			}
			sessionName := parts[0]
			windowIndex := parts[1]
			windowName := parts[2]
			currentCommand := parts[3]

			// Identify Claude sessions by:
			// 1. Command looks like a version number (e.g., "2.1.5") - typical of claude CLI
			// 2. Command is "claude" or contains "claude"
			// 3. Window name matches Claude session pattern (word-number like "owl-31")
			isVersion := len(currentCommand) > 0 && strings.Count(currentCommand, ".") >= 1 &&
				currentCommand[0] >= '0' && currentCommand[0] <= '9'
			isClaudeCmd := strings.Contains(strings.ToLower(currentCommand), "claude")

			// Check window name pattern: lowercase letters followed by dash and numbers
			hasClaudeWindowName := false
			if len(windowName) > 0 {
				dashIdx := strings.LastIndex(windowName, "-")
				if dashIdx > 0 && dashIdx < len(windowName)-1 {
					prefix := windowName[:dashIdx]
					suffix := windowName[dashIdx+1:]
					// Check if prefix is all lowercase letters and suffix is all digits
					allLetters := true
					for _, c := range prefix {
						if c < 'a' || c > 'z' {
							allLetters = false
							break
						}
					}
					allDigits := true
					for _, c := range suffix {
						if c < '0' || c > '9' {
							allDigits = false
							break
						}
					}
					hasClaudeWindowName = allLetters && allDigits && len(prefix) > 0 && len(suffix) > 0
				}
			}

			if isVersion || isClaudeCmd || hasClaudeWindowName {
				// Skip windows that are clearly not Claude (monitor, zsh, etc.)
				if windowName == "monitor" || currentCommand == "zsh" || currentCommand == "bash" {
					continue
				}

				// Kill the window
				target := fmt.Sprintf("%s:%s", sessionName, windowIndex)
				killCmd := exec.Command("tmux", "kill-window", "-t", target)
				if err := killCmd.Run(); err != nil {
					errors = append(errors, fmt.Sprintf("failed to kill %s: %v", windowName, err))
				} else {
					closedWindows = append(closedWindows, windowName)
					log.Printf("Closed Claude session: %s", windowName)
				}
			}
		}

		result := map[string]any{
			"closed": closedWindows,
			"count":  len(closedWindows),
		}
		if len(errors) > 0 {
			result["errors"] = errors
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// Initialize activity log for observability
	activityLog := activity.New(statePath)

	// Register journal_log tool (writes to activity.jsonl for unified logging)
	// Kept for backwards compatibility - all logging now goes to activity.jsonl
	server.RegisterTool("journal_log", mcp.ToolDef{
		Description: "Log a decision, action, or observation to the journal for observability. Use this to record your reasoning, decisions made, and actions taken. Helps answer 'what did you do today?' and 'why did you do that?'",
		Properties: map[string]mcp.PropDef{
			"type":      {Type: "string", Description: "Entry type: 'decision', 'impulse', 'reflex', 'exploration', 'action', or 'observation'"},
			"summary":   {Type: "string", Description: "Brief description of what happened"},
			"context":   {Type: "string", Description: "What prompted this (optional)"},
			"reasoning": {Type: "string", Description: "Why this decision was made (optional)"},
			"outcome":   {Type: "string", Description: "What resulted from this (optional)"},
		},
		Required: []string{"summary"},
	}, func(ctx any, args map[string]any) (string, error) {
		summary, _ := args["summary"].(string)
		context, _ := args["context"].(string)
		reasoning, _ := args["reasoning"].(string)
		outcome, _ := args["outcome"].(string)

		if summary == "" {
			return "", fmt.Errorf("summary is required")
		}

		// All journal entries now logged as activity decisions
		err := activityLog.LogDecision(summary, reasoning, context, outcome)
		if err != nil {
			return "", fmt.Errorf("failed to log entry: %w", err)
		}

		log.Printf("Activity logged: %s", truncate(summary, 50))
		return "Logged to activity.", nil
	})

	// Register journal_recent tool (reads from activity.jsonl)
	server.RegisterTool("journal_recent", mcp.ToolDef{
		Description: "Get recent journal entries. Use this to review what you've been doing and why.",
		Properties: map[string]mcp.PropDef{
			"count": {Type: "number", Description: "Number of entries to return (default 20)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		count := 20 // default
		if n, ok := args["count"].(float64); ok && n > 0 {
			count = int(n)
		}

		entries, err := activityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get entries: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register journal_today tool (reads from activity.jsonl)
	server.RegisterTool("journal_today", mcp.ToolDef{
		Description: "Get today's journal entries. Use this to answer 'what did you do today?'",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		entries, err := activityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's entries: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries today yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_recent tool (get recent activity entries)
	server.RegisterTool("activity_recent", mcp.ToolDef{
		Description: "Get recent activity entries from the log.",
		Properties: map[string]mcp.PropDef{
			"count": {Type: "number", Description: "Number of entries to return (default 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		count := 50 // default
		if n, ok := args["count"].(float64); ok && n > 0 {
			count = int(n)
		}

		entries, err := activityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get activity entries: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_today tool (get today's activity entries)
	server.RegisterTool("activity_today", mcp.ToolDef{
		Description: "Get today's activity entries.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		entries, err := activityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's activity: %w", err)
		}

		if len(entries) == 0 {
			return "No activity entries today yet.", nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_search tool (search activity by text)
	server.RegisterTool("activity_search", mcp.ToolDef{
		Description: "Search activity entries by text.",
		Properties: map[string]mcp.PropDef{
			"query": {Type: "string", Description: "Text to search for"},
			"limit": {Type: "number", Description: "Maximum entries to return (default 100)"},
		},
		Required: []string{"query"},
	}, func(ctx any, args map[string]any) (string, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("query is required")
		}

		limit := 100 // default
		if n, ok := args["limit"].(float64); ok && n > 0 {
			limit = int(n)
		}

		entries, err := activityLog.Search(query, limit)
		if err != nil {
			return "", fmt.Errorf("failed to search activity: %w", err)
		}

		if len(entries) == 0 {
			return fmt.Sprintf("No activity entries matching '%s'.", query), nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Register activity_by_type tool (filter activity by event type)
	server.RegisterTool("activity_by_type", mcp.ToolDef{
		Description: "Filter activity entries by event type.",
		Properties: map[string]mcp.PropDef{
			"type":  {Type: "string", Description: "Event type: input, reflex, reflex_pass, executive_wake, executive_done, action, decision, error"},
			"limit": {Type: "number", Description: "Maximum entries to return (default 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		typeStr, _ := args["type"].(string)
		if typeStr == "" {
			// Return list of valid types
			return "Valid types: input, reflex, reflex_pass, executive_wake, executive_done, action, decision, error", nil
		}

		limit := 50 // default
		if n, ok := args["limit"].(float64); ok && n > 0 {
			limit = int(n)
		}

		entries, err := activityLog.ByType(activity.Type(typeStr), limit)
		if err != nil {
			return "", fmt.Errorf("failed to filter activity: %w", err)
		}

		if len(entries) == 0 {
			return fmt.Sprintf("No activity entries of type '%s'.", typeStr), nil
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// Initialize motivation stores
	taskStore := motivation.NewTaskStore(statePath)
	ideaStore := motivation.NewIdeaStore(statePath)
	taskStore.Load()
	ideaStore.Load()

	// Register add_bud_task tool
	server.RegisterTool("add_bud_task", mcp.ToolDef{
		Description: "Add a task (Bud's commitment) to your task queue. Use this to track things you've committed to do.",
		Properties: map[string]mcp.PropDef{
			"task":     {Type: "string", Description: "What you need to do"},
			"context":  {Type: "string", Description: "Why this task exists (optional)"},
			"priority": {Type: "number", Description: "Priority level: 1=highest, 2=medium, 3=low (default 2)"},
			"due":      {Type: "string", Description: "Due date/time in RFC3339 format (optional)"},
		},
		Required: []string{"task"},
	}, func(ctx any, args map[string]any) (string, error) {
		task, ok := args["task"].(string)
		if !ok || task == "" {
			return "", fmt.Errorf("task description is required")
		}

		t := &motivation.Task{
			Task:    task,
			Context: "",
			Status:  "pending",
		}

		if context, ok := args["context"].(string); ok {
			t.Context = context
		}
		if priority, ok := args["priority"].(float64); ok {
			t.Priority = int(priority)
		} else {
			t.Priority = 2 // default medium priority
		}
		if dueStr, ok := args["due"].(string); ok && dueStr != "" {
			if due := parseDueTime(dueStr); due != nil {
				t.Due = due
			} else {
				log.Printf("Warning: could not parse due time: %s", dueStr)
			}
		}

		taskStore.Add(t)
		if err := taskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		dueInfo := ""
		if t.Due != nil {
			dueInfo = fmt.Sprintf(" (due: %s)", t.Due.Format(time.RFC3339))
		}
		log.Printf("Added task: %s%s", truncate(task, 50), dueInfo)
		return fmt.Sprintf("Task added: %s (ID: %s)%s", task, t.ID, dueInfo), nil
	})

	// Register list_bud_tasks tool
	server.RegisterTool("list_bud_tasks", mcp.ToolDef{
		Description: "List pending Bud tasks. Use this to see what you've committed to do.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		tasks := taskStore.GetPending()
		if len(tasks) == 0 {
			return "No pending tasks.", nil
		}

		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})

	// Register complete_bud_task tool
	server.RegisterTool("complete_bud_task", mcp.ToolDef{
		Description: "Mark a Bud task as complete.",
		Properties: map[string]mcp.PropDef{
			"task_id": {Type: "string", Description: "ID of the task to complete"},
		},
		Required: []string{"task_id"},
	}, func(ctx any, args map[string]any) (string, error) {
		taskID, ok := args["task_id"].(string)
		if !ok || taskID == "" {
			return "", fmt.Errorf("task_id is required")
		}

		task := taskStore.Get(taskID)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", taskID)
		}

		taskStore.Complete(taskID)
		if err := taskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Completed task: %s", taskID)
		return fmt.Sprintf("Task completed: %s", task.Task), nil
	})

	// Register add_idea tool
	server.RegisterTool("add_idea", mcp.ToolDef{
		Description: "Save an idea for later exploration. Ideas are things you want to learn or think about when idle.",
		Properties: map[string]mcp.PropDef{
			"idea":       {Type: "string", Description: "The idea or topic to explore"},
			"sparked_by": {Type: "string", Description: "What triggered this idea (optional)"},
			"priority":   {Type: "number", Description: "Interest level: 1=highest, 2=medium, 3=low (default 2)"},
		},
		Required: []string{"idea"},
	}, func(ctx any, args map[string]any) (string, error) {
		idea, ok := args["idea"].(string)
		if !ok || idea == "" {
			return "", fmt.Errorf("idea description is required")
		}

		i := &motivation.Idea{
			Idea:     idea,
			Priority: 2, // default medium
		}

		if sparkedBy, ok := args["sparked_by"].(string); ok {
			i.SparkBy = sparkedBy
		}
		if priority, ok := args["priority"].(float64); ok {
			i.Priority = int(priority)
		}

		ideaStore.Add(i)
		if err := ideaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save idea: %w", err)
		}

		log.Printf("Added idea: %s", truncate(idea, 50))
		return fmt.Sprintf("Idea saved for later exploration: %s (ID: %s)", idea, i.ID), nil
	})

	// Register list_ideas tool
	server.RegisterTool("list_ideas", mcp.ToolDef{
		Description: "List unexplored ideas. Use this to find something to think about during idle time.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		ideas := ideaStore.GetUnexplored()
		if len(ideas) == 0 {
			return "No unexplored ideas.", nil
		}

		data, _ := json.MarshalIndent(ideas, "", "  ")
		return string(data), nil
	})

	// Register explore_idea tool (mark as explored with notes)
	server.RegisterTool("explore_idea", mcp.ToolDef{
		Description: "Mark an idea as explored, with notes about what you learned.",
		Properties: map[string]mcp.PropDef{
			"idea_id": {Type: "string", Description: "ID of the idea that was explored"},
			"notes":   {Type: "string", Description: "What you learned or discovered (optional)"},
		},
		Required: []string{"idea_id"},
	}, func(ctx any, args map[string]any) (string, error) {
		ideaID, ok := args["idea_id"].(string)
		if !ok || ideaID == "" {
			return "", fmt.Errorf("idea_id is required")
		}

		idea := ideaStore.Get(ideaID)
		if idea == nil {
			return "", fmt.Errorf("idea not found: %s", ideaID)
		}

		notes := ""
		if n, ok := args["notes"].(string); ok {
			notes = n
		}

		ideaStore.MarkExplored(ideaID, notes)
		if err := ideaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Explored idea: %s", ideaID)
		return fmt.Sprintf("Idea explored: %s", idea.Idea), nil
	})

	// Initialize reflex engine
	reflexEngine := reflex.NewEngine(statePath)
	reflexEngine.Load()

	// Register create_reflex tool
	server.RegisterTool("create_reflex", mcp.ToolDef{
		Description: "Create a new reflex (automated response). Reflexes run without waking the executive for pattern-matched inputs.",
		Properties: map[string]mcp.PropDef{
			"name":        {Type: "string", Description: "Unique name for the reflex"},
			"description": {Type: "string", Description: "What this reflex does"},
			"pattern":     {Type: "string", Description: "Regex pattern to match (use capture groups for extraction)"},
			"extract":     {Type: "array", Description: "Names for captured groups (e.g., [\"url\", \"title\"])"},
			"pipeline":    {Type: "array", Description: "Array of action steps: [{action, input, output, ...params}]"},
		},
		Required: []string{"name", "pipeline"},
	}, func(ctx any, args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return "", fmt.Errorf("name is required")
		}

		pattern := ""
		if p, ok := args["pattern"].(string); ok {
			pattern = p
		}

		description := ""
		if d, ok := args["description"].(string); ok {
			description = d
		}

		// Parse pipeline from JSON array
		var pipeline reflex.Pipeline
		if pipelineJSON, ok := args["pipeline"].([]any); ok {
			for _, step := range pipelineJSON {
				if stepMap, ok := step.(map[string]any); ok {
					ps := reflex.PipelineStep{
						Params: make(map[string]any),
					}
					if a, ok := stepMap["action"].(string); ok {
						ps.Action = a
					}
					if i, ok := stepMap["input"].(string); ok {
						ps.Input = i
					}
					if o, ok := stepMap["output"].(string); ok {
						ps.Output = o
					}
					for k, v := range stepMap {
						if k != "action" && k != "input" && k != "output" {
							ps.Params[k] = v
						}
					}
					pipeline = append(pipeline, ps)
				}
			}
		}

		r := &reflex.Reflex{
			Name:        name,
			Description: description,
			Trigger: reflex.Trigger{
				Pattern: pattern,
			},
			Pipeline: pipeline,
		}

		// Parse extract if provided
		if extract, ok := args["extract"].([]any); ok {
			for _, e := range extract {
				if s, ok := e.(string); ok {
					r.Trigger.Extract = append(r.Trigger.Extract, s)
				}
			}
		}

		if err := reflexEngine.SaveReflex(r); err != nil {
			return "", fmt.Errorf("failed to save reflex: %w", err)
		}

		log.Printf("Created reflex: %s", name)
		return fmt.Sprintf("Reflex '%s' created and saved. It will be active on next message.", name), nil
	})

	// Register list_reflexes tool
	server.RegisterTool("list_reflexes", mcp.ToolDef{
		Description: "List all defined reflexes.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		reflexes := reflexEngine.List()
		if len(reflexes) == 0 {
			return "No reflexes defined.", nil
		}

		var result []map[string]any
		for _, r := range reflexes {
			result = append(result, map[string]any{
				"name":        r.Name,
				"description": r.Description,
				"pattern":     r.Trigger.Pattern,
				"fire_count":  r.FireCount,
			})
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	// Register delete_reflex tool
	server.RegisterTool("delete_reflex", mcp.ToolDef{
		Description: "Delete a reflex by name.",
		Properties: map[string]mcp.PropDef{
			"name": {Type: "string", Description: "Name of the reflex to delete"},
		},
		Required: []string{"name"},
	}, func(ctx any, args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return "", fmt.Errorf("name is required")
		}

		if err := reflexEngine.Delete(name); err != nil {
			return "", err
		}

		return fmt.Sprintf("Reflex '%s' deleted.", name), nil
	})

	// Initialize GTD store (user's tasks, not Bud's commitments)
	gtdStore := gtd.NewGTDStore(statePath)
	if err := gtdStore.Load(); err != nil {
		log.Printf("Warning: Failed to load GTD store: %v", err)
	}

	// Register gtd_add tool
	server.RegisterTool("gtd_add", mcp.ToolDef{
		Description: "Add a task to the user's GTD system. Quick capture to inbox by default, or specify when/project to place it directly.",
		Properties: map[string]mcp.PropDef{
			"title":   {Type: "string", Description: "Task title (what needs to be done)"},
			"notes":   {Type: "string", Description: "Additional notes or context for the task (optional)"},
			"when":    {Type: "string", Description: "When to do it: inbox (default), today, anytime, someday, or YYYY-MM-DD date"},
			"project": {Type: "string", Description: "Project ID to add task to (optional)"},
			"heading": {Type: "string", Description: "Heading name within the project (requires project)"},
			"area":    {Type: "string", Description: "Area ID for the task (optional, only if not in a project)"},
		},
		Required: []string{"title"},
	}, func(ctx any, args map[string]any) (string, error) {
		title, ok := args["title"].(string)
		if !ok || title == "" {
			return "", fmt.Errorf("title is required")
		}

		task := &gtd.Task{
			Title: title,
		}

		// Optional fields
		if notes, ok := args["notes"].(string); ok {
			task.Notes = notes
		}
		if when, ok := args["when"].(string); ok && when != "" {
			task.When = when
		}
		if project, ok := args["project"].(string); ok {
			task.Project = project
		}
		if heading, ok := args["heading"].(string); ok {
			task.Heading = heading
		}
		if area, ok := args["area"].(string); ok {
			task.Area = area
		}

		// Validate before adding
		if err := gtdStore.ValidateTask(task); err != nil {
			return "", fmt.Errorf("validation failed: %w", err)
		}

		gtdStore.AddTask(task)
		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		location := task.When
		if task.Project != "" {
			if p := gtdStore.GetProject(task.Project); p != nil {
				location = fmt.Sprintf("project '%s'", p.Title)
				if task.Heading != "" {
					location += fmt.Sprintf(" / %s", task.Heading)
				}
			}
		}

		log.Printf("Added GTD task: %s (when: %s)", truncate(title, 50), location)
		return fmt.Sprintf("Task added to %s: %s (ID: %s)", location, title, task.ID), nil
	})

	// Register gtd_list tool
	server.RegisterTool("gtd_list", mcp.ToolDef{
		Description: "List tasks from the user's GTD system with optional filters.",
		Properties: map[string]mcp.PropDef{
			"when":    {Type: "string", Description: "Filter by when: inbox, today, anytime, someday, logbook (completed+canceled), or YYYY-MM-DD date"},
			"project": {Type: "string", Description: "Filter by project ID"},
			"area":    {Type: "string", Description: "Filter by area ID"},
			"status":  {Type: "string", Description: "Filter by status: open (default), completed, canceled, or all"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		when, _ := args["when"].(string)
		project, _ := args["project"].(string)
		area, _ := args["area"].(string)
		status, _ := args["status"].(string)

		// Handle "logbook" as a virtual when value (completed + canceled)
		isLogbook := when == "logbook"
		if isLogbook {
			when = "" // get all tasks, filter by status below
			status = "all"
		} else if status == "" {
			status = "open" // default to open tasks
		}

		tasks := gtdStore.GetTasks(when, project, area)

		// Filter by status
		var filtered []gtd.Task
		for _, t := range tasks {
			if isLogbook {
				// Logbook shows completed and canceled
				if t.Status == "completed" || t.Status == "canceled" {
					filtered = append(filtered, t)
				}
			} else if status == "all" || t.Status == status {
				filtered = append(filtered, t)
			}
		}

		if len(filtered) == 0 {
			filterDesc := ""
			if when != "" {
				filterDesc += fmt.Sprintf(" when=%s", when)
			}
			if project != "" {
				filterDesc += fmt.Sprintf(" project=%s", project)
			}
			if area != "" {
				filterDesc += fmt.Sprintf(" area=%s", area)
			}
			if status != "all" {
				filterDesc += fmt.Sprintf(" status=%s", status)
			}
			if filterDesc == "" {
				return "No tasks found.", nil
			}
			return fmt.Sprintf("No tasks found matching:%s", filterDesc), nil
		}

		data, _ := json.MarshalIndent(filtered, "", "  ")
		return string(data), nil
	})

	// Register gtd_update tool
	server.RegisterTool("gtd_update", mcp.ToolDef{
		Description: "Update a task in the user's GTD system. Only provided fields are updated.",
		Properties: map[string]mcp.PropDef{
			"id":        {Type: "string", Description: "Task ID to update (required)"},
			"title":     {Type: "string", Description: "New title for the task"},
			"notes":     {Type: "string", Description: "New notes for the task"},
			"when":      {Type: "string", Description: "When to do it: inbox, today, anytime, someday, or YYYY-MM-DD date"},
			"project":   {Type: "string", Description: "Project ID to move task to (empty string to remove from project)"},
			"heading":   {Type: "string", Description: "Heading name within the project"},
			"area":      {Type: "string", Description: "Area ID for the task (empty string to remove area)"},
			"checklist": {Type: "array", Description: "Checklist items as array of {text, done} objects"},
		},
		Required: []string{"id"},
	}, func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		// Get existing task
		task := gtdStore.GetTask(id)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", id)
		}

		// Track what was updated for the response
		var updates []string

		// Update provided fields
		if title, ok := args["title"].(string); ok {
			task.Title = title
			updates = append(updates, "title")
		}
		if notes, ok := args["notes"].(string); ok {
			task.Notes = notes
			updates = append(updates, "notes")
		}
		if when, ok := args["when"].(string); ok {
			task.When = when
			updates = append(updates, "when")
		}
		if project, ok := args["project"].(string); ok {
			task.Project = project
			updates = append(updates, "project")
		}
		if heading, ok := args["heading"].(string); ok {
			task.Heading = heading
			updates = append(updates, "heading")
		}
		if area, ok := args["area"].(string); ok {
			task.Area = area
			updates = append(updates, "area")
		}
		if checklistRaw, ok := args["checklist"].([]any); ok {
			var checklist []gtd.ChecklistItem
			for _, item := range checklistRaw {
				if itemMap, ok := item.(map[string]any); ok {
					ci := gtd.ChecklistItem{}
					if text, ok := itemMap["text"].(string); ok {
						ci.Text = text
					}
					if done, ok := itemMap["done"].(bool); ok {
						ci.Done = done
					}
					checklist = append(checklist, ci)
				}
			}
			task.Checklist = checklist
			updates = append(updates, "checklist")
		}

		// Validate the updated task
		if err := gtdStore.ValidateTask(task); err != nil {
			return "", fmt.Errorf("validation failed: %w", err)
		}

		// Save the update
		if err := gtdStore.UpdateTask(task); err != nil {
			return "", fmt.Errorf("failed to update task: %w", err)
		}
		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Updated GTD task: %s (fields: %v)", id, updates)
		return fmt.Sprintf("Task updated: %s (updated: %s)", task.Title, strings.Join(updates, ", ")), nil
	})

	// Register gtd_complete tool
	server.RegisterTool("gtd_complete", mcp.ToolDef{
		Description: "Mark a task as complete in the user's GTD system. Handles repeating tasks by creating the next occurrence.",
		Properties: map[string]mcp.PropDef{
			"id": {Type: "string", Description: "Task ID to mark as complete (required)"},
		},
		Required: []string{"id"},
	}, func(ctx any, args map[string]any) (string, error) {
		id, ok := args["id"].(string)
		if !ok || id == "" {
			return "", fmt.Errorf("id is required")
		}

		// Get task first to include info in response
		task := gtdStore.GetTask(id)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", id)
		}

		taskTitle := task.Title
		isRepeating := task.Repeat != ""

		// Complete the task (handles repeating tasks internally)
		if err := gtdStore.CompleteTask(id); err != nil {
			return "", fmt.Errorf("failed to complete task: %w", err)
		}
		if err := gtdStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Completed GTD task: %s (repeating: %v)", id, isRepeating)
		if isRepeating {
			return fmt.Sprintf("Task completed: %s (next occurrence created for repeating task)", taskTitle), nil
		}
		return fmt.Sprintf("Task completed: %s", taskTitle), nil
	})

	// Register gtd_areas tool
	server.RegisterTool("gtd_areas", mcp.ToolDef{
		Description: "Manage areas of responsibility in the user's GTD system. Areas are high-level categories like Work, Home, Health.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action to perform: list, add, or update"},
			"id":     {Type: "string", Description: "Area ID (required for update)"},
			"title":  {Type: "string", Description: "Area title (required for add, optional for update)"},
		},
		Required: []string{"action"},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			return "", fmt.Errorf("action is required (list, add, or update)")
		}

		switch action {
		case "list":
			areas := gtdStore.GetAreas()
			if len(areas) == 0 {
				return "No areas defined.", nil
			}
			data, _ := json.MarshalIndent(areas, "", "  ")
			return string(data), nil

		case "add":
			title, ok := args["title"].(string)
			if !ok || title == "" {
				return "", fmt.Errorf("title is required for add")
			}

			area := &gtd.Area{
				Title: title,
			}
			gtdStore.AddArea(area)
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save area: %w", err)
			}

			log.Printf("Added GTD area: %s", title)
			return fmt.Sprintf("Area added: %s (ID: %s)", title, area.ID), nil

		case "update":
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return "", fmt.Errorf("id is required for update")
			}

			area := gtdStore.GetArea(id)
			if area == nil {
				return "", fmt.Errorf("area not found: %s", id)
			}

			// Update title if provided
			if title, ok := args["title"].(string); ok && title != "" {
				area.Title = title
			}

			if err := gtdStore.UpdateArea(area); err != nil {
				return "", fmt.Errorf("failed to update area: %w", err)
			}
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("Updated GTD area: %s", id)
			return fmt.Sprintf("Area updated: %s", area.Title), nil

		default:
			return "", fmt.Errorf("unknown action: %s (use list, add, or update)", action)
		}
	})

	// Register gtd_projects tool
	server.RegisterTool("gtd_projects", mcp.ToolDef{
		Description: "Manage projects in the user's GTD system. Projects are multi-step outcomes with tasks.",
		Properties: map[string]mcp.PropDef{
			"action":   {Type: "string", Description: "Action to perform: list, add, or update"},
			"id":       {Type: "string", Description: "Project ID (required for update)"},
			"title":    {Type: "string", Description: "Project title (required for add, optional for update)"},
			"notes":    {Type: "string", Description: "Project notes (optional)"},
			"when":     {Type: "string", Description: "When: anytime, someday, or YYYY-MM-DD date (optional)"},
			"area":     {Type: "string", Description: "Area ID for filtering (list) or assignment (add/update)"},
			"status":   {Type: "string", Description: "Filter by status (list only): open (default), completed, canceled, or all"},
			"headings": {Type: "array", Description: "Ordered list of heading names for organizing tasks (optional)"},
		},
		Required: []string{"action"},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			return "", fmt.Errorf("action is required (list, add, or update)")
		}

		switch action {
		case "list":
			when, _ := args["when"].(string)
			area, _ := args["area"].(string)
			status, _ := args["status"].(string)
			if status == "" {
				status = "open" // default to open projects
			}

			projects := gtdStore.GetProjects(when, area)

			// Filter by status
			var filtered []gtd.Project
			for _, p := range projects {
				if status == "all" || p.Status == status {
					filtered = append(filtered, p)
				}
			}

			if len(filtered) == 0 {
				filterDesc := ""
				if when != "" {
					filterDesc += fmt.Sprintf(" when=%s", when)
				}
				if area != "" {
					filterDesc += fmt.Sprintf(" area=%s", area)
				}
				if status != "all" {
					filterDesc += fmt.Sprintf(" status=%s", status)
				}
				if filterDesc == "" {
					return "No projects found.", nil
				}
				return fmt.Sprintf("No projects found matching:%s", filterDesc), nil
			}
			data, _ := json.MarshalIndent(filtered, "", "  ")
			return string(data), nil

		case "add":
			title, ok := args["title"].(string)
			if !ok || title == "" {
				return "", fmt.Errorf("title is required for add")
			}

			project := &gtd.Project{
				Title: title,
			}

			// Optional fields
			if notes, ok := args["notes"].(string); ok {
				project.Notes = notes
			}
			if when, ok := args["when"].(string); ok && when != "" {
				project.When = when
			}
			if area, ok := args["area"].(string); ok {
				project.Area = area
			}
			if headingsRaw, ok := args["headings"].([]any); ok {
				var headings []string
				for _, h := range headingsRaw {
					if hs, ok := h.(string); ok {
						headings = append(headings, hs)
					}
				}
				project.Headings = headings
			}

			// Validate project before adding
			if err := gtdStore.ValidateProject(project); err != nil {
				return "", fmt.Errorf("validation failed: %w", err)
			}

			gtdStore.AddProject(project)
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save project: %w", err)
			}

			log.Printf("Added GTD project: %s", title)
			return fmt.Sprintf("Project added: %s (ID: %s)", title, project.ID), nil

		case "update":
			id, ok := args["id"].(string)
			if !ok || id == "" {
				return "", fmt.Errorf("id is required for update")
			}

			project := gtdStore.GetProject(id)
			if project == nil {
				return "", fmt.Errorf("project not found: %s", id)
			}

			// Track what was updated
			var updates []string

			// Update provided fields
			if title, ok := args["title"].(string); ok && title != "" {
				project.Title = title
				updates = append(updates, "title")
			}
			if notes, ok := args["notes"].(string); ok {
				project.Notes = notes
				updates = append(updates, "notes")
			}
			if when, ok := args["when"].(string); ok {
				project.When = when
				updates = append(updates, "when")
			}
			if area, ok := args["area"].(string); ok {
				project.Area = area
				updates = append(updates, "area")
			}
			if headingsRaw, ok := args["headings"].([]any); ok {
				var headings []string
				for _, h := range headingsRaw {
					if hs, ok := h.(string); ok {
						headings = append(headings, hs)
					}
				}
				project.Headings = headings
				updates = append(updates, "headings")
			}

			if err := gtdStore.UpdateProject(project); err != nil {
				return "", fmt.Errorf("failed to update project: %w", err)
			}
			if err := gtdStore.Save(); err != nil {
				return "", fmt.Errorf("failed to save: %w", err)
			}

			log.Printf("Updated GTD project: %s (fields: %v)", id, updates)
			return fmt.Sprintf("Project updated: %s (updated: %s)", project.Title, strings.Join(updates, ", ")), nil

		default:
			return "", fmt.Errorf("unknown action: %s (use list, add, or update)", action)
		}
	})

	// Register create_core tool (create a new core trace directly)
	server.RegisterTool("create_core", mcp.ToolDef{
		Description: "Create a new core identity trace directly. Use this to add new identity information that should always be present.",
		Properties: map[string]mcp.PropDef{
			"content": {Type: "string", Description: "The content of the core trace (e.g., 'I am Bud, a helpful assistant')"},
		},
		Required: []string{"content"},
	}, func(ctx any, args map[string]any) (string, error) {
		content, ok := args["content"].(string)
		if !ok || content == "" {
			return "", fmt.Errorf("content is required")
		}

		// Create new core trace
		trace := &graph.Trace{
			ID:           fmt.Sprintf("core-%d", time.Now().UnixNano()),
			Summary:      content,
			Activation:   1.0,
			Strength:     100,
			IsCore:       true,
			CreatedAt:    time.Now(),
			LastAccessed: time.Now(),
		}

		if err := graphDB.AddTrace(trace); err != nil {
			return "", fmt.Errorf("failed to create trace: %w", err)
		}

		log.Printf("Created core trace: %s", truncate(content, 50))
		return fmt.Sprintf("Created core trace %s.", trace.ID), nil
	})

	// Notion markdown conversion tools (pure conversion, no HTTP)
	// Use official Notion MCP (API-*) tools for HTTP operations
	server.RegisterTool("notion_get_content", mcp.ToolDef{
		Description: "Convert Notion blocks (from API-get-block-children) to markdown. Pass the 'results' array from the API response.",
		Properties: map[string]mcp.PropDef{
			"blocks": {Type: "string", Description: "JSON array of Notion blocks from API-get-block-children results"},
		},
		Required: []string{"blocks"},
	}, func(ctx any, args map[string]any) (string, error) {
		blocksJSON, _ := args["blocks"].(string)
		if blocksJSON == "" {
			return "", fmt.Errorf("blocks is required")
		}

		var blocks []map[string]any
		if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
			return "", fmt.Errorf("invalid JSON: %w", err)
		}

		markdown := notion.BlocksToMarkdown(blocks)
		if markdown == "" {
			return "(empty)", nil
		}
		return markdown, nil
	})

	server.RegisterTool("notion_create_page", mcp.ToolDef{
		Description: "Convert markdown to Notion blocks for API-patch-block-children. Returns JSON blocks array ready for the 'children' parameter.",
		Properties: map[string]mcp.PropDef{
			"markdown": {Type: "string", Description: "Markdown content to convert to Notion blocks"},
		},
		Required: []string{"markdown"},
	}, func(ctx any, args map[string]any) (string, error) {
		markdown, _ := args["markdown"].(string)
		if markdown == "" {
			return "", fmt.Errorf("markdown is required")
		}

		blocks := notion.MarkdownToBlocks(markdown)
		data, err := json.Marshal(blocks)
		if err != nil {
			return "", fmt.Errorf("failed to marshal blocks: %w", err)
		}
		return string(data), nil
	})

	server.RegisterTool("notion_update_content", mcp.ToolDef{
		Description: "Alias for notion_create_page - converts markdown to Notion blocks JSON.",
		Properties: map[string]mcp.PropDef{
			"markdown": {Type: "string", Description: "Markdown content to convert to Notion blocks"},
		},
		Required: []string{"markdown"},
	}, func(ctx any, args map[string]any) (string, error) {
		markdown, _ := args["markdown"].(string)
		if markdown == "" {
			return "", fmt.Errorf("markdown is required")
		}

		blocks := notion.MarkdownToBlocks(markdown)
		data, err := json.Marshal(blocks)
		if err != nil {
			return "", fmt.Errorf("failed to marshal blocks: %w", err)
		}
		return string(data), nil
	})

	server.RegisterTool("notion_append_content", mcp.ToolDef{
		Description: "Alias for notion_create_page - converts markdown to Notion blocks JSON.",
		Properties: map[string]mcp.PropDef{
			"markdown": {Type: "string", Description: "Markdown content to convert to Notion blocks"},
		},
		Required: []string{"markdown"},
	}, func(ctx any, args map[string]any) (string, error) {
		markdown, _ := args["markdown"].(string)
		if markdown == "" {
			return "", fmt.Errorf("markdown is required")
		}

		blocks := notion.MarkdownToBlocks(markdown)
		data, err := json.Marshal(blocks)
		if err != nil {
			return "", fmt.Errorf("failed to marshal blocks: %w", err)
		}
		return string(data), nil
	})

	server.RegisterTool("notion_list_blocks", mcp.ToolDef{
		Description: "Convert Notion blocks to a simple list with IDs and content preview. Pass blocks from API-get-block-children.",
		Properties: map[string]mcp.PropDef{
			"blocks": {Type: "string", Description: "JSON array of Notion blocks from API-get-block-children results"},
		},
		Required: []string{"blocks"},
	}, func(ctx any, args map[string]any) (string, error) {
		blocksJSON, _ := args["blocks"].(string)
		if blocksJSON == "" {
			return "", fmt.Errorf("blocks is required")
		}

		var blocks []map[string]any
		if err := json.Unmarshal([]byte(blocksJSON), &blocks); err != nil {
			return "", fmt.Errorf("invalid JSON: %w", err)
		}

		if len(blocks) == 0 {
			return "No blocks", nil
		}

		var result []map[string]string
		for _, b := range blocks {
			id, _ := b["id"].(string)
			blockType, _ := b["type"].(string)
			result = append(result, map[string]string{
				"id":   id,
				"type": blockType,
			})
		}

		data, _ := json.MarshalIndent(result, "", "  ")
		return string(data), nil
	})

	server.RegisterTool("notion_insert_block", mcp.ToolDef{
		Description: "Create a single Notion block JSON from type and markdown content. Use with API-patch-block-children.",
		Properties: map[string]mcp.PropDef{
			"block_type": {Type: "string", Description: "Block type: paragraph, heading_1, heading_2, heading_3, bulleted_list_item, numbered_list_item, to_do, quote, divider"},
			"content":    {Type: "string", Description: "Text content (supports **bold**, *italic*, `code`, [link](url))"},
		},
		Required: []string{"block_type"},
	}, func(ctx any, args map[string]any) (string, error) {
		blockType, _ := args["block_type"].(string)
		content, _ := args["content"].(string)

		if blockType == "" {
			return "", fmt.Errorf("block_type is required")
		}

		block := notion.CreateBlock(blockType, content)
		if block == nil {
			return "", fmt.Errorf("unsupported block type: %s", blockType)
		}

		data, err := json.Marshal(block)
		if err != nil {
			return "", fmt.Errorf("failed to marshal block: %w", err)
		}
		return string(data), nil
	})

	server.RegisterTool("notion_update_block", mcp.ToolDef{
		Description: "Create rich_text JSON from markdown for API-update-a-block. Returns the rich_text array only.",
		Properties: map[string]mcp.PropDef{
			"content": {Type: "string", Description: "Text content (supports **bold**, *italic*, `code`, [link](url))"},
		},
		Required: []string{"content"},
	}, func(ctx any, args map[string]any) (string, error) {
		content, _ := args["content"].(string)
		if content == "" {
			return "", fmt.Errorf("content is required")
		}

		// Create a paragraph block and extract just the rich_text
		block := notion.CreateBlock("paragraph", content)
		if para, ok := block["paragraph"].(map[string]any); ok {
			if rt, ok := para["rich_text"]; ok {
				data, err := json.Marshal(rt)
				if err != nil {
					return "", fmt.Errorf("failed to marshal rich_text: %w", err)
				}
				return string(data), nil
			}
		}
		return "[]", nil
	})

	// Notion sync tools (notion_pull, notion_diff, notion_push) are now provided
	// by the separate efficient-notion-mcp server for better modularity

	// Google Calendar tools (only register if credentials are configured)
	hasCalendarCreds := os.Getenv("GOOGLE_CALENDAR_CREDENTIALS") != "" || os.Getenv("GOOGLE_CALENDAR_CREDENTIALS_FILE") != ""
	hasCalendarIDs := os.Getenv("GOOGLE_CALENDAR_IDS") != "" || os.Getenv("GOOGLE_CALENDAR_ID") != ""
	if hasCalendarCreds && hasCalendarIDs {
		calendarClient, err := calendar.NewClient()
		if err != nil {
			log.Printf("Warning: Failed to create Calendar client: %v", err)
		} else {
			log.Printf("Google Calendar integration enabled (%d calendars)", len(calendarClient.CalendarIDs()))

			// Register calendar_today tool - get today's events
			server.RegisterTool("calendar_today", mcp.ToolDef{
				Description: "Get today's calendar events. Returns compact format by default (one line per event).",
				Properties: map[string]mcp.PropDef{
					"verbose": {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
				},
			}, func(ctx any, args map[string]any) (string, error) {
				events, err := calendarClient.GetTodayEvents(context.Background())
				if err != nil {
					return "", fmt.Errorf("failed to get today's events: %w", err)
				}

				if len(events) == 0 {
					return "No events scheduled for today.", nil
				}

				// Use verbose JSON only if explicitly requested
				if verbose, _ := args["verbose"].(bool); verbose {
					data, _ := json.MarshalIndent(events, "", "  ")
					return string(data), nil
				}

				return formatEventsCompact(events), nil
			})

			// Register calendar_upcoming tool - get upcoming events
			server.RegisterTool("calendar_upcoming", mcp.ToolDef{
				Description: "Get upcoming calendar events within a time window. Returns compact format by default.",
				Properties: map[string]mcp.PropDef{
					"duration":    {Type: "string", Description: "Time window to look ahead (e.g., '24h', '7d'). Default: 24h"},
					"max_results": {Type: "number", Description: "Maximum number of events to return. Default: 20"},
					"verbose":     {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
				},
			}, func(ctx any, args map[string]any) (string, error) {
				// Default to next 24 hours
				durationStr, _ := args["duration"].(string)
				if durationStr == "" {
					durationStr = "24h"
				}

				duration, err := time.ParseDuration(durationStr)
				if err != nil {
					return "", fmt.Errorf("invalid duration: %w", err)
				}

				maxResults := 20
				if n, ok := args["max_results"].(float64); ok && n > 0 {
					maxResults = int(n)
				}

				events, err := calendarClient.GetUpcomingEvents(context.Background(), duration, maxResults)
				if err != nil {
					return "", fmt.Errorf("failed to get upcoming events: %w", err)
				}

				if len(events) == 0 {
					return fmt.Sprintf("No events in the next %s.", durationStr), nil
				}

				// Use verbose JSON only if explicitly requested
				if verbose, _ := args["verbose"].(bool); verbose {
					data, _ := json.MarshalIndent(events, "", "  ")
					return string(data), nil
				}

				return formatEventsCompact(events), nil
			})

			// Register calendar_list_events tool - query events in a date range
			server.RegisterTool("calendar_list_events", mcp.ToolDef{
				Description: "Query calendar events in a specific date range. Returns compact format by default.",
				Properties: map[string]mcp.PropDef{
					"time_min":    {Type: "string", Description: "Start of time range (RFC3339 or YYYY-MM-DD). Default: now"},
					"time_max":    {Type: "string", Description: "End of time range (RFC3339 or YYYY-MM-DD). Default: 1 week from time_min"},
					"max_results": {Type: "number", Description: "Maximum number of events to return. Default: 50"},
					"query":       {Type: "string", Description: "Text to search for in event titles/descriptions (optional)"},
					"verbose":     {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
				},
			}, func(ctx any, args map[string]any) (string, error) {
				// Parse time range
				timeMinStr, _ := args["time_min"].(string)
				timeMaxStr, _ := args["time_max"].(string)

				var timeMin, timeMax time.Time
				var err error

				if timeMinStr == "" {
					timeMin = time.Now()
				} else {
					timeMin, err = time.Parse(time.RFC3339, timeMinStr)
					if err != nil {
						// Try date-only format
						timeMin, err = time.Parse("2006-01-02", timeMinStr)
						if err != nil {
							return "", fmt.Errorf("invalid time_min format (use RFC3339 or YYYY-MM-DD): %w", err)
						}
					}
				}

				if timeMaxStr == "" {
					timeMax = timeMin.Add(7 * 24 * time.Hour) // Default to 1 week
				} else {
					timeMax, err = time.Parse(time.RFC3339, timeMaxStr)
					if err != nil {
						// Try date-only format
						timeMax, err = time.Parse("2006-01-02", timeMaxStr)
						if err != nil {
							return "", fmt.Errorf("invalid time_max format (use RFC3339 or YYYY-MM-DD): %w", err)
						}
						// Add 24 hours to include the entire day
						timeMax = timeMax.Add(24 * time.Hour)
					}
				}

				params := calendar.ListEventsParams{
					TimeMin:    timeMin,
					TimeMax:    timeMax,
					MaxResults: 50,
				}

				if n, ok := args["max_results"].(float64); ok && n > 0 {
					params.MaxResults = int(n)
				}
				if q, ok := args["query"].(string); ok {
					params.Query = q
				}

				events, err := calendarClient.ListEvents(context.Background(), params)
				if err != nil {
					return "", fmt.Errorf("failed to list events: %w", err)
				}

				if len(events) == 0 {
					return "No events found in the specified time range.", nil
				}

				// Use verbose JSON only if explicitly requested
				if verbose, _ := args["verbose"].(bool); verbose {
					data, _ := json.MarshalIndent(map[string]any{
						"events":   events,
						"count":    len(events),
						"time_min": timeMin.Format(time.RFC3339),
						"time_max": timeMax.Format(time.RFC3339),
					}, "", "  ")
					return string(data), nil
				}

				// Compact format: header + one line per event
				header := fmt.Sprintf("%d events (%s to %s):\n",
					len(events),
					timeMin.Format("2006-01-02"),
					timeMax.Format("2006-01-02"))
				return header + formatEventsCompact(events), nil
			})

			// Register calendar_free_busy tool - check availability
			server.RegisterTool("calendar_free_busy", mcp.ToolDef{
				Description: "Check calendar availability/free-busy status for a time range.",
				Properties: map[string]mcp.PropDef{
					"time_min": {Type: "string", Description: "Start of time range (RFC3339 or YYYY-MM-DD). Default: now"},
					"time_max": {Type: "string", Description: "End of time range (RFC3339 or YYYY-MM-DD). Default: 24h from time_min"},
				},
			}, func(ctx any, args map[string]any) (string, error) {
				timeMinStr, _ := args["time_min"].(string)
				timeMaxStr, _ := args["time_max"].(string)

				var timeMin, timeMax time.Time
				var err error

				if timeMinStr == "" {
					timeMin = time.Now()
				} else {
					timeMin, err = time.Parse(time.RFC3339, timeMinStr)
					if err != nil {
						timeMin, err = time.Parse("2006-01-02", timeMinStr)
						if err != nil {
							return "", fmt.Errorf("invalid time_min format: %w", err)
						}
					}
				}

				if timeMaxStr == "" {
					timeMax = timeMin.Add(24 * time.Hour)
				} else {
					timeMax, err = time.Parse(time.RFC3339, timeMaxStr)
					if err != nil {
						timeMax, err = time.Parse("2006-01-02", timeMaxStr)
						if err != nil {
							return "", fmt.Errorf("invalid time_max format: %w", err)
						}
						timeMax = timeMax.Add(24 * time.Hour)
					}
				}

				busy, err := calendarClient.FreeBusy(context.Background(), calendar.FreeBusyParams{
					TimeMin: timeMin,
					TimeMax: timeMax,
				})
				if err != nil {
					return "", fmt.Errorf("failed to check availability: %w", err)
				}

				// Format response with busy periods and free time summary
				result := map[string]any{
					"time_min":     timeMin.Format(time.RFC3339),
					"time_max":     timeMax.Format(time.RFC3339),
					"busy_periods": busy,
					"busy_count":   len(busy),
				}

				if len(busy) == 0 {
					result["summary"] = "You are free during this entire time range."
				} else {
					var totalBusyMins float64
					for _, b := range busy {
						totalBusyMins += b.End.Sub(b.Start).Minutes()
					}
					totalMins := timeMax.Sub(timeMin).Minutes()
					freeMins := totalMins - totalBusyMins
					result["summary"] = fmt.Sprintf("%.0f minutes busy, %.0f minutes free out of %.0f total minutes.",
						totalBusyMins, freeMins, totalMins)
				}

				data, _ := json.MarshalIndent(result, "", "  ")
				return string(data), nil
			})

			// Register calendar_get_event tool - get a specific event by ID
			server.RegisterTool("calendar_get_event", mcp.ToolDef{
				Description: "Get details of a specific calendar event by ID.",
				Properties: map[string]mcp.PropDef{
					"event_id": {Type: "string", Description: "The event ID to retrieve"},
				},
				Required: []string{"event_id"},
			}, func(ctx any, args map[string]any) (string, error) {
				eventID, _ := args["event_id"].(string)
				if eventID == "" {
					return "", fmt.Errorf("event_id is required")
				}

				event, err := calendarClient.GetEvent(context.Background(), eventID)
				if err != nil {
					return "", fmt.Errorf("failed to get event: %w", err)
				}

				data, _ := json.MarshalIndent(event, "", "  ")
				return string(data), nil
			})

			// Register calendar_create_event tool - create a new event
			server.RegisterTool("calendar_create_event", mcp.ToolDef{
				Description: "Create a new calendar event.",
				Properties: map[string]mcp.PropDef{
					"summary":     {Type: "string", Description: "Event title/summary"},
					"start":       {Type: "string", Description: "Start time (RFC3339 or YYYY-MM-DD for all-day events)"},
					"end":         {Type: "string", Description: "End time (RFC3339 or YYYY-MM-DD). Default: 1 hour after start"},
					"description": {Type: "string", Description: "Event description (optional)"},
					"location":    {Type: "string", Description: "Event location (optional)"},
					"attendees":   {Type: "array", Description: "List of attendee email addresses (optional)"},
				},
				Required: []string{"summary", "start"},
			}, func(ctx any, args map[string]any) (string, error) {
				summary, _ := args["summary"].(string)
				if summary == "" {
					return "", fmt.Errorf("summary is required")
				}

				startStr, _ := args["start"].(string)
				endStr, _ := args["end"].(string)
				if startStr == "" {
					return "", fmt.Errorf("start time is required")
				}

				var start, end time.Time
				var err error
				var allDay bool

				// Try RFC3339 first, then date-only
				start, err = time.Parse(time.RFC3339, startStr)
				if err != nil {
					start, err = time.Parse("2006-01-02", startStr)
					if err != nil {
						return "", fmt.Errorf("invalid start format (use RFC3339 or YYYY-MM-DD): %w", err)
					}
					allDay = true
				}

				if endStr == "" {
					if allDay {
						end = start.Add(24 * time.Hour)
					} else {
						end = start.Add(time.Hour) // Default 1 hour
					}
				} else {
					end, err = time.Parse(time.RFC3339, endStr)
					if err != nil {
						end, err = time.Parse("2006-01-02", endStr)
						if err != nil {
							return "", fmt.Errorf("invalid end format: %w", err)
						}
					}
				}

				params := calendar.CreateEventParams{
					Summary: summary,
					Start:   start,
					End:     end,
					AllDay:  allDay,
				}

				if desc, ok := args["description"].(string); ok {
					params.Description = desc
				}
				if loc, ok := args["location"].(string); ok {
					params.Location = loc
				}
				if attendeesRaw, ok := args["attendees"].([]any); ok {
					for _, a := range attendeesRaw {
						if email, ok := a.(string); ok {
							params.Attendees = append(params.Attendees, email)
						}
					}
				}

				event, err := calendarClient.CreateEvent(context.Background(), params)
				if err != nil {
					return "", fmt.Errorf("failed to create event: %w", err)
				}

				log.Printf("Created calendar event: %s", event.Summary)

				data, _ := json.MarshalIndent(map[string]any{
					"message": "Event created successfully",
					"event":   event,
				}, "", "  ")
				return string(data), nil
			})
		}
	} else {
		log.Println("Google Calendar integration disabled (credentials not configured)")
	}

	// GitHub Projects tools (only register if GITHUB_TOKEN and GITHUB_ORG are set)
	if os.Getenv("GITHUB_TOKEN") != "" && os.Getenv("GITHUB_ORG") != "" {
		githubClient, err := github.NewClient()
		if err != nil {
			log.Printf("Warning: Failed to create GitHub client: %v", err)
		} else {
			log.Printf("GitHub Projects integration enabled (org: %s)", githubClient.Org())

			// Register github_list_projects tool
			server.RegisterTool("github_list_projects", mcp.ToolDef{
				Description: "List all GitHub Projects v2 in the configured organization. Returns project number, title, and URL.",
				Properties:  map[string]mcp.PropDef{},
			}, func(ctx any, args map[string]any) (string, error) {
				projects, err := githubClient.ListProjects()
				if err != nil {
					return "", fmt.Errorf("failed to list projects: %w", err)
				}

				if len(projects) == 0 {
					return "No projects found.", nil
				}

				// Compact format: one line per project
				var lines []string
				for _, p := range projects {
					status := ""
					if p.Closed {
						status = " (closed)"
					}
					lines = append(lines, fmt.Sprintf("#%d %s%s", p.Number, p.Title, status))
				}
				return strings.Join(lines, "\n"), nil
			})

			// Register github_get_project tool
			server.RegisterTool("github_get_project", mcp.ToolDef{
				Description: "Get a GitHub Project by number. Returns project details and field schema.",
				Properties: map[string]mcp.PropDef{
					"number": {Type: "number", Description: "The project number (visible in project URL)"},
				},
				Required: []string{"number"},
			}, func(ctx any, args map[string]any) (string, error) {
				number, ok := args["number"].(float64)
				if !ok || number == 0 {
					return "", fmt.Errorf("number is required")
				}

				project, err := githubClient.GetProject(int(number))
				if err != nil {
					return "", fmt.Errorf("failed to get project: %w", err)
				}

				// Get fields schema
				fields, err := githubClient.GetProjectFields(int(number))
				if err != nil {
					return "", fmt.Errorf("failed to get project fields: %w", err)
				}

				// Compact format with field schema
				result := map[string]any{
					"number": project.Number,
					"title":  project.Title,
					"url":    project.URL,
					"closed": project.Closed,
					"fields": fields,
				}

				data, _ := json.MarshalIndent(result, "", "  ")
				return string(data), nil
			})

			// Register github_project_items tool
			server.RegisterTool("github_project_items", mcp.ToolDef{
				Description: "Query items from a GitHub Project. Returns compact list format by default. Use status filter for views like backlog/sprint.",
				Properties: map[string]mcp.PropDef{
					"project":   {Type: "number", Description: "The project number"},
					"status":    {Type: "string", Description: "Filter by Status field (e.g., 'Backlog', 'In Progress', 'Done')"},
					"sprint":    {Type: "string", Description: "Filter by Sprint (e.g., 'Sprint 65'). Use 'backlog' for items with no sprint assigned."},
					"max_items": {Type: "number", Description: "Maximum items to return (default 100)"},
					"verbose":   {Type: "boolean", Description: "If true, return full JSON with all fields. Default: false (compact format)"},
				},
				Required: []string{"project"},
			}, func(ctx any, args map[string]any) (string, error) {
				projectNum, ok := args["project"].(float64)
				if !ok || projectNum == 0 {
					return "", fmt.Errorf("project number is required")
				}

				params := github.QueryItemsParams{
					ProjectNumber: int(projectNum),
					MaxItems:      100,
				}

				if status, ok := args["status"].(string); ok {
					params.Status = status
				}
				if sprint, ok := args["sprint"].(string); ok {
					params.Sprint = sprint
				}
				if maxItems, ok := args["max_items"].(float64); ok && maxItems > 0 {
					params.MaxItems = int(maxItems)
				}

				items, err := githubClient.QueryItems(params)
				if err != nil {
					return "", fmt.Errorf("failed to query items: %w", err)
				}

				if len(items) == 0 {
					filterDesc := ""
					if params.Status != "" {
						filterDesc = fmt.Sprintf(" with status '%s'", params.Status)
					}
					if params.Sprint != "" {
						if filterDesc != "" {
							filterDesc += " and"
						}
						if params.Sprint == "backlog" {
							filterDesc += " in backlog (no sprint)"
						} else {
							filterDesc += fmt.Sprintf(" in '%s'", params.Sprint)
						}
					}
					if filterDesc != "" {
						return fmt.Sprintf("No items%s.", filterDesc), nil
					}
					return "No items in project.", nil
				}

				// Use verbose JSON only if explicitly requested
				if verbose, _ := args["verbose"].(bool); verbose {
					data, _ := json.MarshalIndent(items, "", "  ")
					return string(data), nil
				}

				// Compact format
				return github.FormatItemsCompact(items), nil
			})
		}
	} else {
		log.Println("GitHub Projects integration disabled (GITHUB_TOKEN or GITHUB_ORG not set)")
	}

	// State introspection tools
	stateInspector := state.NewInspector(statePath, graphDB)

	server.RegisterTool("state_summary", mcp.ToolDef{
		Description: "Get summary of all state components (traces, percepts, threads, logs, queues).",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		summary, err := stateInspector.Summary()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(summary, "", "  ")
		return string(data), nil
	})

	server.RegisterTool("state_health", mcp.ToolDef{
		Description: "Run health checks on state and get recommendations for cleanup.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		health, err := stateInspector.Health()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(health, "", "  ")
		return string(data), nil
	})

	server.RegisterTool("state_traces", mcp.ToolDef{
		Description: "Manage memory traces. Actions: list, show, delete, clear, regen_core.",
		Properties: map[string]mcp.PropDef{
			"action":     {Type: "string", Description: "Action: list (default), show, delete, clear, regen_core"},
			"id":         {Type: "string", Description: "Trace ID (for show/delete)"},
			"clear_core": {Type: "boolean", Description: "If true with clear action, clears core traces instead of non-core"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			traces, err := stateInspector.ListTraces()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(traces, "", "  ")
			return string(data), nil

		case "show":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for show action")
			}
			trace, err := stateInspector.GetTrace(id)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(trace, "", "  ")
			return string(data), nil

		case "delete":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for delete action")
			}
			if err := stateInspector.DeleteTrace(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted trace: %s", id), nil

		case "clear":
			clearCore, _ := args["clear_core"].(bool)
			count, err := stateInspector.ClearTraces(clearCore)
			if err != nil {
				return "", err
			}
			if clearCore {
				return fmt.Sprintf("Cleared %d core traces", count), nil
			}
			return fmt.Sprintf("Cleared %d non-core traces", count), nil

		case "regen_core":
			seedPath := "seed/core_seed.md"
			count, err := stateInspector.RegenCore(seedPath)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Regenerated %d core traces", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_percepts", mcp.ToolDef{
		Description: "Manage percepts (short-term memory). Actions: list, count, clear.",
		Properties: map[string]mcp.PropDef{
			"action":     {Type: "string", Description: "Action: list (default), count, clear"},
			"older_than": {Type: "string", Description: "Duration for clear (e.g., '1h', '30m'). If omitted, clears all."},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			percepts, err := stateInspector.ListPercepts()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(percepts, "", "  ")
			return string(data), nil

		case "count":
			percepts, err := stateInspector.ListPercepts()
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%d", len(percepts)), nil

		case "clear":
			olderThan, _ := args["older_than"].(string)
			var dur time.Duration
			if olderThan != "" {
				var err error
				dur, err = time.ParseDuration(olderThan)
				if err != nil {
					return "", fmt.Errorf("invalid duration: %w", err)
				}
			}
			count, err := stateInspector.ClearPercepts(dur)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Cleared %d percepts", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_threads", mcp.ToolDef{
		Description: "Manage threads (working memory). Actions: list, show, clear.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: list (default), show, clear"},
			"id":     {Type: "string", Description: "Thread ID (for show)"},
			"status": {Type: "string", Description: "Filter for clear (active, paused, frozen, complete)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			threads, err := stateInspector.ListThreads()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(threads, "", "  ")
			return string(data), nil

		case "show":
			id, ok := args["id"].(string)
			if !ok {
				return "", fmt.Errorf("id required for show action")
			}
			thread, err := stateInspector.GetThread(id)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(thread, "", "  ")
			return string(data), nil

		case "clear":
			statusStr, _ := args["status"].(string)
			var statusPtr *types.ThreadStatus
			if statusStr != "" {
				s := types.ThreadStatus(statusStr)
				statusPtr = &s
			}
			count, err := stateInspector.ClearThreads(statusPtr)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Cleared %d threads", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_logs", mcp.ToolDef{
		Description: "Manage journal and activity logs. Actions: tail, truncate.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: tail (default), truncate"},
			"count":  {Type: "integer", Description: "Number of entries for tail (default 20)"},
			"keep":   {Type: "integer", Description: "Entries to keep for truncate (default 100)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "tail"
		}

		switch action {
		case "tail":
			count := 20
			if c, ok := args["count"].(float64); ok {
				count = int(c)
			}
			entries, err := stateInspector.TailLogs(count)
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(entries, "", "  ")
			return string(data), nil

		case "truncate":
			keep := 100
			if k, ok := args["keep"].(float64); ok {
				keep = int(k)
			}
			if err := stateInspector.TruncateLogs(keep); err != nil {
				return "", err
			}
			return fmt.Sprintf("Truncated logs to %d entries", keep), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_queues", mcp.ToolDef{
		Description: "Manage message queues (inbox, outbox, signals). Actions: list, clear.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: list (default), clear"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			queues, err := stateInspector.ListQueues()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(queues, "", "  ")
			return string(data), nil

		case "clear":
			if err := stateInspector.ClearQueues(); err != nil {
				return "", err
			}
			return "Cleared all queues", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_sessions", mcp.ToolDef{
		Description: "Manage session tracking. Actions: list, clear.",
		Properties: map[string]mcp.PropDef{
			"action": {Type: "string", Description: "Action: list (default), clear"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		action, _ := args["action"].(string)
		if action == "" {
			action = "list"
		}

		switch action {
		case "list":
			sessions, err := stateInspector.ListSessions()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(sessions, "", "  ")
			return string(data), nil

		case "clear":
			if err := stateInspector.ClearSessions(); err != nil {
				return "", err
			}
			return "Cleared sessions", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	server.RegisterTool("state_regen_core", mcp.ToolDef{
		Description: "Regenerate core identity traces from core_seed.md. Clears existing core traces first.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		seedPath := "seed/core_seed.md"
		count, err := stateInspector.RegenCore(seedPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Regenerated %d core traces from %s", count, seedPath), nil
	})

	// Memory flush/reset tools for testing recall without conversation context
	server.RegisterTool("memory_flush", mcp.ToolDef{
		Description: "Flush the conversation buffer to memory. Clears conversation context but keeps session running. Pending thoughts will be extracted by main process.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		// Force extraction on any pending thoughts in inbox, then clear conversation buffer
		// This simulates buffer expiration for memory testing

		// Read and process any pending thoughts from inbox
		inboxPath := filepath.Join(queuesPath, "inbox.jsonl")
		data, err := os.ReadFile(inboxPath)
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to read inbox: %w", err)
		}

		// Count thoughts processed
		thoughtCount := 0
		if len(data) > 0 {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			for _, line := range lines {
				if line == "" {
					continue
				}
				var msg map[string]any
				if err := json.Unmarshal([]byte(line), &msg); err != nil {
					continue
				}
				// Count thought-type messages
				if subtype, ok := msg["subtype"].(string); ok && subtype == "thought" {
					thoughtCount++
				}
			}
		}

		// Clear conversation buffer file
		bufferPath := filepath.Join(statePath, "buffers.json")
		if err := os.WriteFile(bufferPath, []byte("{}"), 0644); err != nil {
			return "", fmt.Errorf("failed to clear buffer: %w", err)
		}

		// Trigger consolidation in main process
		triggerPath := filepath.Join(statePath, "consolidate.trigger")
		if err := os.WriteFile(triggerPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write consolidation trigger: %v", err)
		}

		log.Printf("Memory flush: cleared conversation buffer, triggered consolidation, %d thoughts pending extraction", thoughtCount)
		return fmt.Sprintf("Memory flushed. Conversation buffer cleared, consolidation triggered. %d thoughts pending.", thoughtCount), nil
	})

	server.RegisterTool("memory_reset", mcp.ToolDef{
		Description: "Reset memory completely. Clears conversation buffer AND kills this Claude session. Use for clean slate testing.",
		Properties:  map[string]mcp.PropDef{},
	}, func(_ any, _ map[string]any) (string, error) {
		// FIRST: Write reset pending flag to prevent new sessions from starting
		// with the old session ID. This must happen BEFORE anything else.
		resetPendingPath := filepath.Join(statePath, "reset.pending")
		if err := os.WriteFile(resetPendingPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write reset pending flag: %v", err)
		}
		log.Printf("Memory reset: set reset.pending flag to block new sessions")

		// Trigger consolidation (main process will handle it)
		triggerPath := filepath.Join(statePath, "consolidate.trigger")
		if err := os.WriteFile(triggerPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write consolidation trigger: %v", err)
		}

		// Signal main process to clear in-memory buffer
		bufferClearPath := filepath.Join(statePath, "buffer.clear")
		if err := os.WriteFile(bufferClearPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write buffer clear trigger: %v", err)
		}

		// Give consolidation and buffer clear a moment to process
		time.Sleep(3 * time.Second)

		// Clear conversation buffer on disk (redundant but ensures clean state)
		// Note: buffer is stored in system/buffers.json, not statePath/buffers.json
		bufferPath := filepath.Join(systemPath, "buffers.json")
		if err := os.WriteFile(bufferPath, []byte("{}"), 0644); err != nil {
			return "", fmt.Errorf("failed to clear buffer: %w", err)
		}

		// Close Claude sessions (this will end the current session)
		cmd := exec.Command("tmux", "list-windows", "-a", "-F", "#{session_name}:#{window_index}:#{window_name}:#{pane_current_command}")
		output, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("failed to list tmux windows: %w", err)
		}

		lines := strings.Split(strings.TrimSpace(string(output)), "\n")
		var closedWindows []string

		for _, line := range lines {
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, ":", 4)
			if len(parts) < 4 {
				continue
			}
			sessionName := parts[0]
			windowIndex := parts[1]
			windowName := parts[2]
			currentCommand := parts[3]

			// Identify Claude sessions
			isVersion := len(currentCommand) > 0 && strings.Count(currentCommand, ".") >= 1 &&
				currentCommand[0] >= '0' && currentCommand[0] <= '9'
			isClaudeCmd := strings.Contains(strings.ToLower(currentCommand), "claude")

			// Check window name pattern
			hasClaudeWindowName := false
			if len(windowName) > 0 {
				dashIdx := strings.LastIndex(windowName, "-")
				if dashIdx > 0 && dashIdx < len(windowName)-1 {
					prefix := windowName[:dashIdx]
					suffix := windowName[dashIdx+1:]
					allLetters := true
					for _, c := range prefix {
						if c < 'a' || c > 'z' {
							allLetters = false
							break
						}
					}
					allDigits := true
					for _, c := range suffix {
						if c < '0' || c > '9' {
							allDigits = false
							break
						}
					}
					hasClaudeWindowName = allLetters && allDigits && len(prefix) > 0 && len(suffix) > 0
				}
			}

			if isVersion || isClaudeCmd || hasClaudeWindowName {
				if windowName == "monitor" || currentCommand == "zsh" || currentCommand == "bash" {
					continue
				}

				target := fmt.Sprintf("%s:%s", sessionName, windowIndex)
				killCmd := exec.Command("tmux", "kill-window", "-t", target)
				if err := killCmd.Run(); err == nil {
					closedWindows = append(closedWindows, windowName)
					log.Printf("Memory reset: closed Claude session %s", windowName)
				}
			}
		}

		// Write reset_session signal to inbox
		// This tells the main bud process to call session.Reset() which generates a new
		// session ID. Old session files are then orphaned and never used again.
		// We do NOT delete session files here because doing so while a tool call is in
		// progress causes a race condition: the tool_result gets written to a new orphaned
		// session file without its corresponding tool_use.
		resetSignal := map[string]any{
			"id":        fmt.Sprintf("signal-%d", time.Now().UnixNano()),
			"type":      "signal",
			"subtype":   "reset_session",
			"content":   "Memory reset requested",
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
		}

		inboxPath := filepath.Join(queuesPath, "inbox.jsonl")
		f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Printf("Memory reset: warning, failed to write reset signal: %v", err)
		} else {
			data, _ := json.Marshal(resetSignal)
			f.WriteString(string(data) + "\n")
			f.Close()
		}

		log.Printf("Memory reset: buffer cleared, %d Claude sessions closed, reset signal sent", len(closedWindows))
		return fmt.Sprintf("Memory reset complete. Buffer cleared. Closed %d Claude sessions: %v. Session will restart fresh.", len(closedWindows), closedWindows), nil
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

// parseDueTime parses various due time formats
// Supports: RFC3339, relative times (2m, 1h, 30min, etc.), and common formats
func parseDueTime(s string) *time.Time {
	now := time.Now()

	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return &t
	}

	// Try common date/time formats
	formats := []string{
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"01/02/2006 15:04",
		"01/02/2006",
		"15:04",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, s, now.Location()); err == nil {
			// For time-only format, use today's date
			if f == "15:04" {
				t = time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
				if t.Before(now) {
					t = t.Add(24 * time.Hour) // tomorrow if time already passed
				}
			}
			return &t
		}
	}

	// Try relative time parsing (e.g., "2m", "1h", "30min", "2 hours")
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "in ")

	// Parse duration-like strings
	var duration time.Duration
	var matched bool

	// Try standard Go duration first
	if d, err := time.ParseDuration(s); err == nil {
		duration = d
		matched = true
	}

	// Try custom patterns: "2 min", "1 hour", "30 minutes"
	if !matched {
		patterns := []struct {
			suffix string
			mult   time.Duration
		}{
			{"minutes", time.Minute},
			{"minute", time.Minute},
			{"mins", time.Minute},
			{"min", time.Minute},
			{"hours", time.Hour},
			{"hour", time.Hour},
			{"hrs", time.Hour},
			{"hr", time.Hour},
			{"days", 24 * time.Hour},
			{"day", 24 * time.Hour},
			{"seconds", time.Second},
			{"second", time.Second},
			{"secs", time.Second},
			{"sec", time.Second},
		}
		for _, p := range patterns {
			if strings.HasSuffix(s, p.suffix) {
				numStr := strings.TrimSpace(strings.TrimSuffix(s, p.suffix))
				if n, err := strconv.Atoi(numStr); err == nil {
					duration = time.Duration(n) * p.mult
					matched = true
					break
				}
			}
		}
	}

	if matched && duration > 0 {
		t := now.Add(duration)
		return &t
	}

	return nil
}

// formatEventsCompact formats a slice of calendar events in a compact, token-efficient format
// Format: "2026-01-16 14:30-14:45 | Event Title" or "2026-01-10 (all day) | Event Title"
// Optionally includes meet link if present
func formatEventsCompact(events []calendar.Event) string {
	if len(events) == 0 {
		return "No events."
	}

	var lines []string
	for _, e := range events {
		line := formatEventCompact(e)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// formatEventCompact formats a single calendar event compactly
func formatEventCompact(e calendar.Event) string {
	var timeStr string
	if e.AllDay {
		timeStr = e.Start.Format("2006-01-02") + " (all day)"
	} else {
		// Format: "2026-01-16 14:30-14:45"
		timeStr = e.Start.Format("2006-01-02 15:04") + "-" + e.End.Format("15:04")
	}

	line := timeStr + " | " + e.Summary

	// Add shortened meet link if present
	if e.MeetLink != "" {
		// Extract just the path from meet.google.com/xxx-xxx-xxx
		meetLink := e.MeetLink
		if idx := strings.Index(meetLink, "meet.google.com/"); idx != -1 {
			meetLink = meetLink[idx:] // "meet.google.com/xxx-xxx-xxx"
		}
		line += " | " + meetLink
	} else if e.Location != "" {
		line += " | " + e.Location
	}

	return line
}
