package plugins

import (
	"fmt"
	"os"
	"path/filepath"
)

// SettingsGet returns the current value for the given settings key.
// Returns nil (no error) if the key does not exist.
func (e *Plugin) SettingsGet(key string) any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Settings[key]
}

// SettingsSet writes a new value for the given settings key.
// If the plugin manifest declares a schema for this key, the value is
// validated against it; a type mismatch returns an error and nothing is written.
// On success the in-memory settings map is updated and settings.json is persisted.
func (e *Plugin) SettingsSet(key string, value any) error {
	// Schema validation is lock-free: the schema is read-only after load.
	if schema, ok := e.Manifest.Settings[key]; ok {
		if err := validateStrict(value, schema, key); err != nil {
			return fmt.Errorf("settings_set %q: %w", key, err)
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.Settings == nil {
		e.Settings = make(map[string]any)
	}
	e.Settings[key] = value

	if err := os.MkdirAll(filepath.Join(e.Dir, ".bud-plugin"), 0o755); err != nil {
		return err
	}
	return writeJSONFile(filepath.Join(e.Dir, ".bud-plugin", "settings.json"), e.Settings)
}
