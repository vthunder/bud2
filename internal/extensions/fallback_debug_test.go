package extensions_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vthunder/bud2/internal/extensions"
)

func TestWorkflowFallback(t *testing.T) {
	root := repoRoot(t)
	sysExtDir := filepath.Join(root, "state-defaults", "system", "extensions")
	userExtDir := filepath.Join(root, "state", "system", "extensions")

	reg, err := extensions.LoadAll(sysExtDir, userExtDir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	t.Logf("Registry: %d extensions", reg.Len())

	for _, name := range []string{"gtd-today", "gtd-inbox", "gtd-add"} {
		cap, ext, ok := reg.FindCapabilityByName(name)
		if !ok {
			t.Errorf("FindCapabilityByName(%q): not found", name)
			continue
		}
		t.Logf("Found %q: Type=%q, Dir=%s", name, cap.Type, ext.Dir)
		if cap.Type != "workflow" {
			t.Errorf("  cap.Type = %q, want %q", cap.Type, "workflow")
		}
		idx := strings.LastIndex(name, ":")
		capName := name[idx+1:]
		yamlPath := filepath.Join(ext.Dir, "skills", capName+".yaml")
		t.Logf("  YAML path: %s", yamlPath)
	}
}
