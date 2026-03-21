package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vthunder/bud2/internal/mcp"
)

const vmControlDefaultURL = "http://127.0.0.1:3099"
const vmControlPIDFile = "/tmp/vm-control.pid"
const vmControlScript = "/Users/thunder/src/bud2/state/projects/sandmill/vm-control-server.js"
const vmControlLog = "/tmp/vm-control.log"

func vmControlBase(deps *Dependencies) string {
	if deps.VMControlURL != "" {
		return deps.VMControlURL
	}
	return vmControlDefaultURL
}

// vmHTTP makes an HTTP request to the vm-control-server.
func vmHTTP(method, url string, body any) ([]byte, int, error) {
	return vmHTTPWithTimeout(method, url, body, 30*time.Second)
}

func vmHTTPWithTimeout(method, url string, body any, timeout time.Duration) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return data, resp.StatusCode, err
}

func registerVMBrowserTools(server *mcp.Server, deps *Dependencies) {
	base := vmControlBase(deps)

	// ── vm_start ─────────────────────────────────────────────────────────────

	server.RegisterTool("vm_start", mcp.ToolDef{
		Description: "Start the VM control server (headful Chrome with Mac OS 8 emulator). Singleton — safe to call if already running. The emulator loads in the background after the server starts; use vm_screenshot to check when it's ready.",
		Properties: map[string]mcp.PropDef{
			"url": {Type: "string", Description: "Emulator URL (default: http://localhost:8000/newhome)"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		// Already running?
		data, status, err := vmHTTP("GET", base+"/status", nil)
		if err == nil && status == 200 {
			var s map[string]any
			json.Unmarshal(data, &s)
			loaded, _ := s["loaded"].(bool)
			return fmt.Sprintf("VM control server already running (loaded=%v)", loaded), nil
		}

		// Stale PID file?
		if pidBytes, err := os.ReadFile(vmControlPIDFile); err == nil {
			pid, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
			if pid > 0 {
				if proc, err := os.FindProcess(pid); err == nil {
					if proc.Signal(syscall.Signal(0)) == nil {
						return fmt.Sprintf("VM control server (PID %d) running but not responding to HTTP — check logs: %s", pid, vmControlLog), nil
					}
				}
			}
			os.Remove(vmControlPIDFile)
		}

		emulatorURL := "http://localhost:8000/newhome"
		if u, ok := args["url"].(string); ok && u != "" {
			emulatorURL = u
		}

		// Truncate log file
		os.WriteFile(vmControlLog, nil, 0644)

		logF, err := os.OpenFile(vmControlLog, os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("open log file: %w", err)
		}

		cmd := exec.Command("node", vmControlScript)
		cmd.Env = append(os.Environ(), "VM_CONTROL_URL="+emulatorURL)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		cmd.Stdout = logF
		cmd.Stderr = logF

		if err := cmd.Start(); err != nil {
			logF.Close()
			return "", fmt.Errorf("start vm-control-server: %w", err)
		}
		pid := cmd.Process.Pid
		cmd.Process.Release()
		logF.Close()

		os.WriteFile(vmControlPIDFile, []byte(strconv.Itoa(pid)), 0644)

		// Wait up to 15s for HTTP to respond
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(500 * time.Millisecond)
			_, st, err := vmHTTP("GET", base+"/status", nil)
			if err == nil && st == 200 {
				return fmt.Sprintf("VM control server started (PID %d). Emulator loading in background — use vm_screenshot to check. Logs: %s", pid, vmControlLog), nil
			}
		}
		return fmt.Sprintf("VM control server started (PID %d) but not yet responding (still launching). Logs: %s", pid, vmControlLog), nil
	})

	// ── vm_stop ───────────────────────────────────────────────────────────────

	server.RegisterTool("vm_stop", mcp.ToolDef{
		Description: "Stop the VM control server (kills Chrome and emulator). The emulator state will be lost.",
	}, func(ctx any, args map[string]any) (string, error) {
		pidBytes, err := os.ReadFile(vmControlPIDFile)
		if err != nil {
			return "No PID file found — server may not be running", nil
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
		if err != nil || pid <= 0 {
			os.Remove(vmControlPIDFile)
			return "Invalid PID in PID file — cleaned up", nil
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			os.Remove(vmControlPIDFile)
			return fmt.Sprintf("Process %d not found — cleaned up PID file", pid), nil
		}
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			os.Remove(vmControlPIDFile)
			return fmt.Sprintf("Failed to send SIGTERM to PID %d: %v — cleaned up PID file", pid, err), nil
		}
		os.Remove(vmControlPIDFile)
		return fmt.Sprintf("Sent SIGTERM to vm-control-server (PID %d)", pid), nil
	})

	// ── vm_screenshot ─────────────────────────────────────────────────────────

	server.RegisterTool("vm_screenshot", mcp.ToolDef{
		Description: "Take a screenshot of the Mac OS 8 emulator canvas. Returns a base64-encoded PNG data URL. Use this to see the current state of the emulator.",
	}, func(ctx any, args map[string]any) (string, error) {
		data, status, err := vmHTTP("GET", base+"/screenshot", nil)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable (is it running? use vm_start): %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("screenshot failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return "", fmt.Errorf("parse response: %w", err)
		}
		if errMsg, ok := resp["error"].(string); ok {
			return "", fmt.Errorf("screenshot error: %s", errMsg)
		}
		imgData, _ := resp["data"].(string)
		fpath, _ := resp["path"].(string)
		method, _ := resp["method"].(string)
		return fmt.Sprintf("Screenshot (method=%s, saved=%s)\n%s", method, fpath, imgData), nil
	})

	// ── vm_click ──────────────────────────────────────────────────────────────

	server.RegisterTool("vm_click", mcp.ToolDef{
		Description: "Click at coordinates in the Mac OS 8 emulator. Uses the 640×480 emulator canvas coordinate space.",
		Properties: map[string]mcp.PropDef{
			"x":      {Type: "number", Description: "X coordinate (0–639)"},
			"y":      {Type: "number", Description: "Y coordinate (0–479)"},
			"button": {Type: "number", Description: "Mouse button: 0=left (default), 2=right"},
		},
		Required: []string{"x", "y"},
	}, func(ctx any, args map[string]any) (string, error) {
		x, _ := args["x"].(float64)
		y, _ := args["y"].(float64)
		button := 0.0
		if b, ok := args["button"].(float64); ok {
			button = b
		}
		body := map[string]any{"action": "mouse_click", "x": x, "y": y, "button": button}
		data, status, err := vmHTTP("POST", base+"/action", body)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_click failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Clicked at (%.0f, %.0f)", x, y), nil
	})

	// ── vm_type ───────────────────────────────────────────────────────────────

	server.RegisterTool("vm_type", mcp.ToolDef{
		Description: "Type text into the Mac OS 8 emulator using keyboard events. Click the target field first with vm_click.",
		Properties: map[string]mcp.PropDef{
			"text": {Type: "string", Description: "Text to type"},
		},
		Required: []string{"text"},
	}, func(ctx any, args map[string]any) (string, error) {
		text, ok := args["text"].(string)
		if !ok || text == "" {
			return "", fmt.Errorf("text is required")
		}
		body := map[string]any{"action": "type", "text": text}
		data, status, err := vmHTTP("POST", base+"/action", body)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_type failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Typed %d characters", len(text)), nil
	})

	// ── vm_key ────────────────────────────────────────────────────────────────

	server.RegisterTool("vm_key", mcp.ToolDef{
		Description: "Send a keyboard key press to the Mac OS 8 emulator.",
		Properties: map[string]mcp.PropDef{
			"code": {Type: "string", Description: "Key code: Enter, Tab, Escape, Backspace, Delete, Space, ArrowUp, ArrowDown, ArrowLeft, ArrowRight, F1–F12"},
		},
		Required: []string{"code"},
	}, func(ctx any, args map[string]any) (string, error) {
		code, ok := args["code"].(string)
		if !ok || code == "" {
			return "", fmt.Errorf("code is required")
		}
		body := map[string]any{"action": "key_press", "code": code}
		data, status, err := vmHTTP("POST", base+"/action", body)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_key failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Key pressed: %s", code), nil
	})

	// ── vm_actions ────────────────────────────────────────────────────────────

	server.RegisterTool("vm_actions", mcp.ToolDef{
		Description: `Execute a sequence of actions in the emulator or browser. Supported actions: wait, screenshot, mouse_click, mouse_move, key_press, type, navigate, evaluate, browser_click. Returns results for each step including screenshot data URLs.`,
		Properties: map[string]mcp.PropDef{
			"steps": {Type: "array", Description: `Array of action objects. Examples: {"action":"wait","ms":2000}, {"action":"screenshot"}, {"action":"mouse_click","x":320,"y":240}, {"action":"type","text":"hello\n"}, {"action":"key_press","code":"Enter"}, {"action":"navigate","url":"http://..."}, {"action":"evaluate","js":"document.title"}`},
		},
		Required: []string{"steps"},
	}, func(ctx any, args map[string]any) (string, error) {
		steps, ok := args["steps"].([]any)
		if !ok {
			return "", fmt.Errorf("steps must be an array")
		}
		// Calculate timeout: 30s base + sum of any explicit wait steps + 10s per non-wait step
		timeout := 30 * time.Second
		for _, s := range steps {
			sm, _ := s.(map[string]any)
			if sm == nil {
				continue
			}
			if sm["action"] == "wait" {
				if ms, ok := sm["ms"].(float64); ok {
					timeout += time.Duration(ms)*time.Millisecond + 5*time.Second
				}
			} else {
				timeout += 10 * time.Second
			}
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/actions", steps, timeout)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_actions failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		results, _ := resp["results"].([]any)
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Executed %d steps:\n", len(steps)))
		for i, r := range results {
			rm, _ := r.(map[string]any)
			isOK, _ := rm["ok"].(bool)
			result, _ := rm["result"].(map[string]any)
			if isOK {
				if imgData, hasImg := result["data"].(string); hasImg {
					fpath, _ := result["path"].(string)
					sb.WriteString(fmt.Sprintf("  [%d] screenshot (saved=%s)\n%s\n", i+1, fpath, imgData))
				} else {
					action, _ := result["action"].(string)
					if val, hasVal := result["value"]; hasVal {
						valJSON, _ := json.Marshal(val)
						sb.WriteString(fmt.Sprintf("  [%d] %s → %s\n", i+1, action, valJSON))
					} else {
						sb.WriteString(fmt.Sprintf("  [%d] %s ✓\n", i+1, action))
					}
				}
			} else {
				errMsg, _ := rm["error"].(string)
				sb.WriteString(fmt.Sprintf("  [%d] FAILED: %s\n", i+1, errMsg))
			}
		}
		return sb.String(), nil
	})

	// ── browser_screenshot ────────────────────────────────────────────────────

	server.RegisterTool("browser_screenshot", mcp.ToolDef{
		Description: "Take a screenshot of the browser page (the host Chrome window, not the emulated Mac). Returns base64 PNG data URL.",
	}, func(ctx any, args map[string]any) (string, error) {
		data, status, err := vmHTTP("GET", base+"/screenshot", nil)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("browser_screenshot failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return "", fmt.Errorf("parse response: %w", err)
		}
		if errMsg, ok := resp["error"].(string); ok {
			return "", fmt.Errorf("screenshot error: %s", errMsg)
		}
		imgData, _ := resp["data"].(string)
		fpath, _ := resp["path"].(string)
		return fmt.Sprintf("Browser screenshot saved to %s\n%s", fpath, imgData), nil
	})

	// ── browser_navigate ──────────────────────────────────────────────────────

	server.RegisterTool("browser_navigate", mcp.ToolDef{
		Description: "Navigate the Chrome browser to a URL (changes what the browser shows, not the emulated Mac).",
		Properties: map[string]mcp.PropDef{
			"url": {Type: "string", Description: "URL to navigate to"},
		},
		Required: []string{"url"},
	}, func(ctx any, args map[string]any) (string, error) {
		url, ok := args["url"].(string)
		if !ok || url == "" {
			return "", fmt.Errorf("url is required")
		}
		body := map[string]any{"action": "navigate", "url": url}
		data, status, err := vmHTTP("POST", base+"/action", body)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("browser_navigate failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Navigated to %s", url), nil
	})

	// ── browser_evaluate ──────────────────────────────────────────────────────

	server.RegisterTool("browser_evaluate", mcp.ToolDef{
		Description: "Evaluate JavaScript in the browser page context. Returns the result as JSON.",
		Properties: map[string]mcp.PropDef{
			"js": {Type: "string", Description: "JavaScript expression to evaluate (must return a JSON-serializable value)"},
		},
		Required: []string{"js"},
	}, func(ctx any, args map[string]any) (string, error) {
		js, ok := args["js"].(string)
		if !ok || js == "" {
			return "", fmt.Errorf("js is required")
		}
		body := map[string]any{"action": "evaluate", "js": js}
		data, status, err := vmHTTP("POST", base+"/action", body)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("browser_evaluate failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		result, _ := resp["result"].(map[string]any)
		if result != nil {
			if val, ok := result["value"]; ok {
				b, _ := json.Marshal(val)
				return string(b), nil
			}
		}
		return string(data), nil
	})

	// ── vm_double_click ───────────────────────────────────────────────────────

	server.RegisterTool("vm_double_click", mcp.ToolDef{
		Description: "Double-click at coordinates in the Mac OS 8 emulator (opens files, folders, apps).",
		Properties: map[string]mcp.PropDef{
			"x":        {Type: "number", Description: "X coordinate (0–639)"},
			"y":        {Type: "number", Description: "Y coordinate (0–479)"},
			"delay_ms": {Type: "number", Description: "Delay between clicks in ms, default 150"},
		},
		Required: []string{"x", "y"},
	}, func(ctx any, args map[string]any) (string, error) {
		x, _ := args["x"].(float64)
		y, _ := args["y"].(float64)
		delay := 150.0
		if d, ok := args["delay_ms"].(float64); ok {
			delay = d
		}
		body := map[string]any{"action": "mouse_double_click", "x": x, "y": y, "delay": delay}
		data, status, err := vmHTTP("POST", base+"/action", body)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_double_click failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Double-clicked at (%.0f, %.0f)", x, y), nil
	})

	// ── vm_drag ───────────────────────────────────────────────────────────────

	server.RegisterTool("vm_drag", mcp.ToolDef{
		Description: "Drag from one position to another in the emulator (move windows, resize, text selection).",
		Properties: map[string]mcp.PropDef{
			"x1": {Type: "number", Description: "Start X coordinate"},
			"y1": {Type: "number", Description: "Start Y coordinate"},
			"x2": {Type: "number", Description: "End X coordinate"},
			"y2": {Type: "number", Description: "End Y coordinate"},
		},
		Required: []string{"x1", "y1", "x2", "y2"},
	}, func(ctx any, args map[string]any) (string, error) {
		x1, _ := args["x1"].(float64)
		y1, _ := args["y1"].(float64)
		x2, _ := args["x2"].(float64)
		y2, _ := args["y2"].(float64)
		body := map[string]any{"action": "mouse_drag", "x1": x1, "y1": y1, "x2": x2, "y2": y2}
		data, status, err := vmHTTPWithTimeout("POST", base+"/action", body, 15*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_drag failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Dragged from (%.0f, %.0f) to (%.0f, %.0f)", x1, y1, x2, y2), nil
	})

	// ── vm_right_click ────────────────────────────────────────────────────────

	server.RegisterTool("vm_right_click", mcp.ToolDef{
		Description: "Right-click (Ctrl+click) at coordinates in the Mac OS 8 emulator for context menus.",
		Properties: map[string]mcp.PropDef{
			"x": {Type: "number", Description: "X coordinate (0–639)"},
			"y": {Type: "number", Description: "Y coordinate (0–479)"},
		},
		Required: []string{"x", "y"},
	}, func(ctx any, args map[string]any) (string, error) {
		x, _ := args["x"].(float64)
		y, _ := args["y"].(float64)
		body := map[string]any{"action": "mouse_right_click", "x": x, "y": y}
		data, status, err := vmHTTP("POST", base+"/action", body)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_right_click failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Right-clicked at (%.0f, %.0f)", x, y), nil
	})

	// ── vm_scroll ─────────────────────────────────────────────────────────────

	server.RegisterTool("vm_scroll", mcp.ToolDef{
		Description: "Scroll in the emulator using arrow keys.",
		Properties: map[string]mcp.PropDef{
			"direction": {Type: "string", Description: "up/down/left/right"},
			"amount":    {Type: "number", Description: "Number of scroll ticks, default 3"},
		},
		Required: []string{"direction"},
	}, func(ctx any, args map[string]any) (string, error) {
		direction, ok := args["direction"].(string)
		if !ok || direction == "" {
			return "", fmt.Errorf("direction is required")
		}
		amount := 3.0
		if a, ok := args["amount"].(float64); ok {
			amount = a
		}
		body := map[string]any{"action": "scroll", "direction": direction, "amount": amount}
		timeout := time.Duration(amount)*50*time.Millisecond + 10*time.Second
		data, status, err := vmHTTPWithTimeout("POST", base+"/action", body, timeout)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_scroll failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Scrolled %s × %.0f", direction, amount), nil
	})

	// ── vm_find_text ──────────────────────────────────────────────────────────

	server.RegisterTool("vm_find_text", mcp.ToolDef{
		Description: "Find text in the emulator via OCR. Returns coordinates without clicking. Use to verify text is visible or get coordinates for a subsequent action.",
		Properties: map[string]mcp.PropDef{
			"text":   {Type: "string", Description: "Text to find"},
			"region": {Type: "object", Description: "{x,y,width,height} to restrict OCR region"},
		},
		Required: []string{"text"},
	}, func(ctx any, args map[string]any) (string, error) {
		text, ok := args["text"].(string)
		if !ok || text == "" {
			return "", fmt.Errorf("text is required")
		}
		body := map[string]any{"text": text}
		if region, ok := args["region"]; ok {
			body["region"] = region
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/find-text", body, 45*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_find_text failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		if !found {
			return fmt.Sprintf("Text %q not found on screen", text), nil
		}
		x, _ := resp["x"].(float64)
		y, _ := resp["y"].(float64)
		conf, _ := resp["confidence"].(float64)
		return fmt.Sprintf("Found %q at (%.0f, %.0f) confidence=%.2f", text, x, y, conf), nil
	})

	// ── vm_click_text ─────────────────────────────────────────────────────────

	server.RegisterTool("vm_click_text", mcp.ToolDef{
		Description: "Find text in the emulator via OCR and click it. Useful when you know the label but not the pixel coordinates. Use offset_x/offset_y to click next to the label (e.g., click an input field to the right of its label).",
		Properties: map[string]mcp.PropDef{
			"text":     {Type: "string", Description: "Text to find and click"},
			"offset_x": {Type: "number", Description: "X offset from text center, default 0"},
			"offset_y": {Type: "number", Description: "Y offset from text center, default 0"},
			"region":   {Type: "object", Description: "{x,y,width,height} to restrict OCR region"},
		},
		Required: []string{"text"},
	}, func(ctx any, args map[string]any) (string, error) {
		text, ok := args["text"].(string)
		if !ok || text == "" {
			return "", fmt.Errorf("text is required")
		}
		body := map[string]any{"text": text}
		if ox, ok := args["offset_x"].(float64); ok {
			body["offset_x"] = ox
		}
		if oy, ok := args["offset_y"].(float64); ok {
			body["offset_y"] = oy
		}
		if region, ok := args["region"]; ok {
			body["region"] = region
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/click-text", body, 45*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_click_text failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		clicked, _ := resp["clicked"].(bool)
		if !found {
			return fmt.Sprintf("Text %q not found on screen", text), nil
		}
		x, _ := resp["x"].(float64)
		y, _ := resp["y"].(float64)
		if clicked {
			return fmt.Sprintf("Found and clicked %q at (%.0f, %.0f)", text, x, y), nil
		}
		return fmt.Sprintf("Found %q at (%.0f, %.0f) but click failed", text, x, y), nil
	})

	// ── vm_double_click_text ──────────────────────────────────────────────────

	server.RegisterTool("vm_double_click_text", mcp.ToolDef{
		Description: "Find text in the emulator via OCR and double-click it. Use for opening apps and files identified by their icon label.",
		Properties: map[string]mcp.PropDef{
			"text":     {Type: "string", Description: "Text to find and double-click"},
			"offset_x": {Type: "number", Description: "X offset from text center, default 0"},
			"offset_y": {Type: "number", Description: "Y offset from text center, default 0"},
			"region":   {Type: "object", Description: "{x,y,width,height} to restrict OCR region"},
		},
		Required: []string{"text"},
	}, func(ctx any, args map[string]any) (string, error) {
		text, ok := args["text"].(string)
		if !ok || text == "" {
			return "", fmt.Errorf("text is required")
		}
		body := map[string]any{"text": text}
		if ox, ok := args["offset_x"].(float64); ok {
			body["offset_x"] = ox
		}
		if oy, ok := args["offset_y"].(float64); ok {
			body["offset_y"] = oy
		}
		if region, ok := args["region"]; ok {
			body["region"] = region
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/double-click-text", body, 45*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_double_click_text failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		clicked, _ := resp["clicked"].(bool)
		if !found {
			return fmt.Sprintf("Text %q not found on screen", text), nil
		}
		x, _ := resp["x"].(float64)
		y, _ := resp["y"].(float64)
		if clicked {
			return fmt.Sprintf("Found and double-clicked %q at (%.0f, %.0f)", text, x, y), nil
		}
		return fmt.Sprintf("Found %q at (%.0f, %.0f) but click failed", text, x, y), nil
	})

	// ── vm_open_menu ──────────────────────────────────────────────────────────

	server.RegisterTool("vm_open_menu", mcp.ToolDef{
		Description: "Open a Mac OS 8 menu by name and optionally click a menu item. Example: menu='File', item='Open' to choose File > Open.",
		Properties: map[string]mcp.PropDef{
			"menu": {Type: "string", Description: "Menu name e.g. 'File', 'Edit', 'Apple'"},
			"item": {Type: "string", Description: "Menu item to click after opening"},
		},
		Required: []string{"menu"},
	}, func(ctx any, args map[string]any) (string, error) {
		menu, ok := args["menu"].(string)
		if !ok || menu == "" {
			return "", fmt.Errorf("menu is required")
		}
		body := map[string]any{"menu": menu}
		if item, ok := args["item"].(string); ok && item != "" {
			body["item"] = item
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/open-menu", body, 45*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_open_menu failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		if !found {
			return fmt.Sprintf("Menu %q not found in menubar", menu), nil
		}
		if item, ok := args["item"].(string); ok && item != "" {
			itemX := resp["item_x"]
			if itemX != nil {
				return fmt.Sprintf("Opened menu %q and clicked item %q", menu, item), nil
			}
			return fmt.Sprintf("Opened menu %q but item %q not found", menu, item), nil
		}
		return fmt.Sprintf("Opened menu %q", menu), nil
	})

	// ── vm_launch_app ─────────────────────────────────────────────────────────

	server.RegisterTool("vm_launch_app", mcp.ToolDef{
		Description: "Launch an application in the Mac OS 8 emulator by finding its icon label via OCR and double-clicking it.",
		Properties: map[string]mcp.PropDef{
			"name": {Type: "string", Description: "Application name as it appears on the desktop icon"},
		},
		Required: []string{"name"},
	}, func(ctx any, args map[string]any) (string, error) {
		name, ok := args["name"].(string)
		if !ok || name == "" {
			return "", fmt.Errorf("name is required")
		}
		body := map[string]any{"name": name}
		data, status, err := vmHTTPWithTimeout("POST", base+"/launch-app", body, 45*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_launch_app failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		if !found {
			return fmt.Sprintf("App %q not found on screen", name), nil
		}
		x, _ := resp["x"].(float64)
		y, _ := resp["y"].(float64)
		return fmt.Sprintf("Launched %q (double-clicked at %.0f, %.0f)", name, x, y), nil
	})

	// ── vm_wait_for_text ──────────────────────────────────────────────────────

	server.RegisterTool("vm_wait_for_text", mcp.ToolDef{
		Description: "Wait until specified text appears in the emulator (polls OCR). Use to wait for app launch, dialog boxes, network status changes, etc.",
		Properties: map[string]mcp.PropDef{
			"text":            {Type: "string", Description: "Text to wait for"},
			"timeout_ms":      {Type: "number", Description: "Max wait in ms, default 30000"},
			"poll_interval_ms": {Type: "number", Description: "Poll interval in ms, default 1000"},
			"region":          {Type: "object", Description: "{x,y,width,height} to restrict OCR region"},
		},
		Required: []string{"text"},
	}, func(ctx any, args map[string]any) (string, error) {
		text, ok := args["text"].(string)
		if !ok || text == "" {
			return "", fmt.Errorf("text is required")
		}
		body := map[string]any{"text": text}
		timeoutMs := 30000.0
		if t, ok := args["timeout_ms"].(float64); ok {
			timeoutMs = t
		}
		body["timeout_ms"] = timeoutMs
		if p, ok := args["poll_interval_ms"].(float64); ok {
			body["poll_interval_ms"] = p
		}
		if region, ok := args["region"]; ok {
			body["region"] = region
		}
		httpTimeout := time.Duration(timeoutMs)*time.Millisecond + 5*time.Second
		data, status, err := vmHTTPWithTimeout("POST", base+"/wait-for-text", body, httpTimeout)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_wait_for_text failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		elapsed, _ := resp["elapsed_ms"].(float64)
		if found {
			x, _ := resp["x"].(float64)
			y, _ := resp["y"].(float64)
			return fmt.Sprintf("Text %q appeared at (%.0f, %.0f) after %.0fms", text, x, y, elapsed), nil
		}
		return fmt.Sprintf("Text %q not found after %.0fms timeout", text, elapsed), nil
	})

	// ── vm_shortcut ───────────────────────────────────────────────────────────

	server.RegisterTool("vm_shortcut", mcp.ToolDef{
		Description: "Send a keyboard shortcut to the emulator (e.g. Cmd+O, Cmd+Q, Cmd+C).",
		Properties: map[string]mcp.PropDef{
			"shortcut": {Type: "string", Description: "Shortcut string like 'Cmd+O', 'Cmd+Shift+S', 'Ctrl+A'"},
		},
		Required: []string{"shortcut"},
	}, func(ctx any, args map[string]any) (string, error) {
		shortcut, ok := args["shortcut"].(string)
		if !ok || shortcut == "" {
			return "", fmt.Errorf("shortcut is required")
		}

		// Parse modifier keys and main key
		modifierMap := map[string]string{
			"cmd":    "MetaLeft",
			"meta":   "MetaLeft",
			"ctrl":   "ControlLeft",
			"control": "ControlLeft",
			"shift":  "ShiftLeft",
			"alt":    "AltLeft",
			"option": "AltLeft",
		}

		parts := strings.Split(shortcut, "+")
		var modifiers []string
		var mainKey string
		for i, p := range parts {
			lower := strings.ToLower(strings.TrimSpace(p))
			if code, isMod := modifierMap[lower]; isMod {
				modifiers = append(modifiers, code)
			} else if i == len(parts)-1 {
				// Last part is the main key — capitalize first letter for key codes
				if len(p) == 1 {
					mainKey = "Key" + strings.ToUpper(p)
				} else {
					mainKey = p
				}
			}
		}
		if mainKey == "" {
			return "", fmt.Errorf("could not parse shortcut: %q", shortcut)
		}

		// Build action sequence: modifiers down, key down+up, modifiers up (reversed)
		var steps []map[string]any
		for _, mod := range modifiers {
			steps = append(steps, map[string]any{"action": "key_press", "code": mod})
		}
		// For modifier+key we need proper down/up sequencing — use actions endpoint with raw events
		// Build as a sequence of key_press actions (simplified: modifier down via key_press won't hold)
		// Instead, use mouse_actions with emulator events directly via evaluate
		_ = steps

		// Build the full action sequence as raw key events via /actions
		var actionSteps []map[string]any
		for _, mod := range modifiers {
			actionSteps = append(actionSteps, map[string]any{"action": "evaluate", "js": fmt.Sprintf(`
				(function(){
					var iframe = document.getElementById('mac-frame');
					if(iframe && iframe.contentWindow) {
						iframe.contentWindow.postMessage({type:'emulator_key_down',code:%q},'*');
					}
				})()
			`, mod)})
		}
		actionSteps = append(actionSteps, map[string]any{"action": "key_press", "code": mainKey})
		for i := len(modifiers) - 1; i >= 0; i-- {
			mod := modifiers[i]
			actionSteps = append(actionSteps, map[string]any{"action": "evaluate", "js": fmt.Sprintf(`
				(function(){
					var iframe = document.getElementById('mac-frame');
					if(iframe && iframe.contentWindow) {
						iframe.contentWindow.postMessage({type:'emulator_key_up',code:%q},'*');
					}
				})()
			`, mod)})
		}

		data, status, err := vmHTTP("POST", base+"/actions", actionSteps)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_shortcut failed (HTTP %d): %s", status, string(data))
		}
		return fmt.Sprintf("Sent shortcut: %s", shortcut), nil
	})

	// ── vm_run_applescript ────────────────────────────────────────────────────

	server.RegisterTool("vm_run_applescript", mcp.ToolDef{
		Description: "Run an AppleScript in Script Editor on the emulated Mac. Script Editor must be installed. The script is typed into Script Editor and executed via Cmd+R. Returns the script result via OCR.",
		Properties: map[string]mcp.PropDef{
			"script":     {Type: "string", Description: "AppleScript source to execute"},
			"timeout_ms": {Type: "number", Description: "Max ms to wait for script completion, default 10000"},
		},
		Required: []string{"script"},
	}, func(ctx any, args map[string]any) (string, error) {
		script, ok := args["script"].(string)
		if !ok || script == "" {
			return "", fmt.Errorf("script is required")
		}
		timeoutMs := 10000.0
		if t, ok := args["timeout_ms"].(float64); ok {
			timeoutMs = t
		}
		body := map[string]any{"script": script, "timeout_ms": timeoutMs}
		httpTimeout := time.Duration(timeoutMs)*time.Millisecond + 20*time.Second
		data, status, err := vmHTTPWithTimeout("POST", base+"/run-applescript", body, httpTimeout)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_run_applescript failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		if ok2, _ := resp["ok"].(bool); ok2 {
			result, _ := resp["result"].(string)
			return fmt.Sprintf("AppleScript result: %s", result), nil
		}
		errMsg, _ := resp["error"].(string)
		return fmt.Sprintf("AppleScript failed: %s", errMsg), nil
	})

	// ── vm_foreground_app ─────────────────────────────────────────────────────

	server.RegisterTool("vm_foreground_app", mcp.ToolDef{
		Description: "Bring a running application to the foreground via the Mac OS 8 Application menu (top-right menubar icon).",
		Properties: map[string]mcp.PropDef{
			"app": {Type: "string", Description: "Application name as it appears in the Application menu"},
		},
		Required: []string{"app"},
	}, func(ctx any, args map[string]any) (string, error) {
		app, ok := args["app"].(string)
		if !ok || app == "" {
			return "", fmt.Errorf("app is required")
		}
		body := map[string]any{"app": app}
		data, status, err := vmHTTPWithTimeout("POST", base+"/foreground-app", body, 15*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_foreground_app failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		if found {
			return fmt.Sprintf("Brought %q to foreground", app), nil
		}
		return fmt.Sprintf("App %q not found in Application menu", app), nil
	})

	// ── vm_select_window ──────────────────────────────────────────────────────

	server.RegisterTool("vm_select_window", mcp.ToolDef{
		Description: "Select a window by name via the Window menu (if name provided), or cycle to the next window with Cmd+` (no name). In Mac OS 8, many apps support Cmd+` for window cycling.",
		Properties: map[string]mcp.PropDef{
			"name": {Type: "string", Description: "Window title to select. Omit to cycle to next window."},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		body := map[string]any{}
		if name, ok := args["name"].(string); ok && name != "" {
			body["name"] = name
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/select-window", body, 15*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_select_window failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		if cycled, ok := resp["cycled"].(bool); ok && cycled {
			return "Cycled to next window (Cmd+`)", nil
		}
		found, _ := resp["found"].(bool)
		name, _ := resp["name"].(string)
		if found {
			return fmt.Sprintf("Selected window %q", name), nil
		}
		return fmt.Sprintf("Window %q not found", name), nil
	})

	// ── vm_list_elements ──────────────────────────────────────────────────────

	server.RegisterTool("vm_list_elements", mcp.ToolDef{
		Description: `List all visible text elements in the emulator via OCR, grouped by word or line. Returns indexed list with coordinates. Use to find the nth element to click.`,
		Properties: map[string]mcp.PropDef{
			"type":   {Type: "string", Description: `"word" or "line" (default "line"). "line" groups nearby words into lines.`},
			"region": {Type: "object", Description: "{x,y,width,height} to restrict OCR region"},
		},
	}, func(ctx any, args map[string]any) (string, error) {
		body := map[string]any{"type": "line"}
		if t, ok := args["type"].(string); ok && t != "" {
			body["type"] = t
		}
		if region, ok := args["region"]; ok {
			body["region"] = region
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/list-elements", body, 45*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_list_elements failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		elements, _ := resp["elements"].([]any)
		if len(elements) == 0 {
			return "No text elements found via OCR", nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Found %d elements:\n", len(elements)))
		for _, e := range elements {
			em, _ := e.(map[string]any)
			idx, _ := em["index"].(float64)
			text, _ := em["text"].(string)
			x, _ := em["x"].(float64)
			y, _ := em["y"].(float64)
			sb.WriteString(fmt.Sprintf("  [%d] %q at (%.0f, %.0f)\n", int(idx)+1, text, x, y))
		}
		return sb.String(), nil
	})

	// ── vm_click_nth_element ──────────────────────────────────────────────────

	server.RegisterTool("vm_click_nth_element", mcp.ToolDef{
		Description: "Click the nth text element detected via OCR. n=1 is the first element. type='line' groups words into lines (default), type='word' treats each OCR word separately.",
		Properties: map[string]mcp.PropDef{
			"n":        {Type: "number", Description: "1-based index of the element to click"},
			"type":     {Type: "string", Description: `"word" or "line" (default "line")`},
			"region":   {Type: "object", Description: "{x,y,width,height} to restrict OCR region"},
			"offset_x": {Type: "number", Description: "X offset from element center, default 0"},
			"offset_y": {Type: "number", Description: "Y offset from element center, default 0"},
		},
		Required: []string{"n"},
	}, func(ctx any, args map[string]any) (string, error) {
		n, ok := args["n"].(float64)
		if !ok || n < 1 {
			return "", fmt.Errorf("n is required and must be >= 1")
		}
		body := map[string]any{"n": n, "type": "line"}
		if t, ok := args["type"].(string); ok && t != "" {
			body["type"] = t
		}
		if region, ok := args["region"]; ok {
			body["region"] = region
		}
		if ox, ok := args["offset_x"].(float64); ok {
			body["offset_x"] = ox
		}
		if oy, ok := args["offset_y"].(float64); ok {
			body["offset_y"] = oy
		}
		data, status, err := vmHTTPWithTimeout("POST", base+"/click-nth-element", body, 45*time.Second)
		if err != nil {
			return "", fmt.Errorf("vm-control-server unreachable: %w", err)
		}
		if status != 200 {
			return "", fmt.Errorf("vm_click_nth_element failed (HTTP %d): %s", status, string(data))
		}
		var resp map[string]any
		if err := json.Unmarshal(data, &resp); err != nil {
			return string(data), nil
		}
		found, _ := resp["found"].(bool)
		if !found {
			return fmt.Sprintf("Element %d not found (out of range)", int(n)), nil
		}
		x, _ := resp["x"].(float64)
		y, _ := resp["y"].(float64)
		text, _ := resp["text"].(string)
		return fmt.Sprintf("Clicked element %d %q at (%.0f, %.0f)", int(n), text, x, y), nil
	})
}
