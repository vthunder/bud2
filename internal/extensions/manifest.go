// Package extensions implements the extension loader, registry, settings store,
// and state store for the bud2 extensibility framework (WS2 + WS3).
package extensions

import "sync"

// Extension is a fully-loaded extension ready for use by the runtime.
type Extension struct {
	Manifest     Manifest
	Dir          string
	Capabilities map[string]*Capability
	Settings     map[string]any // current settings (loaded + schema defaults applied)
	State        map[string]any // current state
	mu           sync.Mutex    // serializes all file I/O for this extension
}

// Manifest is the parsed content of extension.yaml.
type Manifest struct {
	Name        string                   `yaml:"name"`
	Version     string                   `yaml:"version,omitempty"`
	Description string                   `yaml:"description"`
	Author      string                   `yaml:"author,omitempty"`
	Capabilities map[string]CapabilityMeta `yaml:"capabilities,omitempty"`
	Behaviors   []Behavior               `yaml:"behaviors,omitempty"`
	Lifecycle   map[string]string        `yaml:"lifecycle,omitempty"`
	Requires    Requirements             `yaml:"requires,omitempty"`
	MCPServers  map[string]MCPServerDef  `yaml:"mcp_servers,omitempty"`
	// Settings is a flat map from setting key to its JSON Schema subset node.
	// Treat this as the "properties" of an implicit root object schema.
	Settings         map[string]SchemaNode `yaml:"settings,omitempty"`
	// SettingsRequired lists which top-level settings keys are required.
	// Missing required settings emit a warning on load, but never hard-fail.
	SettingsRequired []string              `yaml:"settings_required,omitempty"`
}

// CapabilityMeta is the per-capability entry in extension.yaml capabilities map.
type CapabilityMeta struct {
	CallableFrom string `yaml:"callable_from"`
}

// Capability is a loaded capability from a capabilities/*.md or capabilities/*.yaml file.
type Capability struct {
	Name         string
	Description  string
	Type         string // "skill", "agent", "workflow", "action"
	CallableFrom string // "model", "direct", "both"
	Body         string // raw markdown body (content after frontmatter); empty for YAML capabilities
	Model        string // optional model override for agent capabilities ("sonnet", "opus", "haiku")
	Tools        []string // per-capability tool allow-list (agent capabilities only)

	// Action-specific fields (populated for type: action from .yaml capability files)
	Run    string               // path to shell script relative to extension dir
	Params map[string]ParamDef  // parameter schema for input validation and MCP tool generation
}

// ParamDef describes a single parameter for an action capability.
// Unlike SchemaNode (which uses Required as a []string for object schemas), ParamDef
// uses Required as a bool to indicate whether the parameter must be provided.
type ParamDef struct {
	Type        string `yaml:"type,omitempty"`        // "string", "integer", "boolean", "number"
	Description string `yaml:"description,omitempty"` // human-readable description
	Required    bool   `yaml:"required,omitempty"`    // whether the parameter must be supplied
	Default     any    `yaml:"default,omitempty"`     // value applied when param is absent
	Enum        []any  `yaml:"enum,omitempty"`        // allowed values
}

// Behavior is a trigger-to-workflow binding declared in extension.yaml.
// The trigger field is kept as a generic map so it can hold any trigger type
// (schedule, slash_command, pattern_match, event, condition, manual) without
// requiring schema changes as new trigger types are added in later workstreams.
type Behavior struct {
	Name     string         `yaml:"name"`
	Trigger  map[string]any `yaml:"trigger"`
	Workflow string         `yaml:"workflow,omitempty"`
}

// Requirements declares what an extension depends on from the runtime.
type Requirements struct {
	// Extensions lists required extension names. LoadAll topologically sorts
	// based on this field and rejects cycles.
	Extensions []string `yaml:"extensions,omitempty"`
	Tools      []string `yaml:"tools,omitempty"`
	MCPServers []string `yaml:"mcp_servers,omitempty"`
}

// MCPServerDef describes an MCP subprocess that the extension can start on demand.
type MCPServerDef struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args,omitempty"`
	Env     map[string]string `yaml:"env,omitempty"`
}

// SchemaNode is a subset of JSON Schema used to describe and validate settings values.
//
// Supported keywords: type, description, default, required (array, for object types),
// enum, minimum, maximum, minLength, maxLength, pattern, items, properties.
//
// Unknown YAML keys are captured in Unknown and produce a warning on load.
// They are never treated as errors — unsupported keywords are warn-and-ignored.
type SchemaNode struct {
	Type        string                `yaml:"type,omitempty"`
	Description string                `yaml:"description,omitempty"`
	Default     any                   `yaml:"default,omitempty"`
	// Required lists required property names within an object-type SchemaNode.
	Required    []string              `yaml:"required,omitempty"`
	Enum        []any                 `yaml:"enum,omitempty"`
	Minimum     *float64              `yaml:"minimum,omitempty"`
	Maximum     *float64              `yaml:"maximum,omitempty"`
	MinLength   *int                  `yaml:"minLength,omitempty"`
	MaxLength   *int                  `yaml:"maxLength,omitempty"`
	Pattern     string                `yaml:"pattern,omitempty"`
	Items       *SchemaNode           `yaml:"items,omitempty"`
	Properties  map[string]SchemaNode `yaml:"properties,omitempty"`
	// Unknown captures any YAML keys not listed above for warning purposes.
	Unknown     map[string]any        `yaml:",inline"`
}
