// migrate-plugin converts a legacy plugin directory (plugin.json + SKILL.md files)
// into an extension directory (extension.yaml + capabilities/*.md).
//
// Usage:
//
//	migrate-plugin --in <plugin-dir> --out <extension-dir>
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	inDir := flag.String("in", "", "input plugin directory")
	outDir := flag.String("out", "", "output extension directory")
	flag.Parse()

	if *inDir == "" || *outDir == "" {
		fmt.Fprintf(os.Stderr, "usage: migrate-plugin --in <plugin-dir> --out <extension-dir>\n")
		os.Exit(1)
	}

	if err := migrate(*inDir, *outDir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// pluginJSON is the legacy plugin.json schema.
type pluginJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Author      struct {
		Name string `json:"name"`
	} `json:"author"`
	Skills string `json:"skills"`
}

// capabilityMeta holds what goes into the extension.yaml capabilities map.
type capabilityMeta struct {
	CallableFrom string `yaml:"callable_from"`
}

// extensionYAML is the output extension.yaml schema (skeleton only for M1).
type extensionYAML struct {
	Name         string                    `yaml:"name"`
	Description  string                    `yaml:"description"`
	Author       string                    `yaml:"author,omitempty"`
	Behaviors    []any                     `yaml:"behaviors"`
	Capabilities map[string]capabilityMeta `yaml:"capabilities"`
}

// skillFrontmatter represents the YAML frontmatter parsed from a SKILL.md file.
type skillFrontmatter struct {
	Name          string `yaml:"name"`
	Description   string `yaml:"description"`
	UserInvocable bool   `yaml:"user-invocable"`
	// Capture any extra keys so we can preserve them (minus user-invocable).
	Extra map[string]any `yaml:",inline"`
}

// migrate performs the full conversion from plugin dir to extension dir.
func migrate(inDir, outDir string) error {
	// Locate plugin.json — check root and .claude-plugin/ subdirectory.
	pjsonPath, err := findPluginJSON(inDir)
	if err != nil {
		return fmt.Errorf("plugin.json not found: %w", err)
	}

	plugin, err := readPluginJSON(pjsonPath)
	if err != nil {
		return fmt.Errorf("reading plugin.json: %w", err)
	}

	// Ensure output directories exist.
	capsDir := filepath.Join(outDir, "capabilities")
	if err := os.MkdirAll(capsDir, 0o755); err != nil {
		return fmt.Errorf("creating output capabilities dir: %w", err)
	}

	// Scan skills directory.
	skillsDir := resolveSkillsDir(inDir, plugin.Skills)
	skills, err := discoverSkills(skillsDir)
	if err != nil {
		return fmt.Errorf("scanning skills: %w", err)
	}

	// Build capabilities map for extension.yaml and write each capability file.
	capsMap := make(map[string]capabilityMeta, len(skills))
	for _, s := range skills {
		meta, err := convertSkill(s, capsDir)
		if err != nil {
			return fmt.Errorf("converting skill %s: %w", s.name, err)
		}
		capsMap[s.name] = meta
	}

	// Write extension.yaml.
	ext := extensionYAML{
		Name:         plugin.Name,
		Description:  plugin.Description,
		Behaviors:    []any{},
		Capabilities: capsMap,
	}
	if plugin.Author.Name != "" {
		ext.Author = plugin.Author.Name
	}

	return writeExtensionYAML(filepath.Join(outDir, "extension.yaml"), ext)
}

// findPluginJSON looks for plugin.json in the root or .claude-plugin/ subdir.
func findPluginJSON(dir string) (string, error) {
	candidates := []string{
		filepath.Join(dir, "plugin.json"),
		filepath.Join(dir, ".claude-plugin", "plugin.json"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("checked %v", candidates)
}

// readPluginJSON reads and parses a plugin.json file using encoding/json.
func readPluginJSON(path string) (*pluginJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Use yaml.v3 in JSON-compatibility mode (it parses JSON natively).
	var p pluginJSON
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// resolveSkillsDir returns the absolute path for the skills directory.
// plugin.Skills is typically a relative path like "./skills/" or empty.
func resolveSkillsDir(pluginDir, skillsField string) string {
	if skillsField == "" {
		return filepath.Join(pluginDir, "skills")
	}
	return filepath.Join(pluginDir, filepath.Clean(skillsField))
}

// skillFile bundles a skill name with the path to its SKILL.md source.
type skillFile struct {
	name string
	path string
}

// discoverSkills walks the skills directory and finds all SKILL.md files.
// It handles two layouts:
//   - skills/<name>/SKILL.md
//   - skills/<name>.md
func discoverSkills(skillsDir string) ([]skillFile, error) {
	var skills []skillFile

	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, fmt.Errorf("reading skills dir %s: %w", skillsDir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			// Check for skills/<name>/SKILL.md
			candidate := filepath.Join(skillsDir, e.Name(), "SKILL.md")
			if _, err := os.Stat(candidate); err == nil {
				skills = append(skills, skillFile{name: e.Name(), path: candidate})
				continue
			}
		} else if strings.HasSuffix(e.Name(), ".md") {
			// Check for skills/<name>.md (flat layout)
			name := strings.TrimSuffix(e.Name(), ".md")
			skills = append(skills, skillFile{
				name: name,
				path: filepath.Join(skillsDir, e.Name()),
			})
		}
	}

	return skills, nil
}

// convertSkill reads a SKILL.md, transforms its frontmatter, and writes the
// capability .md to capsDir. Returns the capabilityMeta for extension.yaml.
func convertSkill(s skillFile, capsDir string) (capabilityMeta, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return capabilityMeta{}, err
	}

	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return capabilityMeta{}, fmt.Errorf("parsing frontmatter: %w", err)
	}

	// Determine callable_from from user-invocable flag.
	callableFrom := "direct"
	if fm.UserInvocable {
		callableFrom = "both"
	}

	// Build new frontmatter: start with preserved extra keys, then set our fields.
	// We want a deterministic YAML output so we control the key order explicitly.
	newFM := map[string]any{}
	// Copy extra keys (excludes name, description, user-invocable via inline parsing).
	for k, v := range fm.Extra {
		// Exclude user-invocable (already handled) and the skill-specific keys.
		if k == "user-invocable" {
			continue
		}
		newFM[k] = v
	}
	newFM["name"] = fm.Name
	newFM["description"] = fm.Description
	newFM["type"] = "skill"
	newFM["callable_from"] = callableFrom

	rendered, err := renderCapabilityMD(newFM, body)
	if err != nil {
		return capabilityMeta{}, err
	}

	outPath := filepath.Join(capsDir, s.name+".md")
	if err := os.WriteFile(outPath, rendered, 0o644); err != nil {
		return capabilityMeta{}, err
	}

	return capabilityMeta{CallableFrom: callableFrom}, nil
}

// parseFrontmatter splits a Markdown file into its YAML frontmatter and body.
// Returns the parsed frontmatter, the raw body (after the closing ---), and any error.
func parseFrontmatter(data []byte) (skillFrontmatter, []byte, error) {
	const delim = "---"

	lines := bytes.SplitN(data, []byte("\n"), -1)
	if len(lines) < 2 || strings.TrimRight(string(lines[0]), "\r") != delim {
		// No frontmatter — return empty struct and full content as body.
		return skillFrontmatter{}, data, nil
	}

	// Find closing ---
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(string(lines[i]), "\r") == delim {
			end = i
			break
		}
	}
	if end == -1 {
		return skillFrontmatter{}, data, fmt.Errorf("unclosed frontmatter block")
	}

	fmBytes := bytes.Join(lines[1:end], []byte("\n"))
	body := bytes.Join(lines[end+1:], []byte("\n"))

	var fm skillFrontmatter
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return skillFrontmatter{}, nil, err
	}

	return fm, body, nil
}

// renderCapabilityMD produces the output .md bytes: YAML frontmatter + body.
func renderCapabilityMD(fm map[string]any, body []byte) ([]byte, error) {
	// Use an ordered node to get deterministic key output.
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}

	// Preferred key order for readability.
	orderedKeys := []string{"name", "description", "type", "callable_from"}
	written := make(map[string]bool)

	for _, k := range orderedKeys {
		v, ok := fm[k]
		if !ok {
			continue
		}
		kNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		vNode := new(yaml.Node)
		if err := vNode.Encode(v); err != nil {
			return nil, err
		}
		// Encode wraps in a document node; unwrap.
		if vNode.Kind == yaml.DocumentNode && len(vNode.Content) > 0 {
			vNode = vNode.Content[0]
		}
		node.Content = append(node.Content, kNode, vNode)
		written[k] = true
	}

	// Append remaining keys in alphabetical order.
	for k, v := range fm {
		if written[k] {
			continue
		}
		kNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
		vNode := new(yaml.Node)
		if err := vNode.Encode(v); err != nil {
			return nil, err
		}
		if vNode.Kind == yaml.DocumentNode && len(vNode.Content) > 0 {
			vNode = vNode.Content[0]
		}
		node.Content = append(node.Content, kNode, vNode)
	}

	fmBytes, err := yaml.Marshal(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(fmBytes)
	buf.WriteString("---\n")
	buf.Write(body)

	return buf.Bytes(), nil
}

// writeExtensionYAML serialises and writes the extension manifest.
func writeExtensionYAML(path string, ext extensionYAML) error {
	data, err := yaml.Marshal(ext)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
