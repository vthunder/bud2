package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vthunder/bud2/internal/extensions"
	"github.com/vthunder/bud2/internal/mcp"
)

// registerWorkflowTools registers the invoke_workflow and Skill MCP tools.
// These tools surface extension-based capabilities to the model for discovery and invocation.
// Both tools require deps.ExtensionRegistry to be set; if nil, a clear error is returned.
func registerWorkflowTools(server *mcp.Server, deps *Dependencies) {
	registerInvokeWorkflow(server, deps)
	registerSkillTool(server, deps)
}

// registerInvokeWorkflow registers the invoke_workflow MCP tool.
// It lists and invokes workflow-type capabilities from the extension registry.
func registerInvokeWorkflow(server *mcp.Server, deps *Dependencies) {
	server.RegisterTool("invoke_workflow", mcp.ToolDef{
		Description: "List or invoke a named workflow from the extension registry. Workflows are reusable automation sequences defined in extensions. Use action=list to discover available workflows; use action=invoke with a workflow name to run one.",
		Properties: map[string]mcp.PropDef{
			"action": {
				Type:        "string",
				Description: `"list" to enumerate available workflows, "invoke" to run a workflow by name`,
			},
			"name": {
				Type:        "string",
				Description: `Fully-qualified workflow name (e.g. "bud:gtd-dispatcher"). Required when action=invoke.`,
			},
			"params": {
				Type:        "object",
				Description: "Key-value parameters to pass to the workflow. Optional.",
			},
		},
		Required: []string{"action"},
	}, func(_ any, args map[string]any) (string, error) {
		if deps.ExtensionRegistry == nil {
			return "", fmt.Errorf("extension registry not configured")
		}

		action, _ := args["action"].(string)
		switch action {
		case "list":
			return listWorkflows(deps.ExtensionRegistry)
		case "invoke":
			name, _ := args["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name is required for action=invoke")
			}
			var params map[string]any
			if p, ok := args["params"].(map[string]any); ok {
				params = p
			}
			return invokeWorkflow(deps, name, params)
		default:
			return "", fmt.Errorf("unknown action %q: must be \"list\" or \"invoke\"", action)
		}
	})
}

// listWorkflows returns a JSON-marshalled list of available workflows from the registry.
func listWorkflows(reg *extensions.Registry) (string, error) {
	type workflowEntry struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		CallableFrom string `json:"callable_from"`
		Extension    string `json:"extension"`
	}

	var entries []workflowEntry
	for _, item := range reg.CapabilitiesOfType("workflow") {
		parts := strings.SplitN(item.FullName, ":", 2)
		extName := ""
		if len(parts) == 2 {
			extName = parts[0]
		}
		entries = append(entries, workflowEntry{
			Name:         item.FullName,
			Description:  item.Cap.Description,
			CallableFrom: item.Cap.CallableFrom,
			Extension:    extName,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if len(entries) == 0 {
		return `{"workflows": [], "note": "No callable workflows found in extension registry"}`, nil
	}

	result := map[string]any{"workflows": entries}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling workflow list: %w", err)
	}
	return string(data), nil
}

// invokeWorkflow loads a workflow capability YAML from the extension directory and
// executes it through the reflex engine. Returns the workflow result as a string.
func invokeWorkflow(deps *Dependencies, name string, params map[string]any) (string, error) {
	cap, ext, ok := deps.ExtensionRegistry.GetCapabilityByFullName(name)
	if !ok {
		return "", fmt.Errorf("workflow %q not found in extension registry", name)
	}
	if cap.Type != "workflow" {
		return "", fmt.Errorf("capability %q has type %q, not \"workflow\"", name, cap.Type)
	}

	// Derive the capability name from the full name (extName:capName).
	idx := strings.LastIndex(name, ":")
	capName := name[idx+1:]

	// Verify the workflow file exists in the extension's capabilities directory.
	yamlPath := filepath.Join(ext.Dir, "capabilities", capName+".yaml")
	if _, err := os.Stat(yamlPath); err != nil {
		return "", fmt.Errorf("workflow file not found at %s: %w", yamlPath, err)
	}

	if deps.ReflexEngine == nil {
		return "", fmt.Errorf("reflex engine not configured; cannot invoke workflow %q", name)
	}

	// Look up the workflow in the reflex engine by capability name.
	// Fall back to loading directly from the capability YAML if not pre-loaded.
	r := deps.ReflexEngine.Get(capName)
	if r == nil {
		loaded, loadErr := deps.ReflexEngine.LoadFile(yamlPath)
		if loadErr != nil {
			log.Printf("[invoke_workflow] workflow %q not in engine and failed to load from %s: %v", name, yamlPath, loadErr)
			paramsJSON, _ := json.MarshalIndent(params, "", "  ")
			return fmt.Sprintf(
				"Workflow %q could not be loaded: %v\nRequested params: %s",
				name, loadErr, string(paramsJSON),
			), nil
		}
		r = loaded
	}

	// Convert params to string-keyed extracted map for the engine.
	extracted := make(map[string]string, len(params))
	for k, v := range params {
		extracted[k] = fmt.Sprintf("%v", v)
	}

	result, err := deps.ReflexEngine.Execute(context.Background(), r, extracted, params)
	if err != nil {
		return "", fmt.Errorf("workflow %q execution failed: %w", name, err)
	}
	if result == nil {
		return fmt.Sprintf("Workflow %q completed (no output)", name), nil
	}
	outJSON, _ := json.MarshalIndent(result.Output, "", "  ")
	return fmt.Sprintf("Workflow %q result: %s", name, string(outJSON)), nil
}

// registerSkillTool registers the Skill MCP tool that reads from the extension registry.
// When invoked without a name, it lists available skills. When invoked with a name,
// it returns the skill's prompt body for injection into the current session.
//
// Note: This coexists with Claude Code's built-in Skill tool (which reads from --plugin-dir).
// The extension-based Skill tool is accessible as mcp__bud2__Skill and surfaces
// capabilities from the extension registry with type:skill and callable_from: both|model.
func registerSkillTool(server *mcp.Server, deps *Dependencies) {
	server.RegisterTool("Skill", mcp.ToolDef{
		Description: "List or invoke a skill from the extension registry. Skills provide behavioral guidance injected into the current session. Omit name to discover available skills; provide a name to load its prompt content.",
		Properties: map[string]mcp.PropDef{
			"name": {
				Type:        "string",
				Description: `Fully-qualified skill name (e.g. "bud-ops:handle-subagent-complete"). Omit to list all available skills.`,
			},
		},
	}, func(_ any, args map[string]any) (string, error) {
		if deps.ExtensionRegistry == nil {
			return "", fmt.Errorf("extension registry not configured")
		}

		name, _ := args["name"].(string)
		if name == "" {
			return listSkills(deps.ExtensionRegistry)
		}
		return invokeSkill(deps.ExtensionRegistry, name)
	})
}

// listSkills returns a JSON-encoded list of skills callable from the model.
func listSkills(reg *extensions.Registry) (string, error) {
	type skillEntry struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		CallableFrom string `json:"callable_from"`
	}

	var entries []skillEntry
	for _, item := range reg.CapabilitiesOfType("skill") {
		entries = append(entries, skillEntry{
			Name:         item.FullName,
			Description:  item.Cap.Description,
			CallableFrom: item.Cap.CallableFrom,
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })

	if len(entries) == 0 {
		return `{"skills": [], "note": "No callable skills found in extension registry"}`, nil
	}

	result := map[string]any{"skills": entries}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshalling skill list: %w", err)
	}
	return string(data), nil
}

// invokeSkill looks up a skill by full name and returns its body for prompt injection.
func invokeSkill(reg *extensions.Registry, name string) (string, error) {
	cap, _, ok := reg.GetCapabilityByFullName(name)
	if !ok {
		return "", fmt.Errorf("skill %q not found in extension registry", name)
	}
	if cap.Type != "skill" {
		return "", fmt.Errorf("capability %q has type %q, not \"skill\"", name, cap.Type)
	}
	if cap.Body == "" {
		return fmt.Sprintf("Skill %q has no prompt body", name), nil
	}
	return cap.Body, nil
}

