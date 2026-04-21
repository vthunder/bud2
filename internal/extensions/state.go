package extensions

import (
	"path/filepath"
)

// StateGet returns the current value for the given state key.
// Returns nil (no error) if the key does not exist.
// The "_enabled" key is reserved by the runtime to track enable/disable status.
func (e *Extension) StateGet(key string) any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.State[key]
}

// StateSet writes a new value for the given state key and persists state.json.
// No schema validation is performed; state is arbitrary key-value storage.
func (e *Extension) StateSet(key string, value any) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.State == nil {
		e.State = make(map[string]any)
	}
	e.State[key] = value

	return writeJSONFile(filepath.Join(e.Dir, "state.json"), e.State)
}

// Enabled reports whether the extension is currently enabled.
// An extension is considered enabled if _enabled is absent (default on) or
// explicitly set to true.
func (e *Extension) Enabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.State["_enabled"]
	if !ok {
		return true // default: enabled
	}
	b, ok := v.(bool)
	return !ok || b
}
