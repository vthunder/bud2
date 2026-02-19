package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

// ExternalServerConfig describes an external stdio MCP server to proxy
type ExternalServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// ProxyClient manages a stdio MCP server subprocess and proxies tool calls to it
type ProxyClient struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex // Serializes all send/receive operations
	nextID int64
}

// StartProxy starts an external MCP server subprocess and initializes the MCP session
func StartProxy(cfg ExternalServerConfig) (*ProxyClient, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)

	// Inherit parent environment, then override/add specified vars
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Pipe stderr to our stderr so we can see server logs
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
	}

	client := &ProxyClient{
		name:   cfg.Name,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}

	if err := client.initialize(); err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, fmt.Errorf("initialize %s: %w", cfg.Name, err)
	}

	log.Printf("[proxy:%s] Ready (pid=%d)", cfg.Name, cmd.Process.Pid)
	return client, nil
}

func (c *ProxyClient) newID() int64 {
	return atomic.AddInt64(&c.nextID, 1)
}

// sendRequest sends a JSON-RPC request and reads the corresponding response.
// The mutex must NOT be held by the caller — this method acquires it.
func (c *ProxyClient) sendRequest(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.newID()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	if _, err := fmt.Fprintf(c.stdin, "%s\n", data); err != nil {
		return nil, fmt.Errorf("write to %s: %w", c.name, err)
	}

	// Read lines until we find a response with matching ID (skip notifications)
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read from %s: %w", c.name, err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			// Not valid JSON-RPC — skip (could be startup logs)
			log.Printf("[proxy:%s] Skipping non-JSON line: %.80s", c.name, line)
			continue
		}

		// Notifications have no ID — skip them
		if resp.ID == nil {
			continue
		}

		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}

		result, err := json.Marshal(resp.Result)
		if err != nil {
			return nil, fmt.Errorf("marshal result: %w", err)
		}
		return result, nil
	}
}

// sendNotification sends a JSON-RPC notification (no response expected)
func (c *ProxyClient) sendNotification(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		notif["params"] = params
	}

	data, err := json.Marshal(notif)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}

	_, err = fmt.Fprintf(c.stdin, "%s\n", data)
	return err
}

func (c *ProxyClient) initialize() error {
	_, err := c.sendRequest("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]string{
			"name":    "bud2",
			"version": "0.1.0",
		},
		"capabilities": map[string]any{},
	})
	if err != nil {
		return fmt.Errorf("initialize handshake: %w", err)
	}

	return c.sendNotification("notifications/initialized", nil)
}

// DiscoverTools lists all tools available from the external server
func (c *ProxyClient) DiscoverTools() ([]ToolDef, error) {
	result, err := c.sendRequest("tools/list", nil)
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}

	var listResult toolsListResult
	if err := json.Unmarshal(result, &listResult); err != nil {
		return nil, fmt.Errorf("parse tools list: %w", err)
	}

	defs := make([]ToolDef, 0, len(listResult.Tools))
	for _, t := range listResult.Tools {
		props := make(map[string]PropDef)
		for name, p := range t.InputSchema.Properties {
			props[name] = PropDef{
				Type:        p.Type,
				Description: p.Description,
			}
		}
		defs = append(defs, ToolDef{
			Name:        t.Name,
			Description: t.Description,
			Properties:  props,
			Required:    t.InputSchema.Required,
		})
	}

	return defs, nil
}

// CallTool calls a named tool on the external server
func (c *ProxyClient) CallTool(name string, args map[string]any) (string, error) {
	result, err := c.sendRequest("tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", fmt.Errorf("tools/call %s: %w", name, err)
	}

	var callResult toolsCallResult
	if err := json.Unmarshal(result, &callResult); err != nil {
		return "", fmt.Errorf("parse call result: %w", err)
	}

	if callResult.IsError {
		if len(callResult.Content) > 0 {
			return "", fmt.Errorf("%s", callResult.Content[0].Text)
		}
		return "", fmt.Errorf("tool returned error")
	}

	if len(callResult.Content) == 0 {
		return "", nil
	}
	return callResult.Content[0].Text, nil
}

// Close stops the external server process
func (c *ProxyClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.stdin.Close()
	if c.cmd.Process != nil {
		c.cmd.Process.Kill()
	}
	c.cmd.Wait()
}

// MCPConfig represents the .mcp.json configuration file
type MCPConfig struct {
	MCPServers map[string]MCPServerEntry `json:"mcpServers"`
}

// MCPServerEntry is a single server entry in .mcp.json
type MCPServerEntry struct {
	Type    string            `json:"type,omitempty"`    // "http" for HTTP transport
	URL     string            `json:"url,omitempty"`     // for type=http
	Command string            `json:"command,omitempty"` // for stdio
	Args    []string          `json:"args,omitempty"`    // for stdio
	Env     map[string]string `json:"env,omitempty"`     // for stdio
}

// LoadMCPConfig reads and parses a .mcp.json file
func LoadMCPConfig(path string) (*MCPConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse .mcp.json: %w", err)
	}
	return &cfg, nil
}

// StartProxiesFromConfig reads .mcp.json and starts proxy clients for all stdio servers.
// For each server discovered, it registers its tools with the given MCP server.
// Returns a slice of started proxy clients (caller should defer Close on each).
func StartProxiesFromConfig(mcpConfigPath string, server *Server) ([]*ProxyClient, error) {
	cfg, err := LoadMCPConfig(mcpConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load mcp config: %w", err)
	}

	var proxies []*ProxyClient

	for name, entry := range cfg.MCPServers {
		// Skip HTTP servers — they're not stdio proxies
		if entry.Type == "http" || entry.Command == "" {
			continue
		}

		log.Printf("[proxy] Starting %s: %s %v", name, entry.Command, entry.Args)

		proxy, err := StartProxy(ExternalServerConfig{
			Name:    name,
			Command: entry.Command,
			Args:    entry.Args,
			Env:     entry.Env,
		})
		if err != nil {
			log.Printf("[proxy] Failed to start %s: %v", name, err)
			continue
		}

		// Discover tools from this server
		tools, err := proxy.DiscoverTools()
		if err != nil {
			log.Printf("[proxy:%s] Failed to discover tools: %v", name, err)
			proxy.Close()
			continue
		}

		log.Printf("[proxy:%s] Discovered %d tools", name, len(tools))

		// Register each tool with the main MCP server
		// The handler proxies calls through to the external process
		for _, def := range tools {
			toolName := def.Name
			proxyRef := proxy // capture for closure
			server.RegisterTool(toolName, def, func(ctx any, args map[string]any) (string, error) {
				return proxyRef.CallTool(toolName, args)
			})
		}

		proxies = append(proxies, proxy)
	}

	return proxies, nil
}
