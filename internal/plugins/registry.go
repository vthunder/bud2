package plugins

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// Registry holds all successfully loaded plugins, ordered by dependency.
type Registry struct {
	byName map[string]*Plugin
	order  []string // topological load order (dependencies before dependents)
}

// LoadAll loads plugins from each of the given dirs in order.
// Later dirs override earlier ones when the same plugin name appears in multiple dirs.
//
// Plugins are ordered by their declared requires.plugins dependency graph.
// If a cycle is detected, the cycled plugins are excluded from the Registry.
//
// Empty strings in dirs are silently skipped.
func LoadAll(dirs ...string) (*Registry, error) {
	exts := make(map[string]*Plugin)
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if err := loadDir(dir, exts); err != nil {
			return nil, fmt.Errorf("plugins: loading dir %s: %w", dir, err)
		}
	}

	// Topological sort to determine load order and detect cycles.
	order, cycled, err := topoSort(exts)
	if err != nil {
		return nil, err
	}

	// Remove cycled plugins from the registry.
	for _, name := range cycled {
		log.Printf("plugins: %s: excluded from registry due to dependency cycle", name)
		delete(exts, name)
	}

	// Filter order to exclude cycled plugins.
	filtered := order[:0]
	for _, name := range order {
		if _, ok := exts[name]; ok {
			filtered = append(filtered, name)
		}
	}

	return &Registry{byName: exts, order: filtered}, nil
}

// loadDir scans dir for subdirectories and attempts to load each as a plugin.
// Subdirs without plugin.yaml are silently skipped (they may be non-plugin dirs).
// Successfully loaded plugins are added to exts (overwriting existing entries with the same name).
func loadDir(dir string, exts map[string]*Plugin) error {
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
		// Silently skip dirs without .bud-plugin/extension.yaml — they're not extensions.
		if _, statErr := os.Stat(filepath.Join(extDir, ".bud-plugin", "plugin.yaml")); statErr != nil {
			continue
		}
		ext, err := LoadPlugin(extDir)
		if err != nil {
			log.Printf("plugins: skipping %s: %v", extDir, err)
			continue
		}
		if existing, ok := exts[ext.Manifest.Name]; ok {
			log.Printf("plugins: %s: overriding plugin loaded from %s", ext.Manifest.Name, existing.Dir)
		}
		exts[ext.Manifest.Name] = ext
	}
	return nil
}

// topoSort computes a topological ordering of plugins based on their
// requires.plugins dependency declarations. It uses DFS to detect cycles.
//
// Returns:
//   - order: names in dependency-first order (safe to iterate for initialization)
//   - cycled: names of plugins that participate in at least one cycle
//   - err: non-nil only for structural problems (missing declared deps, etc.)
//
// Cycled plugins are reported via the log and excluded by the caller.
func topoSort(exts map[string]*Plugin) (order []string, cycled []string, err error) {
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
			log.Printf("plugins: dependency cycle detected: %s", strings.Join(cyclePath, " → "))
			for _, n := range cyclePath[:len(cyclePath)-1] {
				cycledSet[n] = true
			}
			return false
		}

		// Plugin not found in loaded set — skip with warning.
		if _, ok := exts[name]; !ok {
			log.Printf("plugins: %s: required plugin not found (skipping)", name)
			state[name] = visited
			return true
		}

		state[name] = visiting
		stack = append(stack, name)

		ok := true
		for _, dep := range exts[name].Manifest.Requires.Plugins {
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

// Get returns the Plugin with the given name, or nil if not found.
func (r *Registry) Get(name string) *Plugin {
	return r.byName[name]
}

// All returns all plugins in topological dependency order.
func (r *Registry) All() []*Plugin {
	out := make([]*Plugin, 0, len(r.order))
	for _, name := range r.order {
		if ext, ok := r.byName[name]; ok {
			out = append(out, ext)
		}
	}
	return out
}

// Capabilities returns the names of all capabilities across all loaded plugins.
// Names are in the form "<plugin-name>:<capability-name>".
func (r *Registry) Capabilities() []string {
	var names []string
	for _, ext := range r.All() {
		for capName := range ext.Capabilities {
			names = append(names, ext.Manifest.Name+":"+capName)
		}
	}
	return names
}

// Len returns the number of plugins in the registry.
func (r *Registry) Len() int {
	return len(r.byName)
}

// GetCapabilityByFullName looks up a capability by its "pluginname:capname" full name.
// Returns (capability, plugin, true) on success, or (nil, nil, false) if not found.
func (r *Registry) GetCapabilityByFullName(fullName string) (*Capability, *Plugin, bool) {
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

// FindCapabilityByName searches all plugins for a capability with the given short name.
// If multiple plugins define a capability with the same name, returns the first match.
// Returns (nil, nil, false) if not found.
func (r *Registry) FindCapabilityByName(name string) (*Capability, *Plugin, bool) {
	for _, ext := range r.All() {
		if cap, ok := ext.Capabilities[name]; ok {
			return cap, ext, true
		}
	}
	return nil, nil, false
}

// CapabilitiesOfType returns all capabilities across all plugins with the given type
// and a callable_from value that includes model invocation ("model" or "both").
// Each entry is a (fullName, capability, plugin) triple.
func (r *Registry) CapabilitiesOfType(capType string) []struct {
	FullName  string
	Cap      *Capability
	Ext      *Plugin
} {
	var results []struct {
		FullName string
		Cap      *Capability
		Ext      *Plugin
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
				Ext      *Plugin
			}{
				FullName: ext.Manifest.Name + ":" + capName,
				Cap:      cap,
				Ext:      ext,
			})
		}
	}
	return results
}
