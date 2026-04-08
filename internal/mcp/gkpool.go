package mcp

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type gkEntry struct {
	client   *ProxyClient
	lastUsed time.Time
}

// GKPool manages a pool of GK MCP server processes, one per db path.
// Processes are started on demand and killed after 5 minutes of idle.
type GKPool struct {
	mu       sync.Mutex
	entries  map[string]*gkEntry // keyed by db path
	gkPath   string              // path to GK source directory (passed to bun run)
	stateDir string              // bud2 state directory (for resolving domain paths)
}

// NewGKPool creates a new pool. gkPath is the directory of the GK project
// (passed as `bun run <gkPath>`). stateDir is the bud2 state directory.
func NewGKPool(gkPath, stateDir string) *GKPool {
	p := &GKPool{
		entries:  make(map[string]*gkEntry),
		gkPath:   gkPath,
		stateDir: stateDir,
	}
	go p.cleanupLoop()
	return p
}

// DomainToDBPath converts a domain path (e.g. "/" or "/projects/foo") to a
// concrete GK db file path within the state directory.
// "/" → <stateDir>/gk.db
// "/projects/foo" → <stateDir>/projects/foo/gk.db
func (p *GKPool) DomainToDBPath(domain string) string {
	if domain == "" || domain == "/" {
		return filepath.Join(p.stateDir, "system", "gk.db")
	}
	// Clean the path to prevent traversal attacks
	cleaned := filepath.Clean(domain)
	return filepath.Join(p.stateDir, cleaned, "gk.db")
}

// CallTool routes a GK tool call to the appropriate GK process for the given domain.
func (p *GKPool) CallTool(domain, toolName string, args map[string]any) (string, error) {
	dbPath := p.DomainToDBPath(domain)
	client, err := p.getOrStart(dbPath)
	if err != nil {
		return "", fmt.Errorf("gk(%s): %w", domain, err)
	}
	result, err := client.CallTool(toolName, args)
	if err != nil {
		p.evict(dbPath)
	}
	return result, err
}

// ListResources lists all MCP resources available from the GK process for the given domain.
func (p *GKPool) ListResources(domain string) ([]ResourceInfo, error) {
	dbPath := p.DomainToDBPath(domain)
	client, err := p.getOrStart(dbPath)
	if err != nil {
		return nil, fmt.Errorf("gk(%s): %w", domain, err)
	}
	result, err := client.ListResources()
	if err != nil {
		p.evict(dbPath)
	}
	return result, err
}

// ReadResource reads an MCP resource by URI from the GK process for the given domain.
func (p *GKPool) ReadResource(domain, uri string) (string, error) {
	dbPath := p.DomainToDBPath(domain)
	client, err := p.getOrStart(dbPath)
	if err != nil {
		return "", fmt.Errorf("gk(%s): %w", domain, err)
	}
	result, err := client.ReadResource(uri)
	if err != nil {
		p.evict(dbPath)
	}
	return result, err
}

// evict removes a stale entry from the pool so the next call restarts the process.
func (p *GKPool) evict(dbPath string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[dbPath]; ok {
		e.client.Close()
		delete(p.entries, dbPath)
		log.Printf("[gkpool] Evicted stale GK process for %s", dbPath)
	}
}

// getOrStart returns the ProxyClient for the given dbPath, starting a new GK
// process if needed.
func (p *GKPool) getOrStart(dbPath string) (*ProxyClient, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if e, ok := p.entries[dbPath]; ok {
		e.lastUsed = time.Now()
		return e.client, nil
	}

	// Ensure the db directory exists so GK can create the db file.
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create gk db dir %s: %w", dir, err)
	}

	client, err := StartProxy(ExternalServerConfig{
		Name:    fmt.Sprintf("gk:%s", dbPath),
		Command: "bun",
		Args:    []string{"run", p.gkPath},
		Env:     map[string]string{"GK_DB_PATH": dbPath},
	})
	if err != nil {
		return nil, fmt.Errorf("start gk process for %s: %w", dbPath, err)
	}

	p.entries[dbPath] = &gkEntry{client: client, lastUsed: time.Now()}
	log.Printf("[gkpool] Started GK process for %s", dbPath)
	return client, nil
}

// cleanupLoop periodically kills GK processes that have been idle for >5 minutes.
func (p *GKPool) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		p.cleanup()
	}
}

func (p *GKPool) cleanup() {
	p.mu.Lock()
	defer p.mu.Unlock()

	cutoff := time.Now().Add(-5 * time.Minute)
	for dbPath, e := range p.entries {
		if e.lastUsed.Before(cutoff) {
			log.Printf("[gkpool] Killing idle GK process for %s", dbPath)
			e.client.Close()
			delete(p.entries, dbPath)
		}
	}
}

// Close shuts down all running GK processes.
func (p *GKPool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for dbPath, e := range p.entries {
		log.Printf("[gkpool] Closing GK process for %s", dbPath)
		e.client.Close()
		delete(p.entries, dbPath)
	}
}
