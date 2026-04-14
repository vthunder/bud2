package paths

import (
	"log"
	"os"
	"path/filepath"
)

const DefaultsDir = "state-defaults"

// LogDir returns the bud log directory: ~/Library/Logs/bud/
func LogDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "bud")
}

// ResolveFile returns the content of a file from the state directory, falling
// back to state-defaults if the file does not exist in state. The relPath is
// relative to the "system" subdirectory (e.g. "core.md", "reflexes/gtd-handler.yaml").
// Returns the content and true if found in state, content and false if from defaults,
// or empty string and false if not found at all.
func ResolveFile(statePath, relPath string) (string, bool) {
	stateFile := filepath.Join(statePath, "system", relPath)
	if data, err := os.ReadFile(stateFile); err == nil {
		return string(data), true
	}

	defaultsFile := filepath.Join(DefaultsDir, "system", relPath)
	if data, err := os.ReadFile(defaultsFile); err == nil {
		log.Printf("[paths] %s: using defaults (no override in state)", relPath)
		return string(data), false
	}

	log.Printf("[paths] Warning: %s not found in state or defaults", relPath)
	return "", false
}

// MergeDir returns a merged list of file paths from both the state directory
// and state-defaults, for a given subdirectory (e.g. "reflexes", "workflows",
// "guides"). State files take priority — if a file with the same name exists in
// both, only the state version is included. Only files matching the given
// extensions are included (e.g. ".yaml", ".yml").
func MergeDir(statePath, subDir string, extensions []string) []string {
	defaultsDir := filepath.Join(DefaultsDir, "system", subDir)
	stateDir := filepath.Join(statePath, "system", subDir)

	extSet := make(map[string]bool, len(extensions))
	for _, e := range extensions {
		extSet[e] = true
	}

	type entry struct {
		name string
		path string
	}

	collect := func(dir string) []entry {
		var entries []entry
		infos, err := os.ReadDir(dir)
		if err != nil {
			return nil
		}
		for _, info := range infos {
			if info.IsDir() {
				continue
			}
			if !extSet[filepath.Ext(info.Name())] {
				continue
			}
			entries = append(entries, entry{
				name: info.Name(),
				path: filepath.Join(dir, info.Name()),
			})
		}
		return entries
	}

	defaults := collect(defaultsDir)
	states := collect(stateDir)

	// Build map: name → path, state overrides defaults
	merged := make(map[string]string, len(defaults)+len(states))
	for _, e := range defaults {
		merged[e.name] = e.path
	}
	for _, e := range states {
		merged[e.name] = e.path
	}

	// Return in deterministic order (sorted by name)
	result := make([]string, 0, len(merged))
	for _, e := range defaults {
		if p, ok := merged[e.name]; ok {
			result = append(result, p)
			delete(merged, e.name)
		}
	}
	for _, e := range states {
		if p, ok := merged[e.name]; ok {
			result = append(result, p)
			delete(merged, e.name)
		}
	}
	return result
}

// EnsureStateSystemDirs creates the state/system directory and any necessary
// subdirectories, but does NOT copy any files from defaults.
func EnsureStateSystemDirs(statePath string) {
	systemDir := filepath.Join(statePath, "system")
	os.MkdirAll(systemDir, 0755)
	for _, sub := range []string{
		"reflexes",
		"workflows",
		"guides",
		"plugins",
		"profiles",
		"queues",
	} {
		os.MkdirAll(filepath.Join(systemDir, sub), 0755)
	}
}
