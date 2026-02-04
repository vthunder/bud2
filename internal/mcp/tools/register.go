package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/activity"
	"github.com/vthunder/bud2/internal/graph"
	"github.com/vthunder/bud2/internal/gtd"
	"github.com/vthunder/bud2/internal/integrations/calendar"
	"github.com/vthunder/bud2/internal/integrations/github"
	"github.com/vthunder/bud2/internal/mcp"
	"github.com/vthunder/bud2/internal/motivation"
	"github.com/vthunder/bud2/internal/reflex"
)

// RegisterAll registers all MCP tools with the given server and dependencies.
func RegisterAll(server *mcp.Server, deps *Dependencies) {
	registerCommunicationTools(server, deps)
	registerMemoryTools(server, deps)
	registerActivityTools(server, deps)
	registerMotivationTools(server, deps)
	registerStateTools(server, deps)

	if deps.GTDStore != nil {
		registerGTDTools(server, deps)
	}
	if deps.ReflexEngine != nil {
		registerReflexTools(server, deps)
	}
	if deps.CalendarClient != nil {
		registerCalendarTools(server, deps)
	}
	if deps.GitHubClient != nil {
		registerGitHubTools(server, deps)
	}
	if deps.MemoryJudge != nil {
		registerEvalTools(server, deps)
	}
}

func registerCommunicationTools(server *mcp.Server, deps *Dependencies) {
	// talk_to_user - send message to Discord
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

		channelID, _ := args["channel_id"].(string)
		if channelID == "" {
			channelID = deps.DefaultChannel
		}
		if channelID == "" {
			return "", fmt.Errorf("channel_id required (none provided and DISCORD_CHANNEL_ID not set)")
		}

		log.Printf("talk_to_user: channel=%s message=%s", channelID, truncate(message, 50))

		// Use direct effector if available
		if deps.SendMessage != nil {
			if err := deps.SendMessage(channelID, message); err != nil {
				return "", fmt.Errorf("failed to send message: %w", err)
			}
			return "Message sent to Discord", nil
		}

		// Fallback to outbox file
		action := map[string]any{
			"id":       fmt.Sprintf("action-%d", time.Now().UnixNano()),
			"effector": "discord",
			"type":     "send_message",
			"payload": map[string]any{
				"channel_id": channelID,
				"content":    message,
			},
		}

		outboxPath := filepath.Join(deps.QueuesPath, "outbox.jsonl")
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
		if err := f.Sync(); err != nil {
			return "", fmt.Errorf("failed to sync outbox: %w", err)
		}

		log.Printf("Action queued: %s", action["id"])
		return "Message queued for sending to Discord", nil
	})

	// signal_done
	server.RegisterTool("signal_done", mcp.ToolDef{
		Description: "Signal that you have finished processing and are ready for new prompts. IMPORTANT: Always call this when you have completed responding to a message or finishing a task. This helps track thinking time and enables autonomous work scheduling.",
		Properties: map[string]mcp.PropDef{
			"session_id":  {Type: "string", Description: "The current session ID (if known)"},
			"summary":     {Type: "string", Description: "Brief summary of what was accomplished (optional)"},
			"memory_eval": {Type: "object", Description: "Memory usefulness ratings as {\"M1\": 5, \"M2\": 1} where 1=not useful, 5=very useful"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		sessionID, _ := args["session_id"].(string)
		summary, _ := args["summary"].(string)
		memoryEval, _ := args["memory_eval"].(map[string]any)

		extra := map[string]any{
			"session_id": sessionID,
		}
		if len(memoryEval) > 0 {
			extra["memory_eval"] = memoryEval
		}

		msg := map[string]any{
			"id":        fmt.Sprintf("signal-%d", time.Now().UnixNano()),
			"type":      "signal",
			"subtype":   "done",
			"content":   summary,
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
			"extra":     extra,
		}

		inboxPath := filepath.Join(deps.QueuesPath, "inbox.jsonl")
		f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open inbox file: %w", err)
		}
		defer f.Close()

		data, _ := json.Marshal(msg)
		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write signal: %w", err)
		}

		if len(memoryEval) > 0 {
			log.Printf("Session done signal: session=%s summary=%s memory_eval=%v", sessionID, truncate(summary, 50), memoryEval)
		} else {
			log.Printf("Session done signal: session=%s summary=%s", sessionID, truncate(summary, 50))
		}
		return "Done signal recorded. Ready for new prompts.", nil
	})

	// save_thought - writes to inbox as a percept (gets stored to memory graph via normal processing)
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

		// Write thought to inbox as a special message type (subtype: "thought")
		// This flows through: inbox -> percept -> episode -> memory graph
		thought := map[string]any{
			"id":        fmt.Sprintf("thought-%d", time.Now().UnixNano()),
			"subtype":   "thought",
			"content":   content,
			"timestamp": time.Now().Format(time.RFC3339),
			"status":    "pending",
		}

		// Append to inbox file
		f, err := os.OpenFile(filepath.Join(deps.QueuesPath, "inbox.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open inbox: %w", err)
		}
		defer f.Close()

		data, err := json.Marshal(thought)
		if err != nil {
			return "", fmt.Errorf("failed to marshal thought: %w", err)
		}

		if _, err := f.WriteString(string(data) + "\n"); err != nil {
			return "", fmt.Errorf("failed to write thought to inbox: %w", err)
		}

		log.Printf("Saved thought to inbox: %s", truncate(content, 50))
		return "Thought saved to memory. It will be consolidated with other memories over time.", nil
	})
}

func registerMemoryTools(server *mcp.Server, deps *Dependencies) {
	// list_traces
	server.RegisterTool("list_traces", mcp.ToolDef{
		Description: "List all memory traces with their IDs, content preview, and core status. Use this to discover trace IDs before marking them as core.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		traces, err := deps.GraphDB.GetAllTraces()
		if err != nil {
			return "", fmt.Errorf("failed to load traces: %w", err)
		}

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

	// mark_core
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

		isCore := true
		if val, ok := args["is_core"].(bool); ok {
			isCore = val
		}

		if err := deps.GraphDB.SetTraceCore(traceID, isCore); err != nil {
			return "", err
		}

		action := "marked as core"
		if !isCore {
			action = "unmarked as core"
		}
		log.Printf("Trace %s %s", traceID, action)
		return fmt.Sprintf("Trace %s %s.", traceID, action), nil
	})

	// create_core
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

		trace := &graph.Trace{
			ID:           fmt.Sprintf("core-%d", time.Now().UnixNano()),
			Summary:      content,
			Activation:   1.0,
			Strength:     100,
			IsCore:       true,
			CreatedAt:    time.Now(),
			LastAccessed: time.Now(),
		}

		if err := deps.GraphDB.AddTrace(trace); err != nil {
			return "", fmt.Errorf("failed to create trace: %w", err)
		}

		log.Printf("Created core trace: %s", truncate(content, 50))
		return fmt.Sprintf("Created core trace %s.", trace.ID), nil
	})

	// search_memory (if embedder is available)
	if deps.StateInspector != nil && deps.Embedder != nil {
		server.RegisterTool("search_memory", mcp.ToolDef{
			Description: "Search memory traces by semantic similarity. Returns traces most relevant to the query, optionally biased by current activation levels.",
			Properties: map[string]mcp.PropDef{
				"query":       {Type: "string", Description: "The search query"},
				"limit":       {Type: "number", Description: "Maximum results to return (default 10)"},
				"use_context": {Type: "boolean", Description: "If true, bias results toward currently activated traces (default true)"},
			},
			Required: []string{"query"},
		}, func(ctx any, args map[string]any) (string, error) {
			query, ok := args["query"].(string)
			if !ok || query == "" {
				return "", fmt.Errorf("query is required")
			}

			limit := 10
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			useContext := true
			if uc, ok := args["use_context"].(bool); ok {
				useContext = uc
			}

			results, err := deps.StateInspector.SearchMemoryWithContext(query, limit, useContext)
			if err != nil {
				return "", fmt.Errorf("search failed: %w", err)
			}

			data, _ := json.MarshalIndent(results, "", "  ")
			return string(data), nil
		})

		server.RegisterTool("get_trace_context", mcp.ToolDef{
			Description: "Get detailed context for a specific memory trace, including source episodes and linked entities.",
			Properties: map[string]mcp.PropDef{
				"trace_id": {Type: "string", Description: "The trace ID to get context for"},
			},
			Required: []string{"trace_id"},
		}, func(ctx any, args map[string]any) (string, error) {
			traceID, ok := args["trace_id"].(string)
			if !ok || traceID == "" {
				return "", fmt.Errorf("trace_id is required")
			}

			context, err := deps.StateInspector.GetTraceContext(traceID)
			if err != nil {
				return "", err
			}

			data, _ := json.MarshalIndent(context, "", "  ")
			return string(data), nil
		})

		server.RegisterTool("query_trace", mcp.ToolDef{
			Description: "Query a specific trace for detailed information. Returns the trace with its source episodes.",
			Properties: map[string]mcp.PropDef{
				"trace_id": {Type: "string", Description: "The trace ID to query"},
				"question": {Type: "string", Description: "Optional question to answer about this trace"},
			},
			Required: []string{"trace_id"},
		}, func(ctx any, args map[string]any) (string, error) {
			traceID, ok := args["trace_id"].(string)
			if !ok || traceID == "" {
				return "", fmt.Errorf("trace_id is required")
			}

			question, _ := args["question"].(string)

			result, err := deps.StateInspector.QueryTrace(traceID, question)
			if err != nil {
				return "", err
			}

			data, _ := json.MarshalIndent(result, "", "  ")
			return string(data), nil
		})
	}
}

func registerActivityTools(server *mcp.Server, deps *Dependencies) {
	// journal_log
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

		entryType, _ := args["type"].(string)
		if entryType == "" {
			entryType = "observation"
		}

		deps.ActivityLog.Log(activity.Entry{
			Type:    activity.Type(entryType),
			Summary: summary,
			Data: map[string]any{
				"context":   context,
				"reasoning": reasoning,
				"outcome":   outcome,
			},
		})

		log.Printf("Journal logged: [%s] %s", entryType, truncate(summary, 50))
		return "Journal entry recorded.", nil
	})

	// journal_recent
	server.RegisterTool("journal_recent", mcp.ToolDef{
		Description: "Get recent journal entries. Use this to review what you've been doing and why.",
		Properties: map[string]mcp.PropDef{
			"count": {Type: "number", Description: "Number of entries to return (default 20)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		count := 20
		if c, ok := args["count"].(float64); ok && c > 0 {
			count = int(c)
		}

		entries, err := deps.ActivityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get recent entries: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// journal_today
	server.RegisterTool("journal_today", mcp.ToolDef{
		Description: "Get today's journal entries. Use this to answer 'what did you do today?'",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		entries, err := deps.ActivityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's entries: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_recent
	server.RegisterTool("activity_recent", mcp.ToolDef{
		Description: "Get recent activity entries from the log.",
		Properties: map[string]mcp.PropDef{
			"count": {Type: "number", Description: "Number of entries to return (default 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		count := 50
		if c, ok := args["count"].(float64); ok && c > 0 {
			count = int(c)
		}

		entries, err := deps.ActivityLog.Recent(count)
		if err != nil {
			return "", fmt.Errorf("failed to get recent activity: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_today
	server.RegisterTool("activity_today", mcp.ToolDef{
		Description: "Get today's activity entries.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		entries, err := deps.ActivityLog.Today()
		if err != nil {
			return "", fmt.Errorf("failed to get today's activity: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_search
	server.RegisterTool("activity_search", mcp.ToolDef{
		Description: "Search activity entries by text.",
		Properties: map[string]mcp.PropDef{
			"query": {Type: "string", Description: "Text to search for"},
			"limit": {Type: "number", Description: "Maximum entries to return (default 100)"},
		},
		Required: []string{"query"},
	}, func(ctx any, args map[string]any) (string, error) {
		query, ok := args["query"].(string)
		if !ok || query == "" {
			return "", fmt.Errorf("query is required")
		}

		limit := 100
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		entries, err := deps.ActivityLog.Search(query, limit)
		if err != nil {
			return "", fmt.Errorf("search failed: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})

	// activity_by_type
	server.RegisterTool("activity_by_type", mcp.ToolDef{
		Description: "Filter activity entries by event type.",
		Properties: map[string]mcp.PropDef{
			"type":  {Type: "string", Description: "Event type: input, reflex, reflex_pass, executive_wake, executive_done, action, decision, error"},
			"limit": {Type: "number", Description: "Maximum entries to return (default 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		eventType, _ := args["type"].(string)

		limit := 50
		if l, ok := args["limit"].(float64); ok && l > 0 {
			limit = int(l)
		}

		entries, err := deps.ActivityLog.ByType(activity.Type(eventType), limit)
		if err != nil {
			return "", fmt.Errorf("failed to filter by type: %w", err)
		}

		data, _ := json.MarshalIndent(entries, "", "  ")
		return string(data), nil
	})
}

func registerMotivationTools(server *mcp.Server, deps *Dependencies) {
	if deps.TaskStore == nil || deps.IdeaStore == nil {
		return
	}

	// add_bud_task
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
		taskDesc, ok := args["task"].(string)
		if !ok || taskDesc == "" {
			return "", fmt.Errorf("task is required")
		}

		priority := 2
		if p, ok := args["priority"].(float64); ok {
			priority = int(p)
		}

		task := &motivation.Task{
			ID:       fmt.Sprintf("task-%d", time.Now().UnixNano()),
			Task:     taskDesc,
			Priority: priority,
			Status:   "pending",
		}

		if taskContext, ok := args["context"].(string); ok {
			task.Context = taskContext
		}

		if due, ok := args["due"].(string); ok && due != "" {
			if t, err := time.Parse(time.RFC3339, due); err == nil {
				task.Due = &t
			}
		}

		deps.TaskStore.Add(task)
		if err := deps.TaskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		log.Printf("Added task: %s", truncate(taskDesc, 50))
		return fmt.Sprintf("Task added: %s", task.ID), nil
	})

	// list_bud_tasks
	server.RegisterTool("list_bud_tasks", mcp.ToolDef{
		Description: "List pending Bud tasks. Use this to see what you've committed to do.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		deps.TaskStore.Load()
		tasks := deps.TaskStore.GetPending()
		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})

	// complete_bud_task
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

		// Get task first to return its description
		task := deps.TaskStore.Get(taskID)
		if task == nil {
			return "", fmt.Errorf("task not found: %s", taskID)
		}
		taskDesc := task.Task

		deps.TaskStore.Complete(taskID)

		if err := deps.TaskStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Completed task: %s", taskID)
		return fmt.Sprintf("Task completed: %s", taskDesc), nil
	})

	// add_idea
	server.RegisterTool("add_idea", mcp.ToolDef{
		Description: "Save an idea for later exploration. Ideas are things you want to learn or think about when idle.",
		Properties: map[string]mcp.PropDef{
			"idea":       {Type: "string", Description: "The idea or topic to explore"},
			"sparked_by": {Type: "string", Description: "What triggered this idea (optional)"},
			"priority":   {Type: "number", Description: "Interest level: 1=highest, 2=medium, 3=low (default 2)"},
		},
		Required: []string{"idea"},
	}, func(ctx any, args map[string]any) (string, error) {
		ideaDesc, ok := args["idea"].(string)
		if !ok || ideaDesc == "" {
			return "", fmt.Errorf("idea is required")
		}

		priority := 2
		if p, ok := args["priority"].(float64); ok {
			priority = int(p)
		}

		idea := &motivation.Idea{
			ID:       fmt.Sprintf("idea-%d", time.Now().UnixNano()),
			Idea:     ideaDesc,
			Priority: priority,
			Added:    time.Now(),
		}

		if sparkedBy, ok := args["sparked_by"].(string); ok {
			idea.SparkBy = sparkedBy
		}

		deps.IdeaStore.Add(idea)
		if err := deps.IdeaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save idea: %w", err)
		}

		log.Printf("Added idea: %s", truncate(ideaDesc, 50))
		return fmt.Sprintf("Idea saved: %s", idea.ID), nil
	})

	// list_ideas
	server.RegisterTool("list_ideas", mcp.ToolDef{
		Description: "List unexplored ideas. Use this to find something to think about during idle time.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		deps.IdeaStore.Load()
		ideas := deps.IdeaStore.GetUnexplored()
		data, _ := json.MarshalIndent(ideas, "", "  ")
		return string(data), nil
	})

	// explore_idea
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

		notes, _ := args["notes"].(string)

		idea := deps.IdeaStore.Get(ideaID)
		if idea == nil {
			return "", fmt.Errorf("idea not found: %s", ideaID)
		}
		deps.IdeaStore.MarkExplored(ideaID, notes)

		if err := deps.IdeaStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save: %w", err)
		}

		log.Printf("Explored idea: %s", ideaID)
		return fmt.Sprintf("Idea marked as explored: %s", idea.Idea), nil
	})
}

func registerStateTools(server *mcp.Server, deps *Dependencies) {
	if deps.StateInspector == nil {
		return
	}

	// state_summary
	server.RegisterTool("state_summary", mcp.ToolDef{
		Description: "Get summary of all state components (traces, percepts, threads, logs, queues).",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		summary, err := deps.StateInspector.Summary()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(summary, "", "  ")
		return string(data), nil
	})

	// state_health
	server.RegisterTool("state_health", mcp.ToolDef{
		Description: "Run health checks on state and get recommendations for cleanup.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		health, err := deps.StateInspector.Health()
		if err != nil {
			return "", err
		}
		data, _ := json.MarshalIndent(health, "", "  ")
		return string(data), nil
	})

	// state_traces
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
			traces, err := deps.StateInspector.ListTraces()
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
			trace, err := deps.StateInspector.GetTrace(id)
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
			if err := deps.StateInspector.DeleteTrace(id); err != nil {
				return "", err
			}
			return fmt.Sprintf("Deleted trace: %s", id), nil

		case "clear":
			clearCore, _ := args["clear_core"].(bool)
			count, err := deps.StateInspector.ClearTraces(clearCore)
			if err != nil {
				return "", err
			}
			if clearCore {
				return fmt.Sprintf("Cleared %d core traces", count), nil
			}
			return fmt.Sprintf("Cleared %d non-core traces", count), nil

		case "regen_core":
			seedPath := "seed/core_seed.md"
			count, err := deps.StateInspector.RegenCore(seedPath)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Regenerated %d core traces", count), nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// state_sessions
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
			sessions, err := deps.StateInspector.ListSessions()
			if err != nil {
				return "", err
			}
			data, _ := json.MarshalIndent(sessions, "", "  ")
			return string(data), nil

		case "clear":
			if err := deps.StateInspector.ClearSessions(); err != nil {
				return "", err
			}
			return "Cleared sessions", nil

		default:
			return "", fmt.Errorf("unknown action: %s", action)
		}
	})

	// state_regen_core
	server.RegisterTool("state_regen_core", mcp.ToolDef{
		Description: "Regenerate core identity traces from core_seed.md. Clears existing core traces first.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		seedPath := "seed/core_seed.md"
		count, err := deps.StateInspector.RegenCore(seedPath)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Regenerated %d core traces from %s", count, seedPath), nil
	})

	// memory_flush
	server.RegisterTool("memory_flush", mcp.ToolDef{
		Description: "Flush the conversation buffer to memory. Clears conversation context but keeps session running. Pending thoughts will be extracted by main process.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		// Trigger consolidation in main process
		triggerPath := filepath.Join(deps.StatePath, "consolidate.trigger")
		if err := os.WriteFile(triggerPath, []byte(time.Now().Format(time.RFC3339)), 0644); err != nil {
			log.Printf("Warning: failed to write consolidation trigger: %v", err)
		}

		log.Printf("Memory flush: triggered consolidation")
		return "Memory flushed. Consolidation triggered.", nil
	})
}

func registerGTDTools(server *mcp.Server, deps *Dependencies) {
	// gtd_add
	server.RegisterTool("gtd_add", mcp.ToolDef{
		Description: "Add a task to the user's GTD system. Quick capture to inbox by default, or specify when/project to place it directly.",
		Properties: map[string]mcp.PropDef{
			"title":   {Type: "string", Description: "Task title (what needs to be done)"},
			"notes":   {Type: "string", Description: "Additional notes or context for the task (optional)"},
			"when":    {Type: "string", Description: "When to do it: inbox (default), today, anytime, someday, or YYYY-MM-DD date"},
			"project": {Type: "string", Description: "Project ID to add task to (optional)"},
			"area":    {Type: "string", Description: "Area ID for the task (optional, only if not in a project)"},
			"heading": {Type: "string", Description: "Heading name within the project (requires project)"},
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

		if notes, ok := args["notes"].(string); ok {
			task.Notes = notes
		}
		if when, ok := args["when"].(string); ok {
			task.When = when
		}
		if project, ok := args["project"].(string); ok {
			task.Project = project
		}
		if area, ok := args["area"].(string); ok {
			task.Area = area
		}
		if heading, ok := args["heading"].(string); ok {
			task.Heading = heading
		}

		deps.GTDStore.AddTask(task)
		if err := deps.GTDStore.Save(); err != nil {
			return "", fmt.Errorf("failed to save task: %w", err)
		}

		log.Printf("GTD task added: %s", truncate(title, 50))
		return fmt.Sprintf("Task added to GTD: %s (ID: %s)", title, task.ID), nil
	})

	// gtd_list
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
		projectID, _ := args["project"].(string)
		areaID, _ := args["area"].(string)

		tasks := deps.GTDStore.GetTasks(when, projectID, areaID)

		// Filter by status if specified
		if status, ok := args["status"].(string); ok && status != "" && status != "all" {
			var filtered []gtd.Task
			for _, t := range tasks {
				if t.Status == status {
					filtered = append(filtered, t)
				}
			}
			tasks = filtered
		}

		data, _ := json.MarshalIndent(tasks, "", "  ")
		return string(data), nil
	})

	// gtd_complete
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

		if err := deps.GTDStore.CompleteTask(id); err != nil {
			return "", fmt.Errorf("failed to complete task: %w", err)
		}

		log.Printf("GTD task completed: %s", id)
		return fmt.Sprintf("Task completed: %s", id), nil
	})
}

func registerReflexTools(server *mcp.Server, deps *Dependencies) {
	// create_reflex
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

		r := &reflex.Reflex{
			Name: name,
		}

		if desc, ok := args["description"].(string); ok {
			r.Description = desc
		}
		if pattern, ok := args["pattern"].(string); ok {
			r.Trigger.Pattern = pattern
		}
		if extract, ok := args["extract"].([]any); ok {
			for _, e := range extract {
				if s, ok := e.(string); ok {
					r.Trigger.Extract = append(r.Trigger.Extract, s)
				}
			}
		}

		// Parse pipeline
		if pipeline, ok := args["pipeline"].([]any); ok {
			for _, step := range pipeline {
				if stepMap, ok := step.(map[string]any); ok {
					pipelineStep := reflex.PipelineStep{
						Params: make(map[string]any),
					}
					if a, ok := stepMap["action"].(string); ok {
						pipelineStep.Action = a
					}
					if input, ok := stepMap["input"].(string); ok {
						pipelineStep.Input = input
					}
					if output, ok := stepMap["output"].(string); ok {
						pipelineStep.Output = output
					}
					// Copy remaining params
					for k, v := range stepMap {
						if k != "action" && k != "input" && k != "output" {
							pipelineStep.Params[k] = v
						}
					}
					r.Pipeline = append(r.Pipeline, pipelineStep)
				}
			}
		}

		if err := deps.ReflexEngine.SaveReflex(r); err != nil {
			return "", fmt.Errorf("failed to create reflex: %w", err)
		}

		log.Printf("Created reflex: %s", name)
		return fmt.Sprintf("Reflex created: %s", name), nil
	})

	// list_reflexes
	server.RegisterTool("list_reflexes", mcp.ToolDef{
		Description: "List all defined reflexes.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		reflexes := deps.ReflexEngine.List()
		data, _ := json.MarshalIndent(reflexes, "", "  ")
		return string(data), nil
	})

	// delete_reflex
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

		if err := deps.ReflexEngine.Delete(name); err != nil {
			return "", fmt.Errorf("failed to delete reflex: %w", err)
		}

		log.Printf("Deleted reflex: %s", name)
		return fmt.Sprintf("Reflex deleted: %s", name), nil
	})
}

func registerCalendarTools(server *mcp.Server, deps *Dependencies) {
	// calendar_today
	server.RegisterTool("calendar_today", mcp.ToolDef{
		Description: "Get today's calendar events. Returns compact format by default (one line per event).",
		Properties: map[string]mcp.PropDef{
			"verbose": {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		events, err := deps.CalendarClient.GetTodayEvents(context.Background())
		if err != nil {
			return "", fmt.Errorf("failed to get today's events: %w", err)
		}

		if len(events) == 0 {
			return "No events scheduled for today.", nil
		}

		if verbose, _ := args["verbose"].(bool); verbose {
			data, _ := json.MarshalIndent(events, "", "  ")
			return string(data), nil
		}

		return formatEventsCompact(events), nil
	})

	// calendar_upcoming
	server.RegisterTool("calendar_upcoming", mcp.ToolDef{
		Description: "Get upcoming calendar events within a time window. Returns compact format by default.",
		Properties: map[string]mcp.PropDef{
			"duration":    {Type: "string", Description: "Time window to look ahead (e.g., '24h', '7d'). Default: 24h"},
			"max_results": {Type: "number", Description: "Maximum number of events to return. Default: 20"},
			"verbose":     {Type: "boolean", Description: "If true, return full JSON with all event details. Default: false (compact format)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
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

		events, err := deps.CalendarClient.GetUpcomingEvents(context.Background(), duration, maxResults)
		if err != nil {
			return "", fmt.Errorf("failed to get upcoming events: %w", err)
		}

		if len(events) == 0 {
			return fmt.Sprintf("No events in the next %s.", durationStr), nil
		}

		if verbose, _ := args["verbose"].(bool); verbose {
			data, _ := json.MarshalIndent(events, "", "  ")
			return string(data), nil
		}

		return formatEventsCompact(events), nil
	})
}

func registerGitHubTools(server *mcp.Server, deps *Dependencies) {
	// github_list_projects
	server.RegisterTool("github_list_projects", mcp.ToolDef{
		Description: "List all GitHub Projects v2 in the configured organization. Returns project number, title, and URL.",
		Properties:  map[string]mcp.PropDef{},
	}, func(ctx any, args map[string]any) (string, error) {
		projects, err := deps.GitHubClient.ListProjects()
		if err != nil {
			return "", fmt.Errorf("failed to list projects: %w", err)
		}

		data, _ := json.MarshalIndent(projects, "", "  ")
		return string(data), nil
	})

	// github_project_items
	server.RegisterTool("github_project_items", mcp.ToolDef{
		Description: "Query items from a GitHub Project. Returns compact list format by default. Use status filter for views like backlog/sprint.",
		Properties: map[string]mcp.PropDef{
			"project":   {Type: "number", Description: "The project number"},
			"status":    {Type: "string", Description: "Filter by Status field (e.g., 'Backlog', 'In Progress', 'Done')"},
			"sprint":    {Type: "string", Description: "Filter by Sprint (e.g., 'Sprint 65'). Use 'backlog' for items with no sprint assigned."},
			"priority":  {Type: "string", Description: "Filter by Priority field (e.g., 'P0', 'P1', 'P2')"},
			"team_area": {Type: "string", Description: "Filter by Team / Area field (e.g., 'SE', 'Docs', 'Nexus', 'DevOps')"},
			"max_items": {Type: "number", Description: "Maximum items to return (default 100)"},
			"verbose":   {Type: "boolean", Description: "If true, return full JSON with all fields. Default: false (compact format)"},
		},
		Required: []string{"project"},
	}, func(ctx any, args map[string]any) (string, error) {
		projectNum, ok := args["project"].(float64)
		if !ok {
			return "", fmt.Errorf("project number is required")
		}

		params := github.QueryItemsParams{
			ProjectNumber: int(projectNum),
		}
		if status, ok := args["status"].(string); ok {
			params.Status = status
		}
		if sprint, ok := args["sprint"].(string); ok {
			params.Sprint = sprint
		}
		if priority, ok := args["priority"].(string); ok {
			params.Priority = priority
		}
		if teamArea, ok := args["team_area"].(string); ok {
			params.TeamArea = teamArea
		}
		if n, ok := args["max_items"].(float64); ok && n > 0 {
			params.MaxItems = int(n)
		}

		items, err := deps.GitHubClient.QueryItems(params)
		if err != nil {
			return "", fmt.Errorf("failed to get project items: %w", err)
		}

		data, _ := json.MarshalIndent(items, "", "  ")
		return string(data), nil
	})
}

func registerEvalTools(server *mcp.Server, deps *Dependencies) {
	activityPath := filepath.Join(deps.SystemPath, "activity.jsonl")

	// memory_judge_sample
	server.RegisterTool("memory_judge_sample", mcp.ToolDef{
		Description: "Run independent evaluation of recent memory retrievals. Compares LLM judge ratings against self-eval to detect bias.",
		Properties: map[string]mcp.PropDef{
			"sample_size": {Type: "number", Description: "Number of memories to evaluate (default 20, max 50)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		sampleSize := 20
		if n, ok := args["sample_size"].(float64); ok && n > 0 {
			sampleSize = int(n)
			if sampleSize > 50 {
				sampleSize = 50
			}
		}

		report, err := deps.MemoryJudge.EvaluateSample(activityPath, sampleSize)
		if err != nil {
			return "", fmt.Errorf("evaluation failed: %w", err)
		}

		summary := fmt.Sprintf(`Memory Judge Report
==================
Sample size: %d
Self-eval avg: %.2f
Judge avg: %.2f
Bias (judge - self): %.2f
Correlation: %.2f
Agreement (±1 point): %.0f%%

Outliers (|diff| >= 2): %d`,
			report.SampleSize,
			report.SelfAvg,
			report.JudgeAvg,
			report.Bias,
			report.Correlation,
			report.Agreement*100,
			len(report.Outliers))

		if len(report.Outliers) > 0 {
			summary += "\n\nOutlier details:"
			for _, o := range report.Outliers {
				summary += fmt.Sprintf("\n- Self=%d Judge=%d: %s", o.SelfRating, o.JudgeRating, o.Memory)
			}
		}

		log.Printf("Memory judge: evaluated %d samples, bias=%.2f, correlation=%.2f", report.SampleSize, report.Bias, report.Correlation)
		return summary, nil
	})
}

// Helper functions

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatEventsCompact(events []calendar.Event) string {
	var lines []string
	for _, e := range events {
		line := fmt.Sprintf("%s - %s: %s", e.Start.Format("15:04"), e.End.Format("15:04"), e.Summary)
		if e.Location != "" {
			line += fmt.Sprintf(" @ %s", e.Location)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
