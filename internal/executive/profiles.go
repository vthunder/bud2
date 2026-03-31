package executive

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Agent defines a named configuration for subagent spawning:
// a curated set of skills (behavioral guidance) and extra tools to allow.
type Agent struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Level       string   `yaml:"level"`
	Model       string   `yaml:"model"`   // optional: sonnet/opus/haiku (maps to AgentModel enum)
	Skills      []string `yaml:"skills"`  // skill names — folder (<name>/SKILL.md) or flat (<name>.md)
	Tools       []string `yaml:"tools"`   // additional tools beyond the base set
	Body        string   // parsed from markdown body after YAML frontmatter (not in YAML)
}

// AgentAliases holds the namespace alias registry loaded from agent-aliases.yaml.
// Agents: maps alias (e.g. "autopilot-vision:explorer") → resolved file path (e.g. "autopilot-vision/explorer").
// Skills: maps alias (e.g. "issue-operations") → resolved skill name (e.g. "things-operations").
type AgentAliases struct {
	Agents map[string]string `yaml:"agents"`
	Skills map[string]string `yaml:"skills"`
}

// LoadAgentAliases reads the alias table from state/system/agent-aliases.yaml.
// Returns empty aliases (not an error) if the file doesn't exist.
func LoadAgentAliases(statePath string) (*AgentAliases, error) {
	aliasPath := filepath.Join(statePath, "system", "agent-aliases.yaml")
	data, err := os.ReadFile(aliasPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &AgentAliases{Agents: make(map[string]string), Skills: make(map[string]string)}, nil
		}
		return nil, fmt.Errorf("read agent-aliases.yaml: %w", err)
	}
	var aliases AgentAliases
	if err := yaml.Unmarshal(data, &aliases); err != nil {
		return nil, fmt.Errorf("parse agent-aliases.yaml: %w", err)
	}
	if aliases.Agents == nil {
		aliases.Agents = make(map[string]string)
	}
	if aliases.Skills == nil {
		aliases.Skills = make(map[string]string)
	}
	return &aliases, nil
}

// ValidateAliasTargets checks that every alias target in agent-aliases.yaml resolves to
// a registered agent file. Logs an error for each broken alias. Returns false if any alias
// is broken, true if all resolve.
func ValidateAliasTargets(statePath string) bool {
	aliases, err := LoadAgentAliases(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[agent-aliases] ERROR: failed to load alias table: %v\n", err)
		return false
	}
	ok := true
	for alias, target := range aliases.Agents {
		if _, err := loadAgentByPath(statePath, target); err != nil {
			fmt.Fprintf(os.Stderr, "[agent-aliases] ERROR: alias %q → %q: target not found: %v\n", alias, target, err)
			ok = false
		}
	}
	return ok
}

// loadAgentByPath loads an agent from a resolved path (no alias lookup, no subdir inference).
// Tries <path>.yaml then <path>.md under state/system/agents/.
func loadAgentByPath(statePath, agentPath string) ([]byte, error) {
	base := filepath.Join(statePath, "system", "agents", agentPath)
	for _, ext := range []string{".yaml", ".md"} {
		data, err := os.ReadFile(base + ext)
		if err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("not found (tried .yaml and .md)")
}

// loadAgentFromPlugin loads an agent from the plugin directory structure.
// Looks in state/system/plugins/<namespace>/agents/<name>.yaml or .md.
func loadAgentFromPlugin(statePath, namespace, name string) ([]byte, error) {
	base := filepath.Join(statePath, "system", "plugins", namespace, "agents", name)
	for _, ext := range []string{".yaml", ".md"} {
		data, err := os.ReadFile(base + ext)
		if err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("not found in plugin %s (tried .yaml and .md)", namespace)
}

// parseAgentData parses YAML frontmatter + markdown body from raw agent file content.
func parseAgentData(data []byte, agentName string) (*Agent, error) {
	content := string(data)
	var a Agent

	if strings.HasPrefix(content, "---\n") {
		rest := content[4:]
		endIdx := strings.Index(rest, "\n---\n")
		if endIdx != -1 {
			frontmatter := rest[:endIdx]
			if err := yaml.Unmarshal([]byte(frontmatter), &a); err != nil {
				return nil, fmt.Errorf("parse agent %q frontmatter: %w", agentName, err)
			}
			a.Body = strings.TrimSpace(rest[endIdx+5:])
		} else {
			// Malformed frontmatter — parse whole thing as YAML
			if err := yaml.Unmarshal(data, &a); err != nil {
				return nil, fmt.Errorf("parse agent %q: %w", agentName, err)
			}
		}
	} else {
		// No frontmatter — parse entire content as YAML (backward compat)
		if err := yaml.Unmarshal(data, &a); err != nil {
			return nil, fmt.Errorf("parse agent %q: %w", agentName, err)
		}
	}

	if a.Name == "" {
		a.Name = agentName
	}
	return &a, nil
}

// LoadAgent reads an agent definition. Resolution order:
//  1. Plugin directory: state/system/plugins/<namespace>/agents/<name> for "namespace:name" style
//  2. Alias table: state/system/agent-aliases.yaml (backward compat for legacy names)
//  3. Legacy agents directory: state/system/agents/<path>
//
// Supports both .yaml and .md extensions (.yaml preferred).
func LoadAgent(statePath, agentName string) (*Agent, error) {
	// 1. Try plugin-based resolution for "namespace:name" style
	if strings.Contains(agentName, ":") {
		parts := strings.SplitN(agentName, ":", 2)
		namespace, name := parts[0], parts[1]
		if data, err := loadAgentFromPlugin(statePath, namespace, name); err == nil {
			return parseAgentData(data, agentName)
		}
	}

	// 2. Resolve via alias table (legacy compat)
	aliases, err := LoadAgentAliases(statePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[agent-aliases] warning: %v\n", err)
		aliases = &AgentAliases{Agents: make(map[string]string), Skills: make(map[string]string)}
	}
	resolved := agentName
	if target, ok := aliases.Agents[agentName]; ok {
		resolved = target
	}

	// 3. Load from legacy agents directory
	data, err := loadAgentByPath(statePath, resolved)
	if err != nil {
		return nil, fmt.Errorf("agent %q not found: %w", agentName, err)
	}
	return parseAgentData(data, agentName)
}

// LoadSkillContent reads a skill from state/system/skills/.
// Supports both folder format (<name>/SKILL.md) and flat format (<name>.md).
func LoadSkillContent(statePath, skillName string) (string, error) {
	base := filepath.Join(statePath, "system", "skills", skillName)
	// Prefer folder format: <name>/SKILL.md
	folderPath := filepath.Join(base, "SKILL.md")
	skillPath := base + ".md"
	if _, err := os.Stat(folderPath); err == nil {
		skillPath = folderPath
	}
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return "", fmt.Errorf("skill %q not found: %w", skillName, err)
	}
	content := string(data)

	// Strip YAML frontmatter (--- ... ---) if present — keep just the body
	if strings.HasPrefix(content, "---\n") {
		rest := content[4:]
		endIdx := strings.Index(rest, "\n---\n")
		if endIdx != -1 {
			content = strings.TrimSpace(rest[endIdx+5:])
		}
	}

	return content, nil
}

// JobDef defines a reusable job template.
type JobDef struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Profile     string     `yaml:"profile"` // default agent to use (kept as "profile" for backward compat in YAML)
	Params      []JobParam `yaml:"params"`
}

// JobParam describes a single template parameter.
type JobParam struct {
	Name        string `yaml:"name"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
	Description string `yaml:"description"`
}

// JobListing is a summary of a job returned by ListJobs.
type JobListing struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Profile     string   `json:"profile,omitempty"`
	Params      []string `json:"params,omitempty"`
	Source      string   `json:"source"`
	Ref         string   `json:"ref"`
}

// LoadJob loads a job definition by reference.
// ref format: "job-name" (global) or "project/<projectname>/<jobname>" (project-scoped).
// Returns the parsed JobDef, the body template string, and any error.
func LoadJob(statePath, ref string) (*JobDef, string, error) {
	var jobPath string
	if strings.HasPrefix(ref, "project/") {
		// project/<projectname>/<jobname>
		parts := strings.SplitN(strings.TrimPrefix(ref, "project/"), "/", 2)
		if len(parts) != 2 {
			return nil, "", fmt.Errorf("invalid project job ref %q: expected project/<project>/<job>", ref)
		}
		projectName, jobName := parts[0], parts[1]
		jobPath = filepath.Join(statePath, "projects", projectName, "jobs", jobName+".md")
	} else {
		jobPath = filepath.Join(statePath, "system", "jobs", ref+".md")
	}

	data, err := os.ReadFile(jobPath)
	if err != nil {
		return nil, "", fmt.Errorf("job %q not found: %w", ref, err)
	}
	content := string(data)

	var def JobDef
	var body string

	if strings.HasPrefix(content, "---\n") {
		rest := content[4:]
		endIdx := strings.Index(rest, "\n---\n")
		if endIdx != -1 {
			frontmatter := rest[:endIdx]
			if err := yaml.Unmarshal([]byte(frontmatter), &def); err != nil {
				return nil, "", fmt.Errorf("parse job %q frontmatter: %w", ref, err)
			}
			body = strings.TrimSpace(rest[endIdx+5:])
		} else {
			body = strings.TrimSpace(content)
		}
	} else {
		body = strings.TrimSpace(content)
	}

	if def.Name == "" {
		// Extract bare name from ref
		parts := strings.Split(ref, "/")
		def.Name = parts[len(parts)-1]
	}

	return &def, body, nil
}

var jobPlaceholderRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

// RenderJobTemplate substitutes {{param}} placeholders with provided values.
// Returns error if a required param is missing.
func RenderJobTemplate(body string, jobDef *JobDef, params map[string]string) (string, error) {
	// Build a lookup of param definitions by name.
	paramDefs := make(map[string]*JobParam, len(jobDef.Params))
	for i := range jobDef.Params {
		paramDefs[jobDef.Params[i].Name] = &jobDef.Params[i]
	}

	// Check required params first.
	for _, p := range jobDef.Params {
		if p.Required {
			if v, ok := params[p.Name]; !ok || v == "" {
				return "", fmt.Errorf("job %q: required param %q not provided", jobDef.Name, p.Name)
			}
		}
	}

	result := jobPlaceholderRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := jobPlaceholderRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		name := sub[1]
		if v, ok := params[name]; ok && v != "" {
			return v
		}
		if def, ok := paramDefs[name]; ok && def.Default != "" {
			return def.Default
		}
		return ""
	})

	return result, nil
}

// ListJobs returns available job templates. If project is empty, returns only global jobs.
// If project is non-empty, returns global + project jobs.
func ListJobs(statePath, project string) ([]JobListing, error) {
	var listings []JobListing

	// Global jobs
	globalDir := filepath.Join(statePath, "system", "jobs")
	if entries, err := os.ReadDir(globalDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			jobName := strings.TrimSuffix(e.Name(), ".md")
			def, _, loadErr := LoadJob(statePath, jobName)
			if loadErr != nil {
				continue
			}
			l := JobListing{
				Name:        def.Name,
				Description: def.Description,
				Profile:     def.Profile,
				Source:      "global",
				Ref:         jobName,
			}
			for _, p := range def.Params {
				l.Params = append(l.Params, p.Name)
			}
			listings = append(listings, l)
		}
	}

	// Project jobs
	if project != "" {
		projectDir := filepath.Join(statePath, "projects", project, "jobs")
		if entries, err := os.ReadDir(projectDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				jobName := strings.TrimSuffix(e.Name(), ".md")
				ref := "project/" + project + "/" + jobName
				def, _, loadErr := LoadJob(statePath, ref)
				if loadErr != nil {
					continue
				}
				l := JobListing{
					Name:        def.Name,
					Description: def.Description,
					Profile:     def.Profile,
					Source:      "project/" + project,
					Ref:         ref,
				}
				for _, p := range def.Params {
					l.Params = append(l.Params, p.Name)
				}
				listings = append(listings, l)
			}
		}
	}

	return listings, nil
}

// ResolveSubagentConfig loads an agent (if non-empty) and returns:
//   - mergedTools: base tools merged with agent tools, comma-separated
//   - systemPromptAppend: agent body + concatenated skill content to append to system prompt
//
// If agentName is empty, returns baseTools and empty prompt unchanged.
func ResolveSubagentConfig(statePath, agentName, baseTools string) (mergedTools, systemPromptAppend string, err error) {
	if agentName == "" {
		return baseTools, "", nil
	}

	agent, err := LoadAgent(statePath, agentName)
	if err != nil {
		return baseTools, "", err
	}

	// Merge tools: start with base, add agent tools not already present
	toolSet := make(map[string]bool)
	var toolList []string
	for _, t := range strings.Split(baseTools, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !toolSet[t] {
			toolSet[t] = true
			toolList = append(toolList, t)
		}
	}
	for _, t := range agent.Tools {
		t = strings.TrimSpace(t)
		// Normalize autopilot-style Agent(...) declarations to plain "Agent".
		// e.g. "Agent(autopilot-vision:explorer, autopilot-vision:researcher)" → "Agent"
		if strings.HasPrefix(t, "Agent(") {
			t = "Agent"
		}
		if t != "" && !toolSet[t] {
			toolSet[t] = true
			toolList = append(toolList, t)
		}
	}
	mergedTools = strings.Join(toolList, ",")

	// Load agent aliases for skill resolution
	aliases, aliasErr := LoadAgentAliases(statePath)
	if aliasErr != nil {
		aliases = &AgentAliases{Agents: make(map[string]string), Skills: make(map[string]string)}
	}

	// Load and concatenate skill content (with alias resolution)
	var skillParts []string
	for _, skillName := range agent.Skills {
		// Resolve skill alias if present
		if target, ok := aliases.Skills[skillName]; ok {
			skillName = target
		}
		content, skillErr := LoadSkillContent(statePath, skillName)
		if skillErr != nil {
			// Non-fatal: log and skip missing skills
			_ = skillErr
			continue
		}
		if content != "" {
			skillParts = append(skillParts, content)
		}
	}
	skillContent := strings.Join(skillParts, "\n\n---\n\n")

	// Prepend agent body if present
	if agent.Body != "" {
		if skillContent != "" {
			systemPromptAppend = "## Agent Behavioral Guide\n\n" + agent.Body + "\n\n---\n\n" + skillContent
		} else {
			systemPromptAppend = "## Agent Behavioral Guide\n\n" + agent.Body
		}
	} else {
		systemPromptAppend = skillContent
	}

	return mergedTools, systemPromptAppend, nil
}
