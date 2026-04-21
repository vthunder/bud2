package extensions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadExtension reads one extension from dir, validates the manifest, loads all
// capabilities, applies settings schema defaults, and loads any existing state.
// Soft failures (unknown schema keywords, missing-but-defaultable settings, etc.)
// emit log warnings rather than returning errors.
func LoadExtension(dir string) (*Extension, error) {
	manifestPath := filepath.Join(dir, "extension.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("extensions: reading manifest %s: %w", manifestPath, err)
	}

	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("extensions: parsing manifest %s: %w", manifestPath, err)
	}

	if m.Name == "" {
		return nil, fmt.Errorf("extensions: manifest %s: missing required field 'name'", manifestPath)
	}

	ext := &Extension{
		Manifest: m,
		Dir:      dir,
	}

	// Load capabilities from capabilities/ subdirectory.
	caps, err := loadCapabilities(dir)
	if err != nil {
		return nil, fmt.Errorf("extensions: loading capabilities for %s: %w", m.Name, err)
	}
	ext.Capabilities = caps

	// Warn about capabilities declared in the manifest but missing as .md files.
	for name := range m.Capabilities {
		if _, ok := caps[name]; !ok {
			log.Printf("extensions: %s: capability %q declared in manifest but no .md file found", m.Name, name)
		}
	}

	// Load settings.json (or create with defaults if absent).
	if err := initSettings(ext); err != nil {
		return nil, fmt.Errorf("extensions: loading settings for %s: %w", m.Name, err)
	}

	// Load state.json (optional).
	if err := initState(ext); err != nil {
		return nil, fmt.Errorf("extensions: loading state for %s: %w", m.Name, err)
	}

	return ext, nil
}

// loadCapabilities walks the capabilities/ subdirectory of extDir and returns
// a map from capability name (filename without extension) to parsed Capability.
// Both .md (skill/agent/workflow) and .yaml (action) files are supported.
// If a name exists as both .md and .yaml, the .md file takes precedence.
func loadCapabilities(extDir string) (map[string]*Capability, error) {
	capsDir := filepath.Join(extDir, "capabilities")
	entries, err := os.ReadDir(capsDir)
	if os.IsNotExist(err) {
		return map[string]*Capability{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading capabilities dir %s: %w", capsDir, err)
	}

	caps := make(map[string]*Capability, len(entries))

	// First pass: load .yaml action capabilities.
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".yaml")
		path := filepath.Join(capsDir, e.Name())

		cap, err := parseCapabilityYAML(name, path)
		if err != nil {
			return nil, fmt.Errorf("parsing capability %s: %w", path, err)
		}
		caps[name] = cap
	}

	// Second pass: load .md capabilities. .md files override .yaml if both exist.
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		path := filepath.Join(capsDir, e.Name())

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading capability file %s: %w", path, err)
		}

		cap, err := parseCapabilityMD(name, data)
		if err != nil {
			return nil, fmt.Errorf("parsing capability %s: %w", path, err)
		}
		caps[name] = cap
	}

	return caps, nil
}

// parseCapabilityYAML parses a YAML capability file (action capabilities).
// The fallbackName is used when the file's name field is absent.
func parseCapabilityYAML(fallbackName, path string) (*Capability, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	// Parse into a raw struct to avoid field name collisions with SchemaNode.
	var raw struct {
		Name         string               `yaml:"name"`
		Description  string               `yaml:"description"`
		Type         string               `yaml:"type"`
		CallableFrom string               `yaml:"callable_from"`
		Run          string               `yaml:"run"`
		Params       map[string]ParamDef  `yaml:"params"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing YAML capability: %w", err)
	}

	name := raw.Name
	if name == "" {
		name = fallbackName
	}

	return &Capability{
		Name:         name,
		Description:  raw.Description,
		Type:         raw.Type,
		CallableFrom: raw.CallableFrom,
		Run:          raw.Run,
		Params:       raw.Params,
	}, nil
}

// parseCapabilityMD parses a capability Markdown file (frontmatter + body).
// The fallbackName is used when the frontmatter doesn't include a "name" field.
func parseCapabilityMD(fallbackName string, data []byte) (*Capability, error) {
	fm, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, err
	}

	cap := &Capability{
		Name:         stringField(fm, "name", fallbackName),
		Description:  stringField(fm, "description", ""),
		Type:         stringField(fm, "type", ""),
		CallableFrom: stringField(fm, "callable_from", ""),
		Model:        stringField(fm, "model", ""),
		Body:         string(body),
		Tools:        stringSliceField(fm, "tools"),
	}
	return cap, nil
}

// stringSliceField extracts a []string value from a generic map.
// Returns nil if the key is absent or not a []interface{}.
func stringSliceField(m map[string]any, key string) []string {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// parseFrontmatter splits Markdown content into its YAML frontmatter and body.
// The frontmatter is returned as a generic map[string]any for flexible access.
// If there is no frontmatter delimiter, fm is nil and body is the full content.
func parseFrontmatter(data []byte) (map[string]any, []byte, error) {
	const delim = "---"

	lines := bytes.Split(data, []byte("\n"))
	if len(lines) < 2 || strings.TrimRight(string(lines[0]), "\r") != delim {
		return nil, data, nil
	}

	// Find the closing ---
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(string(lines[i]), "\r") == delim {
			end = i
			break
		}
	}
	if end == -1 {
		return nil, data, fmt.Errorf("unclosed frontmatter block")
	}

	fmBytes := bytes.Join(lines[1:end], []byte("\n"))
	body := bytes.Join(lines[end+1:], []byte("\n"))

	var fm map[string]any
	if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
		return nil, nil, fmt.Errorf("parsing frontmatter YAML: %w", err)
	}

	return fm, body, nil
}

// stringField extracts a string value from a generic map, returning fallback if absent or wrong type.
func stringField(m map[string]any, key, fallback string) string {
	if m == nil {
		return fallback
	}
	v, ok := m[key]
	if !ok {
		return fallback
	}
	s, ok := v.(string)
	if !ok {
		return fallback
	}
	return s
}

// initSettings loads <ext-dir>/settings.json into ext.Settings, applies
// schema defaults for missing keys, and writes the file back if defaults were
// added. Warns (never errors) on missing required settings or type mismatches.
func initSettings(ext *Extension) error {
	path := filepath.Join(ext.Dir, "settings.json")
	settings, err := readJSONFile(path)
	if err != nil {
		return err
	}
	if settings == nil {
		settings = make(map[string]any)
	}

	// Apply schema defaults and collect warnings.
	var warns []string
	settings, warns = applyDefaults(settings, ext.Manifest.Settings)
	for _, w := range warns {
		log.Printf("extensions: %s: %s", ext.Manifest.Name, w)
	}

	// Warn about declared required settings that are still absent after defaults.
	for _, req := range ext.Manifest.SettingsRequired {
		if _, ok := settings[req]; !ok {
			log.Printf("extensions: %s: required setting %q is missing (no default provided)", ext.Manifest.Name, req)
		}
	}

	// Warn about type mismatches in loaded settings (lenient — don't reject).
	for key, node := range ext.Manifest.Settings {
		if val, ok := settings[key]; ok {
			warns := validateLenient(val, node, key)
			for _, w := range warns {
				log.Printf("extensions: %s: settings: %s", ext.Manifest.Name, w)
			}
		}
	}

	ext.Settings = settings

	// If we added any defaults, persist the updated settings.json.
	if len(ext.Manifest.Settings) > 0 {
		if err := writeJSONFile(path, settings); err != nil {
			log.Printf("extensions: %s: could not persist settings defaults: %v", ext.Manifest.Name, err)
		}
	}

	return nil
}

// initState loads <ext-dir>/state.json into ext.State.
// A missing file is treated as empty state (not an error).
func initState(ext *Extension) error {
	path := filepath.Join(ext.Dir, "state.json")
	state, err := readJSONFile(path)
	if err != nil {
		return err
	}
	if state == nil {
		state = make(map[string]any)
	}
	ext.State = state
	return nil
}

// readJSONFile reads a JSON file and unmarshals it into map[string]any.
// Returns (nil, nil) if the file does not exist.
func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return m, nil
}

// writeJSONFile marshals data as indented JSON and writes it to path.
func writeJSONFile(path string, data any) error {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
