package executive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// WorkflowStep defines a single step in a workflow definition.
type WorkflowStep struct {
	ID              string `yaml:"id"`
	Agent           string `yaml:"agent"`
	ContextTemplate string `yaml:"context_template"`
}

// WorkflowDef is the parsed representation of a workflow YAML file.
type WorkflowDef struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Steps       []WorkflowStep `yaml:"steps"`
}

// WorkflowInstance tracks the live state of a running workflow.
type WorkflowInstance struct {
	ID                string                     `json:"id"`
	WorkflowName      string                     `json:"workflow_name"`
	WorkflowStep      string                     `json:"workflow_step"`       // current step ID
	WorkflowSessionID string                     `json:"workflow_session_id"` // active subagent session ID
	Input             string                     `json:"input"`
	StartedAt         time.Time                  `json:"started_at"`
	Outputs           map[string]json.RawMessage `json:"outputs"` // step_id -> raw JSON output
}

// LoadWorkflowDef reads state/system/workflows/{name}.yaml from the given state dir.
func LoadWorkflowDef(statePath, name string) (*WorkflowDef, error) {
	p := filepath.Join(statePath, "system", "workflows", name+".yaml")
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read workflow %q: %w", name, err)
	}
	var def WorkflowDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parse workflow %q: %w", name, err)
	}
	return &def, nil
}

// workflowInstancePath returns the path for a workflow instance JSON file.
func workflowInstancePath(statePath, id string) string {
	return filepath.Join(statePath, "system", "workflow-instances", id+".json")
}

// LoadWorkflowInstance reads state/system/workflow-instances/{id}.json.
func LoadWorkflowInstance(statePath, id string) (*WorkflowInstance, error) {
	p := workflowInstancePath(statePath, id)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read workflow instance %q: %w", id, err)
	}
	var inst WorkflowInstance
	if err := json.Unmarshal(data, &inst); err != nil {
		return nil, fmt.Errorf("parse workflow instance %q: %w", id, err)
	}
	return &inst, nil
}

// SaveWorkflowInstance atomically writes the instance to disk.
func SaveWorkflowInstance(statePath string, inst *WorkflowInstance) error {
	dir := filepath.Join(statePath, "system", "workflow-instances")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create workflow-instances dir: %w", err)
	}
	data, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal workflow instance: %w", err)
	}
	// Atomic write via temp file + rename.
	tmp := workflowInstancePath(statePath, inst.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write workflow instance tmp: %w", err)
	}
	if err := os.Rename(tmp, workflowInstancePath(statePath, inst.ID)); err != nil {
		return fmt.Errorf("rename workflow instance: %w", err)
	}
	return nil
}

// ArchiveWorkflowInstance moves the instance file to the archive directory as {id}.done.json.
func ArchiveWorkflowInstance(statePath, id string) error {
	src := workflowInstancePath(statePath, id)
	archiveDir := filepath.Join(statePath, "system", "workflow-instances", "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	dst := filepath.Join(archiveDir, id+".done.json")
	return os.Rename(src, dst)
}

// RenderContextTemplate renders a Go text/template with workflow instance data.
// The template receives:
//   - .input   — the workflow's original input string
//   - .outputs — map[string]any of parsed step outputs (keyed by step ID)
//
// A custom "jsonPath" template func allows dot-separated key navigation into outputs.
func RenderContextTemplate(tmpl string, inst *WorkflowInstance) (string, error) {
	// Parse step outputs into map[string]any for use in templates.
	parsedOutputs := make(map[string]any, len(inst.Outputs))
	for stepID, raw := range inst.Outputs {
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			parsedOutputs[stepID] = v
		}
	}

	funcMap := template.FuncMap{
		"jsonPath": jsonPathFunc,
	}

	t, err := template.New("ctx").Funcs(funcMap).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("parse context template: %w", err)
	}

	data := map[string]any{
		"input":   inst.Input,
		"outputs": parsedOutputs,
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("execute context template: %w", err)
	}
	return buf.String(), nil
}

// jsonPathFunc navigates a dot-separated path into a parsed JSON value.
// E.g. jsonPathFunc("direction.title", obj) returns obj["direction"]["title"].
func jsonPathFunc(path string, v any) (string, error) {
	parts := strings.Split(path, ".")
	cur := v
	for _, part := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return "", fmt.Errorf("jsonPath: expected object at %q, got %T", part, cur)
		}
		cur, ok = m[part]
		if !ok {
			return "", fmt.Errorf("jsonPath: key %q not found", part)
		}
	}
	switch s := cur.(type) {
	case string:
		return s, nil
	case nil:
		return "", nil
	default:
		b, err := json.Marshal(s)
		if err != nil {
			return fmt.Sprintf("%v", s), nil
		}
		return string(b), nil
	}
}

// NextStep returns the next step in the workflow sequence after currentStepID.
// If action == "escalate", it returns the previous step instead.
// Returns nil if the workflow is complete (no next/prev step).
func NextStep(def *WorkflowDef, currentStepID string, action string) (*WorkflowStep, error) {
	idx := -1
	for i, s := range def.Steps {
		if s.ID == currentStepID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("step %q not found in workflow %q", currentStepID, def.Name)
	}

	if action == "escalate" {
		if idx == 0 {
			return nil, nil // already at first step, nowhere to escalate
		}
		prev := def.Steps[idx-1]
		return &prev, nil
	}

	// advance (done / continue / "")
	if idx+1 >= len(def.Steps) {
		return nil, nil // workflow complete
	}
	next := def.Steps[idx+1]
	return &next, nil
}
