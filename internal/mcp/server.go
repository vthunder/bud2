package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/vthunder/bud2/internal/logging"
)

// SessionInfo holds the registration data for a per-subagent MCP session token.
type SessionInfo struct {
	AgentID       string
	DefaultDomain string
}

// Server implements an MCP server over stdio
type Server struct {
	// Tool handlers
	handlers map[string]ToolHandler

	// Tool definitions for tools/list
	definitions []ToolDef

	// Set of tool names that are GK tools (domain injection applies to these)
	gkTools map[string]bool

	// Session token registry: token → SessionInfo
	sessions   map[string]SessionInfo
	sessionsMu sync.RWMutex

	// Context passed to handlers
	context any

	// Extra HTTP handlers registered via RegisterHTTPHandler
	extraHandlers map[string]http.HandlerFunc

	// Resource provider callbacks (optional). Domain comes from session token.
	// resourceLister lists available resources for a domain.
	// resourceReader reads a single resource by URI for a domain.
	resourceLister func(domain string) ([]ResourceInfo, error)
	resourceReader func(domain, uri string) (string, error)

	reader *bufio.Reader
	writer io.Writer
}

// ToolDef defines a tool's schema for the MCP protocol
type ToolDef struct {
	Name        string
	Description string
	Properties  map[string]PropDef
	Required    []string
	// GKTool marks this tool as a GK tool so the HTTP handler injects the
	// session's default domain when the "domain" arg is absent.
	GKTool bool
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
		handlers:      make(map[string]ToolHandler),
		definitions:   []ToolDef{},
		gkTools:       make(map[string]bool),
		sessions:      make(map[string]SessionInfo),
		extraHandlers: make(map[string]http.HandlerFunc),
		reader:        bufio.NewReader(os.Stdin),
		writer:        os.Stdout,
	}
}

// RegisterSession maps a session token to an agent ID and default domain.
// Called by spawn_subagent before starting a subagent so the GK domain can
// be injected automatically for all gk_* tool calls from that subagent.
func (s *Server) RegisterSession(token, agentID, domain string) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	s.sessions[token] = SessionInfo{AgentID: agentID, DefaultDomain: domain}
}

// DomainForToken returns the default domain for a session token.
// Returns "/" if the token is unknown or empty.
func (s *Server) DomainForToken(token string) string {
	if token == "" {
		return "/"
	}
	s.sessionsMu.RLock()
	defer s.sessionsMu.RUnlock()
	if info, ok := s.sessions[token]; ok && info.DefaultDomain != "" {
		return info.DefaultDomain
	}
	return "/"
}

// RegisterHTTPHandler registers an extra HTTP handler at the given path.
// Must be called before RunHTTP.
func (s *Server) RegisterHTTPHandler(path string, handler http.HandlerFunc) {
	s.extraHandlers[path] = handler
}

// RegisterResourceProvider registers callbacks that handle resources/list and resources/read.
// lister receives the domain and returns available resources.
// reader receives the domain and URI and returns the resource text content.
// Must be called before RunHTTP / Run.
func (s *Server) RegisterResourceProvider(lister func(domain string) ([]ResourceInfo, error), reader func(domain, uri string) (string, error)) {
	s.resourceLister = lister
	s.resourceReader = reader
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
	if def.GKTool {
		s.gkTools[name] = true
	}
}

// ToolCount returns the number of registered tools
func (s *Server) ToolCount() int {
	return len(s.definitions)
}

// Call invokes a registered tool handler directly (for use by the reflex engine without HTTP)
func (s *Server) Call(toolName string, args map[string]any) (string, error) {
	handler, ok := s.handlers[toolName]
	if !ok {
		return "", fmt.Errorf("tool not found: %s", toolName)
	}
	return handler(s.context, args)
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
	Tools     *toolsCapability     `json:"tools,omitempty"`
	Resources *resourcesCapability `json:"resources,omitempty"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type resourcesCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ResourceInfo describes a single MCP resource returned by resources/list.
type ResourceInfo struct {
	URI         string
	Name        string
	Description string
	MimeType    string
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

// MCP resource types
type resourcesListResult struct {
	Resources []resourceDefinition `json:"resources"`
}

type resourceDefinition struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type resourcesReadParams struct {
	URI string `json:"uri"`
}

type resourcesReadResult struct {
	Contents []resourceContent `json:"contents"`
}

type resourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
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
		logging.Debug("mcp", "Client initialized")
		return nil
	case "tools/list":
		return s.handleToolsList(req)
	case "tools/call":
		return s.handleToolsCall(req)
	case "resources/list":
		return s.handleResourcesList(req, "/")
	case "resources/read":
		return s.handleResourcesRead(req, "/")
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

	caps := capabilities{
		Tools: &toolsCapability{},
	}
	if s.resourceLister != nil {
		caps.Resources = &resourcesCapability{}
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: initializeResult{
			ProtocolVersion: "2024-11-05",
			ServerInfo: serverInfo{
				Name:    "bud2",
				Version: "0.1.0",
			},
			Capabilities: caps,
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

func (s *Server) handleResourcesList(req jsonRPCRequest, domain string) *jsonRPCResponse {
	if s.resourceLister == nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resourcesListResult{Resources: []resourceDefinition{}},
		}
	}

	infos, err := s.resourceLister(domain)
	if err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonRPCError{
				Code:    -32603,
				Message: fmt.Sprintf("resources/list: %v", err),
			},
		}
	}

	defs := make([]resourceDefinition, 0, len(infos))
	for _, r := range infos {
		defs = append(defs, resourceDefinition{
			URI:         r.URI,
			Name:        r.Name,
			Description: r.Description,
			MimeType:    r.MimeType,
		})
	}
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resourcesListResult{Resources: defs},
	}
}

func (s *Server) handleResourcesRead(req jsonRPCRequest, domain string) *jsonRPCResponse {
	var params resourcesReadParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32602, Message: fmt.Sprintf("Invalid params: %v", err)},
			}
		}
	}
	if params.URI == "" {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32602, Message: "resources/read: uri is required"},
		}
	}
	if s.resourceReader == nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "resources/read: no resource provider configured"},
		}
	}

	text, err := s.resourceReader(domain, params.URI)
	if err != nil {
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32603, Message: fmt.Sprintf("resources/read %s: %v", params.URI, err)},
		}
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: resourcesReadResult{
			Contents: []resourceContent{{URI: params.URI, Text: text}},
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

// RunHTTP starts the MCP server as an HTTP server (blocking).
// Handles both /mcp (default domain "/") and /mcp/{token} (session domain).
func (s *Server) RunHTTP(addr string) error {
	mux := http.NewServeMux()
	// Handle /mcp and /mcp/{token} with the same handler (token extracted from path)
	mux.HandleFunc("/mcp", s.handleHTTP)
	mux.HandleFunc("/mcp/", s.handleHTTP)
	mux.HandleFunc("/", s.handleHTTP)
	for path, handler := range s.extraHandlers {
		mux.HandleFunc(path, handler)
	}

	log.Printf("[mcp] HTTP server starting on %s", addr)
	return http.ListenAndServe(addr, mux)
}

// extractToken parses the session token from the request path.
// /mcp/{token} → token; /mcp or / → ""
func extractToken(path string) string {
	if strings.HasPrefix(path, "/mcp/") {
		token := strings.TrimPrefix(path, "/mcp/")
		// Strip any trailing slashes and reject empty tokens
		token = strings.Trim(token, "/")
		return token
	}
	return ""
}

// handleHTTP handles HTTP requests for the MCP protocol.
// Supports session tokens in the path (/mcp/{token}) for per-subagent domain routing.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Only accept POST
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract session token and look up default domain
	token := extractToken(r.URL.Path)
	domain := s.DomainForToken(token)

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

	// For tool calls on GK tools: inject domain from session token if not provided
	if req.Method == "tools/call" && req.Params != nil {
		var params toolsCallParams
		if json.Unmarshal(req.Params, &params) == nil && s.gkTools[params.Name] {
			if params.Arguments == nil {
				params.Arguments = make(map[string]any)
			}
			if d, _ := params.Arguments["domain"].(string); d == "" {
				params.Arguments["domain"] = domain
				// Re-marshal params with injected domain
				if newParams, err := json.Marshal(map[string]any{
					"name":      params.Name,
					"arguments": params.Arguments,
				}); err == nil {
					req.Params = newParams
				}
			}
		}
	}

	// For resource methods, route directly with session domain (domain already extracted above)
	var resp *jsonRPCResponse
	switch req.Method {
	case "resources/list":
		resp = s.handleResourcesList(req, domain)
	case "resources/read":
		resp = s.handleResourcesRead(req, domain)
	default:
		resp = s.handleRequest(req)
	}

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
