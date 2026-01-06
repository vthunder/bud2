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
						Description: "The Discord channel ID to send to (usually from the original message context)",
					},
				},
				Required: []string{"message", "channel_id"},
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
