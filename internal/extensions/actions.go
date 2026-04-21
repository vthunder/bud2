package extensions

// Package extensions: WS6 Action Proxy Layer.
//
// ActionProxy exposes extension-declared shell actions to the trigger/step pipeline.
// Shell actions have a `run:` field pointing to a script. When callable_from is
// "both" or "model", the action is also registered as an MCP tool named "<ext>:<cap>".
// Actions with callable_from: "direct" are reachable only from workflow type:direct steps.
//
// MCP subprocess actions (`server:` field) are deferred to a later milestone per R3.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/vthunder/bud2/internal/mcp"
)

// ActionProxy exposes extension-declared shell actions to the MCP server and to
// workflow type:direct steps. It implements mcp.ToolCaller so it can be set as
// the reflex engine's action caller.
type ActionProxy struct {
	mu      sync.RWMutex
	actions map[string]*actionEntry // "<ext>:<cap>" → entry
}

// actionEntry holds a shell action's extension context and capability definition.
type actionEntry struct {
	ext *Extension
	cap *Capability
}

// NewActionProxy builds an ActionProxy from the loaded registry.
// All extension capabilities with type=="action" and a non-empty run: field
// are indexed. Call RegisterMCPTools to wire callable_from:both|model actions
// onto an MCP server.
func NewActionProxy(registry *Registry) *ActionProxy {
	p := &ActionProxy{
		actions: make(map[string]*actionEntry),
	}
	for _, ext := range registry.All() {
		for capName, cap := range ext.Capabilities {
			if cap.Type != "action" || cap.Run == "" {
				continue
			}
			toolName := ext.Manifest.Name + ":" + capName
			p.actions[toolName] = &actionEntry{ext: ext, cap: cap}
		}
	}
	return p
}

// RegisterMCPTools registers shell actions with callable_from=both|model as MCP
// tools on the given server. Each tool is named "<ext>:<cap>" and its input schema
// is derived from the capability's params: block.
//
// Actions with callable_from=direct are silently skipped — they are reachable only
// from workflow type:direct steps via the Call method.
func (p *ActionProxy) RegisterMCPTools(server *mcp.Server) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for toolName, entry := range p.actions {
		switch entry.cap.CallableFrom {
		case "both", "model":
			def := buildActionToolDef(entry.cap)
			// Capture loop variables for the closure.
			tName := toolName
			ent := entry
			server.RegisterTool(tName, def, func(_ any, args map[string]any) (string, error) {
				return p.runShell(ent, args)
			})
			log.Printf("[actions] registered MCP tool %q → %s", tName, ent.cap.Run)
		default:
			// callable_from: direct — skip MCP registration.
		}
	}
}

// Call invokes a shell action by its fully-qualified "<ext>:<cap>" name.
// This is the direct-invocation path used by workflow type:direct steps.
// Both callable_from:both|model and callable_from:direct actions are reachable here.
func (p *ActionProxy) Call(toolName string, args map[string]any) (string, error) {
	p.mu.RLock()
	entry, ok := p.actions[toolName]
	p.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("action proxy: unknown action %q", toolName)
	}
	return p.runShell(entry, args)
}

// HasAction reports whether the proxy knows about a tool with the given name.
func (p *ActionProxy) HasAction(toolName string) bool {
	p.mu.RLock()
	_, ok := p.actions[toolName]
	p.mu.RUnlock()
	return ok
}

// runShell executes the action's shell script.
// params are serialized as JSON and written to the script's stdin.
// The script's stdout (trimmed) is returned as the result.
func (p *ActionProxy) runShell(entry *actionEntry, params map[string]any) (string, error) {
	if params == nil {
		params = map[string]any{}
	}

	// Validate required params before invoking the script.
	for paramName, def := range entry.cap.Params {
		if def.Required {
			if _, present := params[paramName]; !present {
				return "", fmt.Errorf("action %s: required param %q is missing", entry.cap.Name, paramName)
			}
		}
	}

	// Resolve the script path relative to the extension directory.
	scriptPath := filepath.Join(entry.ext.Dir, entry.cap.Run)
	if _, err := os.Stat(scriptPath); err != nil {
		return "", fmt.Errorf("action %s: script not found at %s: %w", entry.cap.Name, scriptPath, err)
	}

	// Serialize params as JSON for the script's stdin.
	input, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("action %s: marshaling params: %w", entry.cap.Name, err)
	}

	cmd := exec.Command(scriptPath) // #nosec G204 — path is from trusted extension manifest
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return "", fmt.Errorf("action %s exited %d: %s", entry.cap.Name, exitErr.ExitCode(), stderr)
			}
			return "", fmt.Errorf("action %s exited with code %d", entry.cap.Name, exitErr.ExitCode())
		}
		return "", fmt.Errorf("action %s: %w", entry.cap.Name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// buildActionToolDef converts a Capability's param schema to an mcp.ToolDef.
func buildActionToolDef(cap *Capability) mcp.ToolDef {
	props := make(map[string]mcp.PropDef, len(cap.Params))
	var required []string

	for name, def := range cap.Params {
		paramType := def.Type
		if paramType == "" {
			paramType = "string"
		}
		props[name] = mcp.PropDef{
			Type:        paramType,
			Description: def.Description,
		}
		if def.Required {
			required = append(required, name)
		}
	}

	return mcp.ToolDef{
		Description: cap.Description,
		Properties:  props,
		Required:    required,
	}
}
