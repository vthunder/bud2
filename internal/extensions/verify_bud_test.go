package extensions_test

import (
	"testing"

	"github.com/vthunder/bud2/internal/extensions"
)

func TestBudExtension_LoadsWithTools(t *testing.T) {
	root := repoRoot(t)
	extDir := root + "/state-defaults/system/extensions/bud"

	ext, err := extensions.LoadExtension(extDir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}
	if ext.Manifest.Name != "bud" {
		t.Errorf("Name = %q, want %q", ext.Manifest.Name, "bud")
	}

	wantAgents := []string{"coder", "researcher", "reviewer", "writer"}
	for _, name := range wantAgents {
		cap, ok := ext.Capabilities[name]
		if !ok {
			t.Errorf("missing capability %q", name)
			continue
		}
		if cap.Type != "agent" {
			t.Errorf("capability %q: Type = %q, want agent", name, cap.Type)
		}
		if len(cap.Tools) == 0 {
			t.Errorf("capability %q: Tools is empty (expected tools from frontmatter)", name)
		}
		if cap.Body == "" {
			t.Errorf("capability %q: Body is empty", name)
		}
	}
}
