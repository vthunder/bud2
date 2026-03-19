package executive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Profile defines a named configuration for subagent spawning:
// a curated set of skills (behavioral guidance) and extra tools to allow.
type Profile struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Skills      []string `yaml:"skills"`  // skill names — folder (<name>/SKILL.md) or flat (<name>.md)
	Tools       []string `yaml:"tools"`   // additional tools beyond the base set
}

// LoadProfile reads a profile definition from state/system/profiles/<name>.yaml.
func LoadProfile(statePath, profileName string) (*Profile, error) {
	profilePath := filepath.Join(statePath, "system", "profiles", profileName+".yaml")
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("profile %q not found: %w", profileName, err)
	}

	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse profile %q: %w", profileName, err)
	}
	if p.Name == "" {
		p.Name = profileName
	}
	return &p, nil
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

// ResolveSubagentConfig loads a profile (if non-empty) and returns:
//   - mergedTools: base tools merged with profile tools, comma-separated
//   - systemPromptAppend: concatenated skill content to append to system prompt
//
// If profileName is empty, returns baseTools and empty prompt unchanged.
func ResolveSubagentConfig(statePath, profileName, baseTools string) (mergedTools, systemPromptAppend string, err error) {
	if profileName == "" {
		return baseTools, "", nil
	}

	profile, err := LoadProfile(statePath, profileName)
	if err != nil {
		return baseTools, "", err
	}

	// Merge tools: start with base, add profile tools not already present
	toolSet := make(map[string]bool)
	var toolList []string
	for _, t := range strings.Split(baseTools, ",") {
		t = strings.TrimSpace(t)
		if t != "" && !toolSet[t] {
			toolSet[t] = true
			toolList = append(toolList, t)
		}
	}
	for _, t := range profile.Tools {
		t = strings.TrimSpace(t)
		if t != "" && !toolSet[t] {
			toolSet[t] = true
			toolList = append(toolList, t)
		}
	}
	mergedTools = strings.Join(toolList, ",")

	// Load and concatenate skill content
	var skillParts []string
	for _, skillName := range profile.Skills {
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
	systemPromptAppend = strings.Join(skillParts, "\n\n---\n\n")

	return mergedTools, systemPromptAppend, nil
}
