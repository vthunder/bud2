package extensions_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vthunder/bud2/internal/extensions"
	"github.com/vthunder/bud2/internal/mcp"
)

// --- test helpers specific to action tests ---

// writeCapabilityYAMLRaw writes raw YAML bytes as a capability file in <extDir>/capabilities/.
func writeCapabilityYAMLRaw(t *testing.T, extDir, name string, content []byte) {
	t.Helper()
	capsDir := filepath.Join(extDir, "capabilities")
	if err := os.MkdirAll(capsDir, 0o755); err != nil {
		t.Fatalf("creating capabilities dir: %v", err)
	}
	path := filepath.Join(capsDir, name+".yaml")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// writeScript writes an executable shell script into <extDir>/scripts/<name>.
// Returns the path relative to extDir (e.g., "scripts/action.sh").
func writeScript(t *testing.T, extDir, name, body string) string {
	t.Helper()
	scriptsDir := filepath.Join(extDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("creating scripts dir: %v", err)
	}
	path := filepath.Join(scriptsDir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing script %s: %v", path, err)
	}
	return "scripts/" + name // relative to extDir
}

// makeEchoScript writes a script that reads JSON from stdin and echoes it back
// wrapped as {"received":<stdin>}. Returns the relative path from extDir.
func makeEchoScript(t *testing.T, extDir string) string {
	t.Helper()
	body := `#!/bin/sh
input=$(cat)
printf '{"received":%s}' "$input"
`
	return writeScript(t, extDir, "echo.sh", body)
}

// makeTestExtDir creates a temp extension directory with the given name,
// a minimal extension.yaml, and returns the dir path.
func makeTestExtDir(t *testing.T, extName string) string {
	t.Helper()
	dir := t.TempDir()
	writeExtensionYAML(t, dir, map[string]any{
		"name":         extName,
		"description":  extName + " test extension",
		"behaviors":    []any{},
		"capabilities": map[string]any{},
	})
	return dir
}

// makeSystemRegistry places an extension directory at <systemDir>/<extName>/
// and returns a loaded Registry. extDir must already contain a valid extension.yaml.
func makeSystemRegistry(t *testing.T, extDir string) *extensions.Registry {
	t.Helper()
	systemDir := t.TempDir()
	// Determine ext name from the directory.
	entries, err := os.ReadDir(extDir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", extDir, err)
	}
	// Find extension.yaml to get name (reuse LoadExtension for this).
	ext, err := extensions.LoadExtension(extDir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}
	_ = entries

	linkDir := filepath.Join(systemDir, ext.Manifest.Name)
	// Copy the extension dir contents into the system dir.
	if err := copyDir(t, extDir, linkDir); err != nil {
		t.Fatalf("copyDir: %v", err)
	}

	reg, err := extensions.LoadAll(systemDir, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	return reg
}

// copyDir recursively copies src into dst.
func copyDir(t *testing.T, src, dst string) error {
	t.Helper()
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// --- TestActionProxy_LoadYAMLCapability ---

// TestActionProxy_LoadYAMLCapability verifies that a .yaml capability file is
// parsed by LoadExtension and exposes the correct Capability fields.
func TestActionProxy_LoadYAMLCapability(t *testing.T) {
	dir := makeTestExtDir(t, "test-ext")

	capYAML := `name: fetch-price
description: Fetch the current price
type: action
callable_from: both
run: scripts/fetch.sh
params:
  symbol:
    type: string
    description: Stock ticker symbol
    required: true
  limit:
    type: integer
    description: Max results
`
	writeCapabilityYAMLRaw(t, dir, "fetch-price", []byte(capYAML))

	ext, err := extensions.LoadExtension(dir)
	if err != nil {
		t.Fatalf("LoadExtension: %v", err)
	}

	cap, ok := ext.Capabilities["fetch-price"]
	if !ok {
		t.Fatal("capability 'fetch-price' not found after loading .yaml file")
	}

	if cap.Type != "action" {
		t.Errorf("Type = %q, want %q", cap.Type, "action")
	}
	if cap.CallableFrom != "both" {
		t.Errorf("CallableFrom = %q, want %q", cap.CallableFrom, "both")
	}
	if cap.Run != "scripts/fetch.sh" {
		t.Errorf("Run = %q, want %q", cap.Run, "scripts/fetch.sh")
	}
	if cap.Description != "Fetch the current price" {
		t.Errorf("Description = %q, want %q", cap.Description, "Fetch the current price")
	}
	if len(cap.Params) != 2 {
		t.Errorf("len(Params) = %d, want 2", len(cap.Params))
	}

	sym := cap.Params["symbol"]
	if sym.Type != "string" {
		t.Errorf("symbol.Type = %q, want %q", sym.Type, "string")
	}
	if !sym.Required {
		t.Error("symbol.Required should be true")
	}
	if sym.Description != "Stock ticker symbol" {
		t.Errorf("symbol.Description = %q, want %q", sym.Description, "Stock ticker symbol")
	}

	lim := cap.Params["limit"]
	if lim.Required {
		t.Error("limit.Required should be false")
	}
	if lim.Type != "integer" {
		t.Errorf("limit.Type = %q, want %q", lim.Type, "integer")
	}
}

// --- TestActionProxy_RegisterMCPTools_Both ---

// TestActionProxy_RegisterMCPTools_Both verifies that an action with
// callable_from:both is registered as an MCP tool named "<ext>:<cap>".
func TestActionProxy_RegisterMCPTools_Both(t *testing.T) {
	dir := makeTestExtDir(t, "price-tracker")
	scriptRelPath := makeEchoScript(t, dir)

	capYAML := fmt.Sprintf(`name: fetch-price
description: Fetch the current price
type: action
callable_from: both
run: %s
params:
  symbol:
    type: string
    required: true
`, scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "fetch-price", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)
	server := mcp.NewServer()
	proxy.RegisterMCPTools(server)

	names := server.ToolNames()
	found := false
	for _, n := range names {
		if n == "price-tracker:fetch-price" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("MCP tool 'price-tracker:fetch-price' not registered; tools = %v", names)
	}
}

// --- TestActionProxy_RegisterMCPTools_DirectNotRegistered ---

// TestActionProxy_RegisterMCPTools_DirectNotRegistered verifies that an action with
// callable_from:direct is NOT registered as an MCP tool.
func TestActionProxy_RegisterMCPTools_DirectNotRegistered(t *testing.T) {
	dir := makeTestExtDir(t, "test-ext")
	scriptRelPath := makeEchoScript(t, dir)

	capYAML := fmt.Sprintf(`name: internal-action
description: Internal only
type: action
callable_from: direct
run: %s
params: {}
`, scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "internal-action", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)
	server := mcp.NewServer()
	proxy.RegisterMCPTools(server)

	for _, n := range server.ToolNames() {
		if n == "test-ext:internal-action" {
			t.Errorf("callable_from:direct action should NOT be registered as MCP tool, but found %q", n)
		}
	}
}

// --- TestActionProxy_Call_PassesParamsAsJSON ---

// TestActionProxy_Call_PassesParamsAsJSON verifies that the action proxy passes
// params as JSON on stdin and returns stdout as the result.
func TestActionProxy_Call_PassesParamsAsJSON(t *testing.T) {
	dir := makeTestExtDir(t, "test-ext")

	// Script reads JSON from stdin and echoes it back wrapped in {"received":...}.
	scriptRelPath := makeEchoScript(t, dir)

	capYAML := fmt.Sprintf(`name: echo-action
description: Echo input
type: action
callable_from: both
run: %s
params:
  symbol:
    type: string
    required: true
`, scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "echo-action", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)

	result, err := proxy.Call("test-ext:echo-action", map[string]any{"symbol": "AAPL"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v — raw: %q", err, result)
	}
	received, ok := parsed["received"].(map[string]any)
	if !ok {
		t.Fatalf("result['received'] not a map: %T — %v", parsed["received"], parsed["received"])
	}
	if received["symbol"] != "AAPL" {
		t.Errorf("received['symbol'] = %v, want 'AAPL'", received["symbol"])
	}
}

// --- TestActionProxy_Call_RequiredParamMissing ---

// TestActionProxy_Call_RequiredParamMissing verifies that a missing required param
// returns an error before the script is invoked.
func TestActionProxy_Call_RequiredParamMissing(t *testing.T) {
	dir := makeTestExtDir(t, "test-ext")
	scriptRelPath := makeEchoScript(t, dir)

	capYAML := fmt.Sprintf(`name: strict-action
description: Requires symbol
type: action
callable_from: direct
run: %s
params:
  symbol:
    type: string
    required: true
`, scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "strict-action", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)

	_, err := proxy.Call("test-ext:strict-action", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing required param, got nil")
	}
	if !strings.Contains(err.Error(), "symbol") {
		t.Errorf("error should mention missing param name 'symbol': %v", err)
	}
}

// --- TestActionProxy_Call_UnknownAction ---

// TestActionProxy_Call_UnknownAction verifies that calling an unknown action name
// returns an error.
func TestActionProxy_Call_UnknownAction(t *testing.T) {
	systemDir := t.TempDir()
	reg, err := extensions.LoadAll(systemDir, "")
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	proxy := extensions.NewActionProxy(reg)

	_, err = proxy.Call("no-such-ext:no-such-action", map[string]any{})
	if err == nil {
		t.Fatal("expected error for unknown action, got nil")
	}
}

// --- TestActionProxy_DirectCallableFromDirect ---

// TestActionProxy_DirectCallableFromDirect verifies that a callable_from:direct action
// is reachable via Call even though it is NOT registered as an MCP tool.
func TestActionProxy_DirectCallableFromDirect(t *testing.T) {
	dir := makeTestExtDir(t, "test-ext")

	scriptBody := `#!/bin/sh
echo "ok"
`
	scriptRelPath := writeScript(t, dir, "direct.sh", scriptBody)
	capYAML := fmt.Sprintf(`name: direct-only
description: Only direct
type: action
callable_from: direct
run: %s
params: {}
`, scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "direct-only", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)

	// Verify NOT registered as MCP tool.
	server := mcp.NewServer()
	proxy.RegisterMCPTools(server)
	for _, n := range server.ToolNames() {
		if n == "test-ext:direct-only" {
			t.Errorf("callable_from:direct should NOT be registered as MCP tool, found %q", n)
		}
	}

	// Verify Call works via the proxy's direct path.
	result, err := proxy.Call("test-ext:direct-only", map[string]any{})
	if err != nil {
		t.Fatalf("Call on callable_from:direct action: %v", err)
	}
	if result != "ok" {
		t.Errorf("result = %q, want %q", result, "ok")
	}
}

// --- TestActionProxy_MCPToolCallsScript ---

// TestActionProxy_MCPToolCallsScript verifies that calling the registered MCP tool
// routes through the shell script end-to-end (via server.Call).
func TestActionProxy_MCPToolCallsScript(t *testing.T) {
	dir := makeTestExtDir(t, "price-tracker")

	// Script extracts "symbol" from JSON stdin and echoes it.
	scriptBody := `#!/bin/sh
input=$(cat)
# Extract symbol value from {"symbol":"GOOG"} etc.
symbol=$(printf '%s' "$input" | sed 's/.*"symbol":"\([^"]*\)".*/\1/')
echo "$symbol"
`
	scriptRelPath := writeScript(t, dir, "fetch.sh", scriptBody)
	capYAML := fmt.Sprintf(`name: fetch-price
description: Fetch current price
type: action
callable_from: both
run: %s
params:
  symbol:
    type: string
    description: Ticker symbol
    required: true
`, scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "fetch-price", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)
	server := mcp.NewServer()
	proxy.RegisterMCPTools(server)

	// Call via MCP server (as the model would call it).
	result, err := server.Call("price-tracker:fetch-price", map[string]any{"symbol": "GOOG"})
	if err != nil {
		t.Fatalf("server.Call: %v", err)
	}
	if result != "GOOG" {
		t.Errorf("result = %q, want %q", result, "GOOG")
	}
}

// --- TestActionProxy_HasAction ---

// TestActionProxy_HasAction verifies the HasAction helper.
func TestActionProxy_HasAction(t *testing.T) {
	dir := makeTestExtDir(t, "test-ext")
	scriptRelPath := makeEchoScript(t, dir)
	capYAML := fmt.Sprintf("name: my-action\ndescription: d\ntype: action\ncallable_from: both\nrun: %s\nparams: {}\n", scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "my-action", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)

	if !proxy.HasAction("test-ext:my-action") {
		t.Error("HasAction('test-ext:my-action') should be true")
	}
	if proxy.HasAction("test-ext:nonexistent") {
		t.Error("HasAction('test-ext:nonexistent') should be false")
	}
}

// --- TestActionProxy_ModelCallableFrom ---

// TestActionProxy_ModelCallableFrom verifies that callable_from:model is registered
// as an MCP tool (same behavior as callable_from:both for MCP registration).
func TestActionProxy_ModelCallableFrom(t *testing.T) {
	dir := makeTestExtDir(t, "test-ext")
	scriptRelPath := makeEchoScript(t, dir)
	capYAML := fmt.Sprintf(`name: model-action
description: Model-only action
type: action
callable_from: model
run: %s
params: {}
`, scriptRelPath)
	writeCapabilityYAMLRaw(t, dir, "model-action", []byte(capYAML))

	reg := makeSystemRegistry(t, dir)
	proxy := extensions.NewActionProxy(reg)
	server := mcp.NewServer()
	proxy.RegisterMCPTools(server)

	found := false
	for _, n := range server.ToolNames() {
		if n == "test-ext:model-action" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("callable_from:model action should be registered as MCP tool; tools = %v", server.ToolNames())
	}
}
