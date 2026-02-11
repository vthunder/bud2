package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/vthunder/bud2/internal/logging"
)

// Server implements an MCP server over stdio
type Server struct {
	// Tool handlers
	handlers map[string]ToolHandler

	// Tool definitions for tools/list
	definitions []ToolDef

	// Context passed to handlers
	context any

	reader *bufio.Reader
	writer io.Writer
}

// ToolDef defines a tool's schema for the MCP protocol
type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]PropDef
	Required    []string
}

// PropDef defines a property in a tool's input schema
type PropDef struct {
	Type        string
	Description string
}

// ToolHandler handles a tool call
type ToolHandler func(ctx any, args map[string]any) (string, error)

// NewServer creates a new MCP server
func NewServer() *Server {
	return &Server{
		handlers:    make(map[string]ToolHandler),
		definitions: []ToolDef{},
		reader:      bufio.NewReader(os.Stdin),
		writer:      os.Stdout,
	}
}

// SetContext sets the context passed to tool handlers
func (s *Server) SetContext(ctx any) {
	s.context = ctx
}

// RegisterTool registers a tool handler with its definition
func (s *Server) RegisterTool(name string, def ToolDef, handler ToolHandler) {
	s.handlers[name] = handler
	def.Name = name // Ensure name matches
	s.definitions = append(s.definitions, def)
}

// ToolCount returns the number of registered tools
func (s *Server) ToolCount() int {
	return len(s.definitions)
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
	case "initialized", "notifications/initialized":
		// Notification, no response needed
		// Both forms are valid - "initialized" per original spec,
		// "notifications/initialized" per newer MCP implementations
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

	logging.Debug("mcp", "Initialize from %s %s", params.ClientInfo.Name, params.ClientInfo.Version)

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
	// Convert registered ToolDefs to MCP toolDefinition format
	tools := make([]toolDefinition, 0, len(s.definitions))
	for _, def := range s.definitions {
		props := make(map[string]property)
		for name, p := range def.Properties {
			props[name] = property{
				Type:        p.Type,
				Description: p.Description,
			}
		}
		tools = append(tools, toolDefinition{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: inputSchema{
				Type:       "object",
				Properties: props,
				Required:   def.Required,
			},
		})
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

	logging.Debug("mcp", "Tool call: %s", params.Name)

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
	fmt.Fprintln(s.writer, string(data))
}

// RunHTTP starts the MCP server as an HTTP server (blocking)
// The server handles JSON-RPC requests via POST to the root path
func (s *Server) RunHTTP(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHTTP)
	mux.HandleFunc("/mcp", s.handleHTTP)

	log.Printf("[mcp] HTTP server starting on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// handleHTTP handles HTTP requests for the MCP protocol
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse JSON-RPC request
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		log.Printf("[mcp-http] Failed to parse request: %v", err)
		http.Error(w, "Invalid JSON-RPC request", http.StatusBadRequest)
		return
	}

	// Handle the request
	resp := s.handleRequest(req)

	// Set headers
	w.Header().Set("Content-Type", "application/json")

	// For notifications (no response needed), return 204
	if resp == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Marshal and send response
	data, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[mcp-http] Failed to marshal response: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Write(data)
}
