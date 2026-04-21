package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/vthunder/bud2/internal/extensions"
	"github.com/vthunder/bud2/internal/reflex"
)

// extWorkflowRunner adapts *reflex.Engine + *extensions.Registry to the WorkflowRunner
// interface expected by extensions.Dispatcher.
type extWorkflowRunner struct {
	engine   *reflex.Engine
	registry *extensions.Registry
}

func (r *extWorkflowRunner) RunWorkflow(ctx context.Context, name string, params map[string]any) (any, error) {
	cap, ext, ok := r.registry.GetCapabilityByFullName(name)
	if !ok {
		return nil, fmt.Errorf("workflow %q not found in extension registry", name)
	}
	if cap.Type != "workflow" {
		return nil, fmt.Errorf("capability %q has type %q, not \"workflow\"", name, cap.Type)
	}

	idx := strings.LastIndex(name, ":")
	capName := name[idx+1:]
	yamlPath := filepath.Join(ext.Dir, "capabilities", capName+".yaml")

	rx := r.engine.Get(capName)
	if rx == nil {
		loaded, err := r.engine.LoadFile(yamlPath)
		if err != nil {
			return nil, fmt.Errorf("workflow %q: failed to load from %s: %w", name, yamlPath, err)
		}
		rx = loaded
	}

	extracted := make(map[string]string, len(params))
	for k, v := range params {
		extracted[k] = fmt.Sprintf("%v", v)
	}
	result, err := r.engine.Execute(ctx, rx, extracted, params)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	return result.Output, nil
}

// dispatcherTalker implements extensions.TalkToUser using a send function captured from main.
type dispatcherTalker struct {
	send func(msg string) error
}

func (t *dispatcherTalker) Notify(message string) error {
	if t.send == nil {
		return nil
	}
	return t.send(message)
}

// dispatcherLogger implements extensions.SaveThought using a log function captured from main.
type dispatcherLogger struct {
	log func(msg string)
}

func (l *dispatcherLogger) Log(message string) error {
	if l.log != nil {
		l.log(message)
	}
	return nil
}
