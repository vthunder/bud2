package memory

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"

	"github.com/vthunder/bud2/internal/types"
)

// Outbox tails an append-only JSONL file written by external processes (e.g. bud-mcp).
// It tracks a file offset and returns new actions on each Poll call.
// No status tracking — once read, actions are handed off to the caller.
type Outbox struct {
	mu         sync.Mutex
	path       string
	lastOffset int64
}

// NewOutbox creates a new outbox tailer for the given file path.
func NewOutbox(path string) *Outbox {
	return &Outbox{path: path}
}

// Init seeks to the end of the file so that only entries appended after
// startup are returned by Poll. Call this once at startup.
func (o *Outbox) Init() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	file, err := os.Open(o.path)
	if os.IsNotExist(err) {
		o.lastOffset = 0
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	o.lastOffset, err = file.Seek(0, io.SeekEnd)
	return err
}

// Poll reads new entries appended to the file since the last call.
// Returns the parsed actions. The caller is responsible for processing them.
func (o *Outbox) Poll() ([]*types.Action, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	file, err := os.Open(o.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	if o.lastOffset > 0 {
		if _, err = file.Seek(o.lastOffset, io.SeekStart); err != nil {
			return nil, err
		}
	}

	scanner := bufio.NewScanner(file)
	var actions []*types.Action

	for scanner.Scan() {
		var action types.Action
		if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
			continue // skip malformed lines
		}
		actions = append(actions, &action)
	}

	// Update offset — use the scanner's consumed position.
	// bufio.Scanner may read ahead, so compute offset from bytes actually scanned.
	// Since we're reading line-by-line from a known start, track bytes consumed.
	newOffset, _ := file.Seek(0, io.SeekCurrent)
	o.lastOffset = newOffset

	return actions, scanner.Err()
}

// Append writes an action to the file (for in-process callers like reflexes).
func (o *Outbox) Append(action *types.Action) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	file, err := os.OpenFile(o.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	data, err := json.Marshal(action)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if _, err := file.WriteString("\n"); err != nil {
		return err
	}
	return file.Sync()
}
