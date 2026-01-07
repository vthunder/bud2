package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

// Server implements an MCP server over stdio
type Server struct {
	// Tool handlers
	handlers map[string]ToolHandler

	// Context passed to handlers
	context any

	reader *bufio.Reader
	writer io.Writer
}

// ToolHandler handles a tool call
type ToolHandler func(ctx any, args map[string]any) (string, error)

// NewServer creates a new MCP server
func NewServer() *Server {
	return &Server{
		handlers: make(map[string]ToolHandler),
		reader:   bufio.NewReader(os.Stdin),
		writer:   os.Stdout,
	}
}

// SetContext sets the context passed to tool handlers
func (s *Server) SetContext(ctx any) {
	s.context = ctx
}

// RegisterTool registers a tool handler
func (s *Server) RegisterTool(name string, handler ToolHandler) {
	s.handlers[name] = handler
}

// JSON-RPC types
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP types
type initializeParams struct {
	ProtocolVersion string     `json:"protocolVersion"`
	ClientInfo      clientInfo `json:"clientInfo"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	ServerInfo      serverInfo   `json:"serverInfo"`
	Capabilities    capabilities `json:"capabilities"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type capabilities struct {
	Tools *toolsCapability `json:"tools,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type toolsListResult struct {
	Tools []toolDefinition `json:"tools"`
}

type toolDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type toolsCallResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Run starts the MCP server (blocking)
func (s *Server) Run() error {
	log.Println("[mcp] Server starting...")

	for {
		line, err := s.reader.ReadString('\n')
		if err == io.EOF {
			log.Println("[mcp] EOF received, shutting down")
			return nil
		}
		if err != nil {
			return fmt.Errorf("read error: %w", err)
		}

		if line == "" || line == "\n" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			log.Printf("[mcp] Failed to parse request: %v", err)
			continue
		}

		log.Printf("[mcp] Received: %s (id=%v)", req.Method, req.ID)

		resp := s.handleRequest(req)
		if resp != nil {
			s.sendResponse(resp)
		}
	}
}

func (s *Server) handleRequest(req jsonRPCRequest) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)
	case "initialized":
		// Notification, no response needed
		log.Println("[mcp] Client initialized")
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	default:
		log.Printf("[mcp] Unknown method: %s", req.Method)
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

func (s *Server) handleInitialize(req jsonRPCRequest) *jsonRPCResponse {
	var params initializeParams
	if req.Params != nil {
		json.Unmarshal(req.Params, &params)
	}

	log.Printf("[mcp] Initialize from %s %s (protocol %s)",
		params.ClientInfo.Name, params.ClientInfo.Version, params.ProtocolVersion)

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo: serverInfo{
				Name:    "bud2",
				Version: "0.1.0",
			},
			Capabilities: capabilities{
				Tools: &toolsCapability{},
			},
		},
	}
}

func (s *Server) handleToolsList(req jsonRPCRequest) *jsonRPCResponse {
	tools := []toolDefinition{
		{
			Name:        "talk_to_user",
			Description: "Send a message to the user via Discord. Use this to respond to questions, share observations, ask clarifying questions, or give status updates.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"message": {
						Type:        "string",
						Description: "The message to send to the user",
					},
					"channel_id": {
						Type:        "string",
						Description: "The Discord channel ID to send to. Optional - if not provided, uses the default channel from DISCORD_CHANNEL_ID.",
					},
				},
				Required: []string{"message"},
			},
		},
		{
			Name:        "list_traces",
			Description: "List all memory traces with their IDs, content preview, and core status. Use this to discover trace IDs before marking them as core.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
		{
			Name:        "mark_core",
			Description: "Mark a memory trace as core (part of identity) or remove core status. Core traces are always included in prompts and define Bud's identity.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"trace_id": {
						Type:        "string",
						Description: "The ID of the trace to mark as core",
					},
					"is_core": {
						Type:        "boolean",
						Description: "Whether to mark as core (true) or remove core status (false). Defaults to true.",
					},
				},
				Required: []string{"trace_id"},
			},
		},
		{
			Name:        "save_thought",
			Description: "Save a thought or observation to memory. Use this to remember decisions, observations, or anything worth recalling later. These get consolidated with other memories over time.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"content": {
						Type:        "string",
						Description: "The thought or observation to save (e.g., 'User prefers morning check-ins')",
					},
				},
				Required: []string{"content"},
			},
		},
		{
			Name:        "create_core",
			Description: "Create a new core identity trace directly. Use this to add new identity information that should always be present.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"content": {
						Type:        "string",
						Description: "The content of the core trace (e.g., 'I am Bud, a helpful assistant')",
					},
				},
				Required: []string{"content"},
			},
		},
		{
			Name:        "signal_done",
			Description: "Signal that you have finished processing and are ready for new prompts. IMPORTANT: Always call this when you have completed responding to a message or finishing a task. This helps track thinking time and enables autonomous work scheduling.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"session_id": {
						Type:        "string",
						Description: "The current session ID (if known)",
					},
					"summary": {
						Type:        "string",
						Description: "Brief summary of what was accomplished (optional)",
					},
				},
			},
		},
		{
			Name:        "journal_log",
			Description: "Log a decision, action, or observation to the journal for observability. Use this to record your reasoning, decisions made, and actions taken. Helps answer 'what did you do today?' and 'why did you do that?'",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"type": {
						Type:        "string",
						Description: "Entry type: 'decision', 'impulse', 'reflex', 'exploration', 'action', or 'observation'",
					},
					"summary": {
						Type:        "string",
						Description: "Brief description of what happened",
					},
					"context": {
						Type:        "string",
						Description: "What prompted this (optional)",
					},
					"reasoning": {
						Type:        "string",
						Description: "Why this decision was made (optional)",
					},
					"outcome": {
						Type:        "string",
						Description: "What resulted from this (optional)",
					},
				},
				Required: []string{"summary"},
			},
		},
		{
			Name:        "journal_recent",
			Description: "Get recent journal entries. Use this to review what you've been doing and why.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"count": {
						Type:        "number",
						Description: "Number of entries to return (default 20)",
					},
				},
			},
		},
		{
			Name:        "journal_today",
			Description: "Get today's journal entries. Use this to answer 'what did you do today?'",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
		{
			Name:        "add_bud_task",
			Description: "Add a task (Bud's commitment) to your task queue. Use this to track things you've committed to do.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"task": {
						Type:        "string",
						Description: "What you need to do",
					},
					"context": {
						Type:        "string",
						Description: "Why this task exists (optional)",
					},
					"priority": {
						Type:        "number",
						Description: "Priority level: 1=highest, 2=medium, 3=low (default 2)",
					},
					"due": {
						Type:        "string",
						Description: "Due date/time in RFC3339 format (optional)",
					},
				},
				Required: []string{"task"},
			},
		},
		{
			Name:        "list_bud_tasks",
			Description: "List pending Bud tasks. Use this to see what you've committed to do.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
		{
			Name:        "complete_bud_task",
			Description: "Mark a Bud task as complete.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"task_id": {
						Type:        "string",
						Description: "ID of the task to complete",
					},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "add_idea",
			Description: "Save an idea for later exploration. Ideas are things you want to learn or think about when idle.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"idea": {
						Type:        "string",
						Description: "The idea or topic to explore",
					},
					"sparked_by": {
						Type:        "string",
						Description: "What triggered this idea (optional)",
					},
					"priority": {
						Type:        "number",
						Description: "Interest level: 1=highest, 2=medium, 3=low (default 2)",
					},
				},
				Required: []string{"idea"},
			},
		},
		{
			Name:        "list_ideas",
			Description: "List unexplored ideas. Use this to find something to think about during idle time.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
		{
			Name:        "explore_idea",
			Description: "Mark an idea as explored, with notes about what you learned.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"idea_id": {
						Type:        "string",
						Description: "ID of the idea that was explored",
					},
					"notes": {
						Type:        "string",
						Description: "What you learned or discovered (optional)",
					},
				},
				Required: []string{"idea_id"},
			},
		},
		{
			Name:        "create_reflex",
			Description: "Create a new reflex (automated response). Reflexes run without waking the executive for pattern-matched inputs.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"name": {
						Type:        "string",
						Description: "Unique name for the reflex",
					},
					"description": {
						Type:        "string",
						Description: "What this reflex does",
					},
					"pattern": {
						Type:        "string",
						Description: "Regex pattern to match (use capture groups for extraction)",
					},
					"extract": {
						Type:        "array",
						Description: "Names for captured groups (e.g., [\"url\", \"title\"])",
					},
					"pipeline": {
						Type:        "array",
						Description: "Array of action steps: [{action, input, output, ...params}]",
					},
				},
				Required: []string{"name", "pipeline"},
			},
		},
		{
			Name:        "list_reflexes",
			Description: "List all defined reflexes.",
			InputSchema: inputSchema{
				Type:       "object",
				Properties: map[string]property{},
			},
		},
		{
			Name:        "delete_reflex",
			Description: "Delete a reflex by name.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"name": {
						Type:        "string",
						Description: "Name of the reflex to delete",
					},
				},
				Required: []string{"name"},
			},
		},
		// Notion tools
		{
			Name:        "notion_search",
			Description: "Search Notion for pages and databases by text query. Returns titles and IDs.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"query": {
						Type:        "string",
						Description: "Text to search for in page/database titles and content",
					},
					"filter": {
						Type:        "string",
						Description: "Filter by type: 'page' or 'database' (optional)",
					},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "notion_get_page",
			Description: "Get a Notion page by ID. Returns the page properties.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"page_id": {
						Type:        "string",
						Description: "The Notion page ID (UUID format)",
					},
				},
				Required: []string{"page_id"},
			},
		},
		{
			Name:        "notion_get_database",
			Description: "Get a Notion database schema by ID. Returns property definitions including select/status options.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"database_id": {
						Type:        "string",
						Description: "The Notion database ID (UUID format)",
					},
				},
				Required: []string{"database_id"},
			},
		},
		{
			Name:        "notion_query_database",
			Description: "Query a Notion database with optional filter and sort. Returns matching pages with their properties.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"database_id": {
						Type:        "string",
						Description: "The Notion database ID (UUID format)",
					},
					"filter": {
						Type:        "string",
						Description: "JSON filter object (optional). Example: {\"property\": \"Status\", \"status\": {\"equals\": \"In Progress\"}}",
					},
					"sort_property": {
						Type:        "string",
						Description: "Property name to sort by (optional)",
					},
					"sort_direction": {
						Type:        "string",
						Description: "Sort direction: 'ascending' or 'descending' (default: descending)",
					},
				},
				Required: []string{"database_id"},
			},
		},
		// GTD tools (user's tasks, not Bud's commitments)
		{
			Name:        "gtd_add",
			Description: "Add a task to the user's GTD system. Quick capture to inbox by default, or specify when/project to place it directly.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"title": {
						Type:        "string",
						Description: "Task title (what needs to be done)",
					},
					"notes": {
						Type:        "string",
						Description: "Additional notes or context for the task (optional)",
					},
					"when": {
						Type:        "string",
						Description: "When to do it: inbox (default), today, anytime, someday, or YYYY-MM-DD date",
					},
					"project": {
						Type:        "string",
						Description: "Project ID to add task to (optional)",
					},
					"heading": {
						Type:        "string",
						Description: "Heading name within the project (requires project)",
					},
					"area": {
						Type:        "string",
						Description: "Area ID for the task (optional, only if not in a project)",
					},
				},
				Required: []string{"title"},
			},
		},
		{
			Name:        "gtd_list",
			Description: "List tasks from the user's GTD system with optional filters.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]property{
					"when": {
						Type:        "string",
						Description: "Filter by when: inbox, today, anytime, someday, or a specific YYYY-MM-DD date",
					},
					"project": {
						Type:        "string",
						Description: "Filter by project ID",
					},
					"area": {
						Type:        "string",
						Description: "Filter by area ID",
					},
					"status": {
						Type:        "string",
						Description: "Filter by status: open (default), completed, canceled, or all",
					},
				},
			},
		},
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  toolsListResult{Tools: tools},
	}
}

func (s *Server) handleToolsCall(req jsonRPCRequest) *jsonRPCResponse {
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
		}
	}

	log.Printf("[mcp] Tool call: %s with args %v", params.Name, params.Arguments)

	handler, ok := s.handlers[params.Name]
	if !ok {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: toolsCallResult{
				Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("Unknown tool: %s", params.Name)}},
				IsError: true,
			},
		}
	}

	result, err := handler(s.context, params.Arguments)
	if err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: toolsCallResult{
				Content: []contentBlock{{Type: "text", Text: fmt.Sprintf("Error: %v", err)}},
				IsError: true,
			},
		}
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: toolsCallResult{
			Content: []contentBlock{{Type: "text", Text: result}},
		},
	}
}

func (s *Server) sendResponse(resp *jsonRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[mcp] Failed to marshal response: %v", err)
		return
	}

	log.Printf("[mcp] Sending response for id=%v", resp.ID)
	fmt.Fprintln(s.writer, string(data))
}
