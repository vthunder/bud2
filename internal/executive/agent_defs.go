package executive

import (
	"fmt"
	"strings"

	claudecode "github.com/severity1/claude-agent-sdk-go"
	"github.com/vthunder/bud2/internal/extensions"
)

// LoadAgentDefsFromRegistry builds an AgentDefinition map from the extension registry.
// It returns definitions for all agent-type capabilities with callable_from "both" or "model".
// Keys follow the "extname:capname" format (e.g. "bud:coder").
//
// knownTools is the list of MCP tool names (with mcp__<server>__ prefix) used to
// expand wildcard patterns in extension requires.tools (e.g. "mcp__bud2__gk_*").
func LoadAgentDefsFromRegistry(reg *extensions.Registry, knownTools []string) map[string]claudecode.AgentDefinition {
	defs := make(map[string]claudecode.AgentDefinition)
	for _, entry := range reg.CapabilitiesOfType("agent") {
		cap := entry.Cap
		ext := entry.Ext

		prompt := ""
		if cap.Body != "" {
			prompt = "## Agent Behavioral Guide\n\n" + cap.Body
		}

		// Build tools list: per-capability tools first, then extension-level requires.tools.
		toolSeen := make(map[string]bool)
		var tools []string
		for _, t := range cap.Tools {
			t = strings.TrimSpace(t)
			if strings.HasPrefix(t, "Agent(") {
				t = "Agent"
			}
			if t != "" && !toolSeen[t] {
				toolSeen[t] = true
				tools = append(tools, t)
			}
		}
		for _, t := range expandToolGrants(ext.Manifest.Requires.Tools, knownTools) {
			t = strings.TrimSpace(t)
			if strings.HasPrefix(t, "Agent(") {
				t = "Agent"
			}
			if t != "" && !toolSeen[t] {
				toolSeen[t] = true
				tools = append(tools, t)
			}
		}

		defs[entry.FullName] = claudecode.AgentDefinition{
			Description: cap.Description,
			Prompt:      prompt,
			Tools:       tools,
			Model:       parseAgentModel(cap.Model),
		}
	}
	return defs
}

// ResolveSubagentConfigFromRegistry is the extension-registry-based resolver for
// on-demand subagent spawning. It looks up an agent capability by its "ext:cap" name
// and returns:
//   - mergedTools: baseTools merged with the capability's declared tools, comma-separated
//   - systemPromptAppend: the capability body prepended with the agent guide header
//
// Returns an error if the capability is not found or is not of type "agent".
func ResolveSubagentConfigFromRegistry(reg *extensions.Registry, agentName, baseTools string) (mergedTools, systemPromptAppend string, err error) {
	if agentName == "" {
		return baseTools, "", nil
	}

	cap, ext, ok := reg.GetCapabilityByFullName(agentName)
	if !ok {
		return baseTools, "", fmt.Errorf("agent %q not found in extension registry", agentName)
	}
	if cap.Type != "agent" {
		return baseTools, "", fmt.Errorf("capability %q is not an agent (type: %s)", agentName, cap.Type)
	}

	// Merge tools: start with base, add capability-declared and extension-level tools.
	toolSet := make(map[string]bool)
	var toolList []string
	for _, t := range strings.Split(baseTools, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !toolSet[t] {
			toolSet[t] = true
			toolList = append(toolList, t)
		}
	}
	for _, t := range cap.Tools {
		t = strings.TrimSpace(t)
		if strings.HasPrefix(t, "Agent(") {
			t = "Agent"
		}
		if t != "" && !toolSet[t] {
			toolSet[t] = true
			toolList = append(toolList, t)
		}
	}
	for _, t := range ext.Manifest.Requires.Tools {
		t = strings.TrimSpace(t)
		if t != "" && !toolSet[t] {
			toolSet[t] = true
			toolList = append(toolList, t)
		}
	}
	mergedTools = strings.Join(toolList, ",")

	if cap.Body != "" {
		systemPromptAppend = "## Agent Behavioral Guide\n\n" + cap.Body
	}

	return mergedTools, systemPromptAppend, nil
}

// parseAgentModel converts a model string from agent YAML to an AgentModel enum value.
func parseAgentModel(model string) claudecode.AgentModel {
	switch strings.ToLower(strings.TrimSpace(model)) {
	case "sonnet":
		return claudecode.AgentModelSonnet
	case "opus":
		return claudecode.AgentModelOpus
	case "haiku":
		return claudecode.AgentModelHaiku
	default:
		return claudecode.AgentModelInherit
	}
}
