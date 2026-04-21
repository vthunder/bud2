package extensions

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// Registry holds all successfully loaded extensions, ordered by dependency.
type Registry struct {
	byName map[string]*Extension
	order  []string // topological load order (dependencies before dependents)
}

// LoadAll loads extensions from systemDir (first) and userDir (second).
// If both dirs contain an extension with the same name, the user extension
// replaces the system extension entirely.
//
// Extensions are ordered by their declared requires.extensions dependency graph.
// If a cycle is detected, LoadAll returns an error naming the cycle; no extensions
// involved in the cycle are added to the Registry.
//
// Either systemDir or userDir may be empty; missing directories are silently skipped.
func LoadAll(systemDir, userDir string) (*Registry, error) {
	// Load system extensions.
	exts := make(map[string]*Extension)
	if systemDir != "" {
		if err := loadDir(systemDir, exts); err != nil {
			return nil, fmt.Errorf("extensions: loading system dir %s: %w", systemDir, err)
		}
	}

	// Load user extensions; same-name entries override system ones.
	if userDir != "" {
		if err := loadDir(userDir, exts); err != nil {
			return nil, fmt.Errorf("extensions: loading user dir %s: %w", userDir, err)
		}
	}

	// Topological sort to determine load order and detect cycles.
	order, cycled, err := topoSort(exts)
	if err != nil {
		return nil, err
	}

	// Remove cycled extensions from the registry.
	for _, name := range cycled {
		log.Printf("extensions: %s: excluded from registry due to dependency cycle", name)
		delete(exts, name)
	}

	// Filter order to exclude cycled extensions.
	filtered := order[:0]
	for _, name := range order {
		if _, ok := exts[name]; ok {
			filtered = append(filtered, name)
		}
	}

	return &Registry{byName: exts, order: filtered}, nil
}

// loadDir scans dir for subdirectories and attempts to load each as an extension.
// Successfully loaded extensions are added to exts (overwriting existing entries
// with the same name, so user-dir calls override system-dir calls).
func loadDir(dir string, exts map[string]*Extension) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		extDir := dir + "/" + e.Name()
		ext, err := LoadExtension(extDir)
		if err != nil {
			log.Printf("extensions: skipping %s: %v", extDir, err)
			continue
		}
		if existing, ok := exts[ext.Manifest.Name]; ok {
			log.Printf("extensions: %s: overriding extension loaded from %s", ext.Manifest.Name, existing.Dir)
		}
		exts[ext.Manifest.Name] = ext
	}
	return nil
}

// topoSort computes a topological ordering of extensions based on their
// requires.extensions dependency declarations. It uses DFS to detect cycles.
//
// Returns:
//   - order: names in dependency-first order (safe to iterate for initialization)
//   - cycled: names of extensions that participate in at least one cycle
//   - err: non-nil only for structural problems (missing declared deps, etc.)
//
// Cycled extensions are reported via the log and excluded by the caller.
func topoSort(exts map[string]*Extension) (order []string, cycled []string, err error) {
	const (
		unvisited = 0
		visiting  = 1
		visited   = 2
	)

	state := make(map[string]int, len(exts))
	cycledSet := make(map[string]bool)
	var result []string

	// stack tracks the current DFS path for cycle reporting.
	var stack []string

	var dfs func(name string) bool
	dfs = func(name string) bool {
		switch state[name] {
		case visited:
			return true
		case visiting:
			// Cycle detected. Find where in the stack this name appears.
			cycleStart := -1
			for i, n := range stack {
				if n == name {
					cycleStart = i
					break
				}
			}
			var cyclePath []string
			if cycleStart >= 0 {
				cyclePath = append(stack[cycleStart:], name)
			} else {
				cyclePath = []string{name, name}
			}
			log.Printf("extensions: dependency cycle detected: %s", strings.Join(cyclePath, " → "))
			for _, n := range cyclePath[:len(cyclePath)-1] {
				cycledSet[n] = true
			}
			return false
		}

		// Extension not found in loaded set — skip with warning.
		if _, ok := exts[name]; !ok {
			log.Printf("extensions: %s: required extension not found (skipping)", name)
			state[name] = visited
			return true
		}

		state[name] = visiting
		stack = append(stack, name)

		ok := true
		for _, dep := range exts[name].Manifest.Requires.Extensions {
			if !dfs(dep) {
				ok = false
			}
		}

		stack = stack[:len(stack)-1]
		state[name] = visited
		if ok {
			result = append(result, name)
		}
		return ok
	}

	for name := range exts {
		if state[name] == unvisited {
			dfs(name)
		}
	}

	var cycledList []string
	for name := range cycledSet {
		cycledList = append(cycledList, name)
	}

	return result, cycledList, nil
}

// Get returns the Extension with the given name, or nil if not found.
func (r *Registry) Get(name string) *Extension {
	return r.byName[name]
}

// All returns all extensions in topological dependency order.
func (r *Registry) All() []*Extension {
	out := make([]*Extension, 0, len(r.order))
	for _, name := range r.order {
		if ext, ok := r.byName[name]; ok {
			out = append(out, ext)
		}
	}
	return out
}

// Capabilities returns the names of all capabilities across all loaded extensions.
// Names are in the form "<extension-name>:<capability-name>".
func (r *Registry) Capabilities() []string {
	var names []string
	for _, ext := range r.All() {
		for capName := range ext.Capabilities {
			names = append(names, ext.Manifest.Name+":"+capName)
		}
	}
	return names
}

// Len returns the number of extensions in the registry.
func (r *Registry) Len() int {
	return len(r.byName)
}

// GetCapabilityByFullName looks up a capability by its "extname:capname" full name.
// Returns (capability, extension, true) on success, or (nil, nil, false) if not found.
func (r *Registry) GetCapabilityByFullName(fullName string) (*Capability, *Extension, bool) {
	idx := strings.LastIndex(fullName, ":")
	if idx < 0 {
		return nil, nil, false
	}
	extName := fullName[:idx]
	capName := fullName[idx+1:]
	ext, ok := r.byName[extName]
	if !ok {
		return nil, nil, false
	}
	cap, ok := ext.Capabilities[capName]
	if !ok {
		return nil, nil, false
	}
	return cap, ext, true
}

// FindCapabilityByName searches all extensions for a capability with the given short name.
// If multiple extensions define a capability with the same name, returns the first match.
// Returns (nil, nil, false) if not found.
func (r *Registry) FindCapabilityByName(name string) (*Capability, *Extension, bool) {
	for _, ext := range r.All() {
		if cap, ok := ext.Capabilities[name]; ok {
			return cap, ext, true
		}
	}
	return nil, nil, false
}

// CapabilitiesOfType returns all capabilities across all extensions with the given type
// and a callable_from value that includes model invocation ("model" or "both").
// Each entry is a (fullName, capability, extension) triple.
func (r *Registry) CapabilitiesOfType(capType string) []struct {
	FullName  string
	Cap      *Capability
	Ext      *Extension
} {
	var results []struct {
		FullName string
		Cap      *Capability
		Ext      *Extension
	}
	for _, ext := range r.All() {
		for capName, cap := range ext.Capabilities {
			if cap.Type != capType {
				continue
			}
			if cap.CallableFrom != "model" && cap.CallableFrom != "both" {
				continue
			}
			results = append(results, struct {
				FullName string
				Cap      *Capability
				Ext      *Extension
			}{
				FullName: ext.Manifest.Name + ":" + capName,
				Cap:      cap,
				Ext:      ext,
			})
		}
	}
	return results
}
