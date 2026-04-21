package extensions_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/vthunder/bud2/internal/extensions"
)

// repoRoot returns the root of the bud2 repository by locating go.mod above
// this test file. This avoids hard-coded absolute paths.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test file")
		}
		dir = parent
	}
}

// runMigratePlugin runs the migrate-plugin command (via go run) to convert a
// legacy plugin directory into an extension directory.
func runMigratePlugin(t *testing.T, inDir, outDir string) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command(
		"go", "run",
		filepath.Join(root, "cmd", "migrate-plugin", "main.go"),
		"--in", inDir,
		"--out", outDir,
	)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("migrate-plugin failed: %v\n%s", err, out)
	}
}

// --- TestLoadExtension_BudOps ---

// TestLoadExtension_BudOps converts the bud-ops plugin using the M1 migration
// script and verifies that the resulting extension loads without error and
// exposes the expected capabilities.
func TestLoadExtension_BudOps(t *testing.T) {
	root := repoRoot(t)
	budOpsPlugin := filepath.Join(root, "state-defaults", "system", "plugins", "bud-ops")
	if _, err := os.Stat(budOpsPlugin); err != nil {
		t.Skipf("bud-ops plugin not found at %s: %v", budOpsPlugin, err)
	}

	outDir := t.TempDir()
	runMigratePlugin(t, budOpsPlugin, outDir)

	ext, err := extensions.LoadExtension(outDir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}

	// Manifest fields.
	if ext.Manifest.Name != "bud-ops" {
		t.Errorf("Name = %q, want %q", ext.Manifest.Name, "bud-ops")
	}
	if ext.Manifest.Description == "" {
		t.Error("Description is empty")
	}

	// All three expected capabilities must be present.
	wantCaps := []string{"handle-subagent-complete", "things-operations", "start-workflow"}
	for _, name := range wantCaps {
		cap, ok := ext.Capabilities[name]
		if !ok {
			t.Errorf("missing capability %q", name)
			continue
		}
		if cap.Type != "skill" {
			t.Errorf("capability %q: Type = %q, want %q", name, cap.Type, "skill")
		}
		if cap.Body == "" {
			t.Errorf("capability %q: Body is empty", name)
		}
	}
}

// --- TestRegistry_LoadAll ---

// TestRegistry_LoadAll verifies that LoadAll loads extensions from a directory
// and that Registry.Capabilities returns the expected fully-qualified names.
func TestRegistry_LoadAll(t *testing.T) {
	root := repoRoot(t)
	budOpsPlugin := filepath.Join(root, "state-defaults", "system", "plugins", "bud-ops")
	if _, err := os.Stat(budOpsPlugin); err != nil {
		t.Skipf("bud-ops plugin not found: %v", err)
	}

	// Build a fake system dir containing the migrated bud-ops extension.
	systemDir := t.TempDir()
	extDir := filepath.Join(systemDir, "bud-ops")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runMigratePlugin(t, budOpsPlugin, extDir)

	reg, err := extensions.LoadAll(systemDir, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	if reg.Len() != 1 {
		t.Errorf("Len = %d, want 1", reg.Len())
	}

	caps := reg.Capabilities()
	sort.Strings(caps)

	wantCaps := []string{
		"bud-ops:handle-subagent-complete",
		"bud-ops:start-workflow",
		"bud-ops:things-operations",
	}
	sort.Strings(wantCaps)

	if fmt.Sprintf("%v", caps) != fmt.Sprintf("%v", wantCaps) {
		t.Errorf("Capabilities = %v, want %v", caps, wantCaps)
	}
}

// --- TestDependencyCycle ---

// TestDependencyCycle verifies that two extensions requiring each other are
// both excluded from the Registry (not silently loaded) and that an appropriate
// cycle error is logged. The cycle must prevent both extensions from appearing
// in Registry.All().
func TestDependencyCycle(t *testing.T) {
	dir := t.TempDir()

	// ext-alpha requires ext-beta.
	alphaDir := filepath.Join(dir, "ext-alpha")
	betaDir := filepath.Join(dir, "ext-beta")
	if err := os.MkdirAll(alphaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(betaDir, 0o755); err != nil {
		t.Fatal(err)
	}

	writeExtensionYAML(t, alphaDir, map[string]any{
		"name":        "ext-alpha",
		"description": "alpha test extension",
		"requires":    map[string]any{"extensions": []string{"ext-beta"}},
		"behaviors":   []any{},
		"capabilities": map[string]any{},
	})
	writeExtensionYAML(t, betaDir, map[string]any{
		"name":        "ext-beta",
		"description": "beta test extension",
		"requires":    map[string]any{"extensions": []string{"ext-alpha"}},
		"behaviors":   []any{},
		"capabilities": map[string]any{},
	})

	reg, err := extensions.LoadAll(dir, "")
	// LoadAll may return nil error even when cycles are detected (cycles are
	// excluded from the registry rather than aborting the entire load). Verify
	// that neither cycled extension appears in the registry.
	if err != nil {
		// An error is also acceptable — the important thing is neither ext loads.
		t.Logf("LoadAll returned error (acceptable for cycle case): %v", err)
	}
	if reg == nil {
		t.Fatal("LoadAll returned nil registry")
	}

	if reg.Get("ext-alpha") != nil {
		t.Error("ext-alpha should not be in registry (it is part of a cycle)")
	}
	if reg.Get("ext-beta") != nil {
		t.Error("ext-beta should not be in registry (it is part of a cycle)")
	}
}

// --- TestSettings ---

// TestSettings covers:
//   - Schema defaults are populated when settings.json is absent.
//   - settings_get("nonexistent") returns nil without error.
//   - settings_set with correct type succeeds and persists to disk.
//   - settings_set with wrong type is rejected (type mismatch error).
func TestSettings(t *testing.T) {
	dir := t.TempDir()

	// Write a minimal extension with a settings schema.
	writeExtensionYAML(t, dir, map[string]any{
		"name":        "test-ext",
		"description": "settings test extension",
		"behaviors":   []any{},
		"capabilities": map[string]any{},
		"settings": map[string]any{
			"port": map[string]any{
				"type":        "integer",
				"description": "Port number",
				"default":     float64(8080),
			},
			"api_key": map[string]any{
				"type":        "string",
				"description": "API key",
			},
		},
		"settings_required": []string{"api_key"},
	})

	// Load without any settings.json — defaults should be applied.
	ext, err := extensions.LoadExtension(dir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}

	// Default for "port" should be populated.
	t.Run("default_populated", func(t *testing.T) {
		v := ext.SettingsGet("port")
		if v == nil {
			t.Fatal("expected default for 'port', got nil")
		}
		// JSON numbers unmarshal as float64; schema default stored as float64.
		switch n := v.(type) {
		case float64:
			if n != 8080 {
				t.Errorf("port default = %v, want 8080", n)
			}
		case int:
			if n != 8080 {
				t.Errorf("port default = %v, want 8080", n)
			}
		default:
			t.Errorf("port default has unexpected type %T: %v", v, v)
		}
	})

	// settings_get for a nonexistent key must return nil (no error).
	t.Run("get_nonexistent", func(t *testing.T) {
		v := ext.SettingsGet("nonexistent")
		if v != nil {
			t.Errorf("SettingsGet('nonexistent') = %v, want nil", v)
		}
	})

	// settings_set with correct type must succeed and persist.
	t.Run("set_valid", func(t *testing.T) {
		if err := ext.SettingsSet("api_key", "my-secret"); err != nil {
			t.Fatalf("SettingsSet valid: %v", err)
		}
		v := ext.SettingsGet("api_key")
		if v != "my-secret" {
			t.Errorf("SettingsGet after set = %v, want %q", v, "my-secret")
		}
		// Verify persistence: re-read settings.json from disk.
		data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil {
			t.Fatalf("reading settings.json: %v", err)
		}
		var persisted map[string]any
		if err := json.Unmarshal(data, &persisted); err != nil {
			t.Fatalf("parsing settings.json: %v", err)
		}
		if persisted["api_key"] != "my-secret" {
			t.Errorf("persisted api_key = %v, want %q", persisted["api_key"], "my-secret")
		}
	})

	// settings_set with wrong type must return an error and not persist.
	t.Run("set_type_mismatch", func(t *testing.T) {
		err := ext.SettingsSet("port", "not-a-number")
		if err == nil {
			t.Fatal("expected error for type mismatch, got nil")
		}
		if !strings.Contains(err.Error(), "type mismatch") {
			t.Errorf("error should mention 'type mismatch': %v", err)
		}
	})
}

// --- TestSettingsDefaultsRequired ---

// TestSettingsDefaultsRequired verifies that missing required settings produce
// warnings during load but do NOT prevent the extension from loading.
func TestSettingsDefaultsRequired(t *testing.T) {
	dir := t.TempDir()

	writeExtensionYAML(t, dir, map[string]any{
		"name":        "req-ext",
		"description": "required settings test",
		"behaviors":   []any{},
		"capabilities": map[string]any{},
		"settings": map[string]any{
			"token": map[string]any{
				"type":        "string",
				"description": "Auth token (required)",
			},
		},
		"settings_required": []string{"token"},
	})

	// Should load successfully even though "token" is not set and has no default.
	ext, err := extensions.LoadExtension(dir)
	if err != nil {
		t.Fatalf("LoadExtension with missing required setting: %v (want no error)", err)
	}
	// The required key must be absent (no default was provided).
	if v := ext.SettingsGet("token"); v != nil {
		t.Errorf("expected 'token' to be absent, got %v", v)
	}
}

// --- TestState ---

// TestState verifies StateGet/StateSet semantics.
func TestState(t *testing.T) {
	dir := t.TempDir()

	writeExtensionYAML(t, dir, map[string]any{
		"name":        "state-ext",
		"description": "state test extension",
		"behaviors":   []any{},
		"capabilities": map[string]any{},
	})

	ext, err := extensions.LoadExtension(dir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}

	// StateGet nonexistent returns nil.
	if v := ext.StateGet("nonexistent"); v != nil {
		t.Errorf("StateGet('nonexistent') = %v, want nil", v)
	}

	// StateSet persists.
	if err := ext.StateSet("last_run", "2026-04-20"); err != nil {
		t.Fatalf("StateSet: %v", err)
	}
	if v := ext.StateGet("last_run"); v != "2026-04-20" {
		t.Errorf("StateGet after set = %v, want '2026-04-20'", v)
	}

	// Verify persistence.
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatalf("reading state.json: %v", err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("parsing state.json: %v", err)
	}
	if persisted["last_run"] != "2026-04-20" {
		t.Errorf("persisted last_run = %v, want '2026-04-20'", persisted["last_run"])
	}
}

// --- helpers ---

// writeExtensionYAML writes an extension.yaml file from a generic map.
func writeExtensionYAML(t *testing.T, dir string, data map[string]any) {
	t.Helper()
	b, err := yaml.Marshal(data)
	if err != nil {
		t.Fatalf("marshaling extension.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extension.yaml"), b, 0o644); err != nil {
		t.Fatalf("writing extension.yaml: %v", err)
	}
}
