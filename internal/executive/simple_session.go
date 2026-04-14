package executive

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	claudecode "github.com/severity1/claude-agent-sdk-go"
	"github.com/vthunder/bud2/internal/executive/provider"
	"github.com/vthunder/bud2/internal/logging"
	"gopkg.in/yaml.v3"
)

// Compile-time check that SimpleSession satisfies provider.Session
var _ provider.Session = (*SimpleSession)(nil)

// SendPrompt implements the provider.Session interface. It delegates to
// SendPromptWithCfg using the session's stored config and adapts StreamCallbacks.
func (s *SimpleSession) SendPrompt(ctx context.Context, prompt string, cb provider.StreamCallbacks) (*provider.SessionResult, error) {
	// Wire StreamCallbacks into the existing OnOutput/OnToolCall hooks
	if cb.OnText != nil {
		prevOnOutput := s.onOutput
		s.onOutput = func(text string) {
			if prevOnOutput != nil {
				prevOnOutput(text)
			}
			cb.OnText(text)
		}
		defer func() { s.onOutput = prevOnOutput }()
	}
	if cb.OnTool != nil {
		prevOnToolCall := s.onToolCall
		s.onToolCall = func(name string, args map[string]any) (string, error) {
			cb.OnTool(name, args)
			if prevOnToolCall != nil {
				return prevOnToolCall(name, args)
			}
			return "", nil
		}
		defer func() { s.onToolCall = prevOnToolCall }()
	}

	cfg := s.lastCfg
	err := s.SendPromptWithCfg(ctx, prompt, cfg)
	if err != nil {
		if errors.Is(err, ErrInterrupted) {
			return nil, provider.ErrInterrupted
		}
		return nil, err
	}

	result := &provider.SessionResult{
		SessionID: s.claudeSessionID,
	}
	if s.lastUsage != nil {
		result.Usage = s.lastUsage
		if cb.OnResult != nil {
			cb.OnResult(s.lastUsage)
		}
	}
	return result, nil
}

// higher-priority item (e.g. a P1 user message arriving during a background wake).
var ErrInterrupted = errors.New("session interrupted by higher-priority item")

// scanLocalPlugins returns the absolute path of each plugin directory under
// state/system/plugins/ that contains a .claude-plugin/plugin.json file.
// These are passed to Claude Code via --plugin-dir so skills are loaded without
// needing manual symlinks in ~/.claude/plugins/marketplaces/.
func scanLocalPlugins(statePath string) []string {
	pluginsDir := filepath.Join(statePath, "system", "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pluginJSON := filepath.Join(pluginsDir, e.Name(), ".claude-plugin", "plugin.json")
		if _, err := os.Stat(pluginJSON); err == nil {
			paths = append(paths, filepath.Join(pluginsDir, e.Name()))
		}
	}
	return paths
}

// pluginManifest is the structure of state/system/extensions.yaml
// (formerly plugins.yaml — see extensionsManifestPath for the fallback logic).
type pluginManifest struct {
	Plugins []pluginManifestEntry `yaml:"plugins"`
	Skills  []skillManifestEntry  `yaml:"skills"`
}

// pluginManifestEntry is a single entry in the plugins: section of extensions.yaml.
// Supports both string form ("owner/repo[:dir][@ref]") and object form
// (with repo/dir/ref/path/tool_grants fields).
type pluginManifestEntry struct {
	// Populated for remote repo entries
	owner string
	repo  string
	dir   string // subdirectory within repo, empty = root
	ref   string // branch/tag/commit, empty = default branch
	// Populated for local path entries
	localPath string
	// Tool grants: pattern -> list of tools (may include wildcards like mcp__bud2__gk_*)
	ToolGrants map[string][]string
	// Exclude: sub-plugin directory names to skip (e.g. "issues-linear")
	Exclude []string
}

// UnmarshalYAML handles both string and object form for plugin entries.
//
// String form supports explicit prefixes:
//
//	git:owner/repo[:dir][@ref]   — GitHub repo (preferred)
//	path:/local/path             — local filesystem path
//	owner/repo[:dir][@ref]       — legacy bare form (logs deprecation warning)
//
// clawhub: entries in the plugins: section log a "not yet supported" warning and
// are skipped (reserved for future use).
func (e *pluginManifestEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return e.parsePluginString(value.Value)
	}
	// Object form
	var raw struct {
		Repo       string              `yaml:"repo"`
		Dir        string              `yaml:"dir"`
		Ref        string              `yaml:"ref"`
		Path       string              `yaml:"path"`
		ToolGrants map[string][]string `yaml:"tool_grants"`
		Exclude    []string            `yaml:"exclude"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if raw.Repo != "" {
		if err := e.parseRepoString(raw.Repo); err != nil {
			return err
		}
		if raw.Dir != "" {
			e.dir = raw.Dir
		}
		if raw.Ref != "" {
			e.ref = raw.Ref
		}
	}
	e.localPath = raw.Path
	e.ToolGrants = raw.ToolGrants
	e.Exclude = raw.Exclude
	return nil
}

// parsePluginString dispatches a string-form plugin entry based on its prefix.
func (e *pluginManifestEntry) parsePluginString(s string) error {
	switch {
	case strings.HasPrefix(s, "git:"):
		return e.parseRepoString(strings.TrimPrefix(s, "git:"))
	case strings.HasPrefix(s, "path:"):
		e.localPath = strings.TrimPrefix(s, "path:")
		return nil
	case strings.HasPrefix(s, "clawhub:"):
		log.Printf("[plugins] WARNING: clawhub: entries are not yet supported in the plugins: section (skipping %q)", s)
		return nil
	default:
		// Legacy bare "owner/repo" form — still functional, but deprecated.
		log.Printf("[plugins] DEPRECATED: bare plugin entry %q; use git:%s", s, s)
		return e.parseRepoString(s)
	}
}

// parseRepoString parses "owner/repo[:dir][@ref]" into the entry's fields.
func (e *pluginManifestEntry) parseRepoString(s string) error {
	// Split ref first (last @)
	if idx := strings.LastIndex(s, "@"); idx != -1 {
		e.ref = s[idx+1:]
		s = s[:idx]
	}
	// Split subdir (first :)
	if idx := strings.Index(s, ":"); idx != -1 {
		e.dir = s[idx+1:]
		s = s[:idx]
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid plugin entry %q: expected owner/repo", s)
	}
	e.owner = parts[0]
	e.repo = parts[1]
	return nil
}

// pluginDir associates a local filesystem path with any tool grants from its
// manifest entry. Used when loading agent definitions.
type pluginDir struct {
	Path       string
	ToolGrants map[string][]string // nil if no grants
}

// looksLikePluginDir returns true if the path appears to be a plugin directory
// (has .claude-plugin/plugin.json, an agents/ subdir, or .md files).
func looksLikePluginDir(dirPath string) bool {
	if _, err := os.Stat(filepath.Join(dirPath, ".claude-plugin", "plugin.json")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dirPath, "agents")); err == nil {
		return true
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return false
	}
	for _, f := range entries {
		if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
			return true
		}
	}
	return false
}

// resolvePluginPathsFromLocalPath resolves a cache path into one or more plugin
// directory paths. If the path itself looks like a plugin dir, it's returned
// as-is. Otherwise its immediate subdirectories that look like plugin dirs are
// returned (one-level-deep expansion for monorepo-style repos).
func resolvePluginPathsFromLocalPath(localPath string) []string {
	if _, err := os.Stat(filepath.Join(localPath, ".claude-plugin", "plugin.json")); err == nil {
		return []string{localPath}
	}
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return nil
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := filepath.Join(localPath, e.Name())
		if looksLikePluginDir(sub) {
			paths = append(paths, sub)
		}
	}
	return paths
}

// extensionsManifestPath returns the path to the extensions manifest file.
// Checks for extensions.yaml first; falls back to the legacy plugins.yaml with
// a one-time deprecation log if only the old file exists.
func extensionsManifestPath(statePath string) string {
	newPath := filepath.Join(statePath, "system", "extensions.yaml")
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	oldPath := filepath.Join(statePath, "system", "plugins.yaml")
	if _, err := os.Stat(oldPath); err == nil {
		log.Printf("[extensions] DEPRECATED: plugins.yaml found; rename to extensions.yaml")
		return oldPath
	}
	return newPath // caller handles not-exist gracefully
}

// loadManifestPlugins reads the plugins: section of extensions.yaml (or the
// legacy plugins.yaml) and ensures each listed repo is cloned under
// ~/.cache/bud/plugins/. Returns the resolved local paths for --plugin-dir.
// Errors are logged and the failing entry is skipped — startup continues.
func loadManifestPlugins(statePath string) []string {
	manifestPath := extensionsManifestPath(statePath)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[plugins] failed to read extensions manifest: %v", err)
		}
		return nil
	}

	var manifest pluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		log.Printf("[plugins] failed to parse extensions manifest: %v", err)
		return nil
	}

	cacheBase, err := os.UserCacheDir()
	if err != nil {
		log.Printf("[plugins] failed to resolve user cache dir: %v", err)
		return nil
	}
	pluginCacheDir := filepath.Join(cacheBase, "bud", "plugins")

	var paths []string
	for _, e := range manifest.Plugins {
		if e.owner == "" {
			// Local path entry — no git ops needed.
			if e.localPath != "" {
				paths = append(paths, resolvePluginPathsFromLocalPath(e.localPath)...)
			}
			continue
		}

		repoDir := filepath.Join(pluginCacheDir, e.owner, e.repo)

		if _, err := os.Stat(repoDir); os.IsNotExist(err) {
			// Clone the repo.
			cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", e.owner, e.repo)
			args := []string{"clone", "--depth=1"}
			if e.ref != "" {
				args = append(args, "--branch", e.ref)
			}
			args = append(args, cloneURL, repoDir)
			cmd := exec.Command("git", args...)
			if out, err := cmd.CombinedOutput(); err != nil {
				log.Printf("[plugins] failed to clone %s: %v\n%s", cloneURL, err, out)
				continue
			}
			log.Printf("[plugins] cloned %s/%s", e.owner, e.repo)
		} else {
			// Repo already exists — update it.
			if e.ref != "" {
				// Pinned ref: fetch then checkout.
				fetch := exec.Command("git", "-C", repoDir, "fetch", "--depth=1", "origin", e.ref)
				if out, err := fetch.CombinedOutput(); err != nil {
					log.Printf("[plugins] failed to fetch %s/%s@%s: %v\n%s", e.owner, e.repo, e.ref, err, out)
					// Non-fatal: use whatever is already checked out.
				} else {
					checkout := exec.Command("git", "-C", repoDir, "checkout", e.ref)
					if out, err := checkout.CombinedOutput(); err != nil {
						log.Printf("[plugins] failed to checkout %s/%s@%s: %v\n%s", e.owner, e.repo, e.ref, err, out)
					}
				}
			} else {
				// Floating: fast-forward pull.
				pull := exec.Command("git", "-C", repoDir, "pull", "--ff-only")
				if out, err := pull.CombinedOutput(); err != nil {
					log.Printf("[plugins] failed to pull %s/%s: %v\n%s", e.owner, e.repo, err, out)
					// Non-fatal: keep using the existing checkout.
				}
			}
		}

		localPath := repoDir
		if e.dir != "" {
			localPath = filepath.Join(repoDir, e.dir)
		}
		if _, err := os.Stat(localPath); err != nil {
			log.Printf("[plugins] skipping %s/%s: resolved path not found: %s", e.owner, e.repo, localPath)
			continue
		}

		excludeSet := make(map[string]bool, len(e.Exclude))
		for _, ex := range e.Exclude {
			excludeSet[ex] = true
		}
		for _, p := range resolvePluginPathsFromLocalPath(localPath) {
			if excludeSet[filepath.Base(p)] {
				logging.Debug("plugins", "skipping excluded plugin: %s", filepath.Base(p))
				continue
			}
			paths = append(paths, p)
		}
	}
	return paths
}

// resolvedManifestPluginDirs reads the extensions manifest and returns the
// already-cloned plugin directories with their associated tool grants. No git
// operations — only returns dirs that already exist on disk. Expands monorepo
// entries one level deep (see resolvePluginPathsFromLocalPath).
func resolvedManifestPluginDirs(statePath string) []pluginDir {
	manifestPath := extensionsManifestPath(statePath)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil
	}

	var manifest pluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil
	}

	cacheBase, err := os.UserCacheDir()
	if err != nil {
		return nil
	}
	pluginCacheDir := filepath.Join(cacheBase, "bud", "plugins")

	var dirs []pluginDir
	for _, e := range manifest.Plugins {
		var localPath string
		if e.owner != "" {
			repoDir := filepath.Join(pluginCacheDir, e.owner, e.repo)
			localPath = repoDir
			if e.dir != "" {
				localPath = filepath.Join(repoDir, e.dir)
			}
		} else {
			localPath = e.localPath
		}
		if localPath == "" {
			continue
		}
		if _, err := os.Stat(localPath); err != nil {
			continue
		}
		excludeSet := make(map[string]bool, len(e.Exclude))
		for _, ex := range e.Exclude {
			excludeSet[ex] = true
		}
		for _, p := range resolvePluginPathsFromLocalPath(localPath) {
			if excludeSet[filepath.Base(p)] {
				logging.Debug("plugins", "skipping excluded plugin: %s", filepath.Base(p))
				continue
			}
			dirs = append(dirs, pluginDir{Path: p, ToolGrants: e.ToolGrants})
		}
	}
	return dirs
}

// resolvedManifestPluginPaths returns the paths from resolvedManifestPluginDirs
// as a flat string slice (for backward-compat use in skill loading).
func resolvedManifestPluginPaths(statePath string) []string {
	dirs := resolvedManifestPluginDirs(statePath)
	paths := make([]string, len(dirs))
	for i, d := range dirs {
		paths[i] = d.Path
	}
	return paths
}

// allPluginDirs returns all directories searched for skill content:
//   - local plugins (state/system/plugins/)
//   - manifest git-plugins (~/Library/Caches/bud/plugins/)
//   - manifest git-skills (same cache, different subpaths)
//   - manifest local-path skills
//   - ClaWHub skills virtual dir (~/Library/Caches/bud/skills-clawhub)
//   - ClaWHub per-slug dirs (~/Library/Caches/bud/skills-clawhub/skills/{slug}/)
//
// Used to pass --plugin-dir to subagents and to search for skill content.
func allPluginDirs(statePath string) []string {
	dirs := append(scanLocalPlugins(statePath), resolvedManifestPluginPaths(statePath)...)
	dirs = append(dirs, resolvedManifestSkillDirs(statePath)...)
	if cacheBase, err := os.UserCacheDir(); err == nil {
		chDir := clawhubSkillsDir(cacheBase)
		skillsDir := filepath.Join(chDir, "skills")
		if _, err := os.Stat(skillsDir); err == nil {
			dirs = append(dirs, chDir)
			// Enumerate per-slug subdirs so hooks/bud/ inside each skill is discovered.
			if slugEntries, err := os.ReadDir(skillsDir); err == nil {
				for _, e := range slugEntries {
					if e.IsDir() {
						dirs = append(dirs, filepath.Join(skillsDir, e.Name()))
					}
				}
			}
		}
	}
	return dirs
}

// allPluginDirsForAgents returns all plugin directories with their associated
// tool grants. Local plugins and skill dirs have no grants; manifest plugins
// carry their grants from plugins.yaml tool_grants entries.
func allPluginDirsForAgents(statePath string) []pluginDir {
	var dirs []pluginDir
	for _, p := range scanLocalPlugins(statePath) {
		dirs = append(dirs, pluginDir{Path: p})
	}
	dirs = append(dirs, resolvedManifestPluginDirs(statePath)...)
	for _, p := range resolvedManifestSkillDirs(statePath) {
		dirs = append(dirs, pluginDir{Path: p})
	}
	if cacheBase, err := os.UserCacheDir(); err == nil {
		chDir := clawhubSkillsDir(cacheBase)
		skillsDir := filepath.Join(chDir, "skills")
		if _, err := os.Stat(skillsDir); err == nil {
			dirs = append(dirs, pluginDir{Path: chDir})
			// Enumerate per-slug subdirs so hooks/bud/ inside each skill is discovered.
			if slugEntries, err := os.ReadDir(skillsDir); err == nil {
				for _, e := range slugEntries {
					if e.IsDir() {
						dirs = append(dirs, pluginDir{Path: filepath.Join(skillsDir, e.Name())})
					}
				}
			}
		}
	}
	return dirs
}

// matchesAgentPattern reports whether pattern matches agentKey (format "namespace:agent").
// Supported patterns:
//   - "*"             — matches any agent
//   - "ns:*"          — matches any agent in namespace ns
//   - "ns-*:*"        — matches any agent whose namespace starts with "ns-"
//   - exact string    — literal match
//   - path.Match glob — general glob
func matchesAgentPattern(pattern, agentKey string) bool {
	if pattern == "*" {
		return true
	}
	if ok, _ := path.Match(pattern, agentKey); ok {
		return true
	}
	return false
}

// expandToolGrants expands a list of tool name patterns (which may include glob
// wildcards like "mcp__bud2__gk_*") against knownTools and returns the deduplicated
// set of matching tool names.
func expandToolGrants(grants []string, knownTools []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, g := range grants {
		if !strings.Contains(g, "*") {
			if !seen[g] {
				seen[g] = true
				result = append(result, g)
			}
			continue
		}
		// Wildcard: match against knownTools
		for _, kt := range knownTools {
			if ok, _ := path.Match(g, kt); ok {
				if !seen[kt] {
					seen[kt] = true
					result = append(result, kt)
				}
			}
		}
	}
	return result
}

// zettelLibrary describes a single zettel library for zettel-libraries.yaml.
type zettelLibrary struct {
	Name     string `yaml:"name"`
	Path     string `yaml:"path"`
	Default  bool   `yaml:"default,omitempty"`
	Readonly bool   `yaml:"readonly,omitempty"`
	Source   string `yaml:"source,omitempty"`
}

// zettelLibrariesFile is the top-level struct for zettel-libraries.yaml.
type zettelLibrariesFile struct {
	Libraries []zettelLibrary `yaml:"libraries"`
}

// generateZettelLibraries scans all plugin dirs for plugin.json files that
// declare a "zettels" path and writes state/system/zettel-libraries.yaml.
// Plugins loaded from the OS cache directory are always marked readonly.
// Manual entries (those without a source field) are preserved across restarts.
func generateZettelLibraries(statePath string) {
	dest := filepath.Join(statePath, "system", "zettel-libraries.yaml")

	// Collect manual entries from the existing file (entries with no source field).
	var manualEntries []zettelLibrary
	if existing, err := os.ReadFile(dest); err == nil {
		var existingFile zettelLibrariesFile
		if err := yaml.Unmarshal(existing, &existingFile); err == nil {
			for _, lib := range existingFile.Libraries {
				if lib.Source == "" && lib.Name != "home" {
					manualEntries = append(manualEntries, lib)
				}
			}
		}
	}

	// Start with the hardcoded home entry, then preserved manual entries.
	libraries := []zettelLibrary{
		{Name: "home", Path: filepath.Join(statePath, "zettels"), Default: true},
	}
	libraries = append(libraries, manualEntries...)

	cacheBase, _ := os.UserCacheDir()
	pluginCacheDir := filepath.Join(cacheBase, "bud", "plugins")

	for _, dir := range allPluginDirs(statePath) {
		manifestPath := filepath.Join(dir, ".claude-plugin", "plugin.json")
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // no plugin.json, skip
		}
		var manifest struct {
			Name     string `json:"name"`
			Zettels  string `json:"zettels"`
			Readonly bool   `json:"readonly"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		if manifest.Zettels == "" {
			continue
		}
		name := manifest.Name
		if name == "" {
			name = filepath.Base(dir)
		}
		// Cache checkouts are always readonly — never commit from a cached clone.
		isCache := strings.HasPrefix(dir, pluginCacheDir)
		zettelsPath := filepath.Join(dir, manifest.Zettels)
		libraries = append(libraries, zettelLibrary{
			Name:     name,
			Path:     zettelsPath,
			Readonly: manifest.Readonly || isCache,
			Source:   "plugin:" + name,
		})
	}

	out, err := yaml.Marshal(zettelLibrariesFile{Libraries: libraries})
	if err != nil {
		log.Printf("[plugins] failed to marshal zettel-libraries.yaml: %v", err)
		return
	}
	header := "# Managed by plugin loader. Entries without 'source' field are preserved across restarts.\n"
	if err := os.WriteFile(dest, append([]byte(header), out...), 0644); err != nil {
		log.Printf("[plugins] failed to write zettel-libraries.yaml: %v", err)
		return
	}
	log.Printf("[plugins] wrote zettel-libraries.yaml with %d libraries (%d manual)", len(libraries), len(manualEntries))
}

// MaxContextTokens is the threshold for context tokens before auto-reset.
// Uses cache_read_input_tokens from the API which tells us how much session
// history is being read from cache. With a 200K context window, we reset
// at 150K to leave headroom for the current prompt + response.
const MaxContextTokens = provider.MaxContextTokensDefault

// SimpleSession manages a single persistent Claude session via the SDK
type SimpleSession struct {
	mu sync.Mutex

	sessionID        string
	sessionStartTime time.Time // When this session started (for guardrails)
	statePath        string    // Path to state directory for reset coordination

	// Track what's been sent to this session
	seenMemoryIDs map[string]bool   // Track which memory traces have been sent
	memoryIDMap   map[string]string // Map trace_id -> short hash display ID (tr_xxxxx)

	// Usage from last completed prompt
	lastUsage *SessionUsage

	// Last ClaudeConfig used, stored for provider.Session.SendPrompt
	lastCfg ClaudeConfig

	// Claude-assigned session ID (from result event) — use this for --resume
	claudeSessionID string

	// Track if we've received text output for current prompt (to avoid duplicates)
	currentPromptHasText bool

	// isResuming is set by PrepareForResume to tell buildPrompt to skip static context
	// (core identity, conversation buffer) that's already in the Claude session history.
	isResuming bool

	// sessionLogPath is the file where conversation events are streamed for observability.
	// Set via SetSessionLog before each SendPrompt call.
	sessionLogPath string

	// Callbacks
	onToolCall func(name string, args map[string]any) (string, error)
	onOutput   func(text string)

	// Cached plugin scan results (computed once, reused per prompt)
	localPlugins    []string
	manifestPlugins []string
	pluginsComputed bool

	// How often floating ClaWHub skills are re-fetched (0 = auto-update disabled).
	// Set by NewExecutiveV2 from BudConfig.Extensions.ParsedUpdateInterval().
	extensionsUpdateInterval time.Duration
}

// NewSimpleSession creates a new simple session manager
func NewSimpleSession(statePath string) *SimpleSession {
	return &SimpleSession{
		sessionID:        generateSessionUUID(),
		sessionStartTime: time.Now(),
		seenMemoryIDs:    make(map[string]bool),
		memoryIDMap:      make(map[string]string),
		statePath:        statePath,
	}
}

// cachedPlugins returns the local and manifest plugin paths, computing them once
// and caching the result for subsequent calls.
func (s *SimpleSession) cachedPlugins() (localPlugins, manifestPlugins []string) {
	if s.pluginsComputed {
		return s.localPlugins, s.manifestPlugins
	}
	s.localPlugins = scanLocalPlugins(s.statePath)
	s.manifestPlugins = loadManifestPlugins(s.statePath)
	loadManifestSkills(s.statePath, s.extensionsUpdateInterval)
	s.pluginsComputed = true
	return s.localPlugins, s.manifestPlugins
}

// generateSessionUUID creates a random UUID v4
func generateSessionUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%12x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// resetPendingPath returns the path to the reset pending flag file
func (s *SimpleSession) resetPendingPath() string {
	if s.statePath == "" {
		return ""
	}
	return s.statePath + "/reset.pending"
}

// execSessionPath returns the path to the persistent executive session file.
func (s *SimpleSession) execSessionPath() string {
	if s.statePath == "" {
		return ""
	}
	return filepath.Join(s.statePath, "system", "executive-session.json")
}

// execSessionDiskFormat is the on-disk representation of the executive session.
type execSessionDiskFormat struct {
	ClaudeSessionID string    `json:"claude_session_id"`
	SavedAt         time.Time `json:"saved_at"`
	CacheReadTokens int       `json:"cache_read_tokens"`
}

// SaveSessionToDisk writes the current claudeSessionID to disk so it survives
// Bud restarts. Callers must not hold s.mu when calling this.
func (s *SimpleSession) SaveSessionToDisk() {
	path := s.execSessionPath()
	if path == "" {
		return
	}
	cacheRead := 0
	if s.lastUsage != nil {
		cacheRead = s.lastUsage.CacheReadInputTokens
	}
	data, err := json.Marshal(execSessionDiskFormat{
		ClaudeSessionID: s.claudeSessionID,
		SavedAt:         time.Now().UTC(),
		CacheReadTokens: cacheRead,
	})
	if err != nil {
		log.Printf("[simple-session] failed to marshal session file: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[simple-session] failed to save session to disk: %v", err)
		return
	}
	if s.claudeSessionID != "" {
		log.Printf("[simple-session] Saved Claude session ID to disk: %s", s.claudeSessionID)
	} else {
		log.Printf("[simple-session] Cleared Claude session ID from disk")
	}
}

// LoadSessionFromDisk reads a previously persisted claudeSessionID from disk.
// On success, sets s.claudeSessionID so the next wake can resume the session.
// Must be called before any prompts are sent (i.e. at startup).
func (s *SimpleSession) LoadSessionFromDisk() {
	path := s.execSessionPath()
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		log.Printf("[simple-session] failed to read session file: %v", err)
		return
	}
	var sf execSessionDiskFormat
	if err := json.Unmarshal(data, &sf); err != nil {
		log.Printf("[simple-session] failed to parse session file: %v", err)
		return
	}
	if sf.ClaudeSessionID != "" {
		s.claudeSessionID = sf.ClaudeSessionID
		log.Printf("[simple-session] Loaded Claude session ID from disk: %s (saved %v ago)",
			sf.ClaudeSessionID, time.Since(sf.SavedAt).Round(time.Second))
	}
}

// WriteSessionLogEntry appends a message to the session log file (if set).
// Used to write section headers like wake start and context-clear markers.
func (s *SimpleSession) WriteSessionLogEntry(format string, args ...any) {
	if s.sessionLogPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.sessionLogPath), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(s.sessionLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(f, msg)
}

// isResetPending checks if a memory reset is pending
func (s *SimpleSession) isResetPending() bool {
	path := s.resetPendingPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// clearResetPending removes the reset pending flag
func (s *SimpleSession) clearResetPending() {
	path := s.resetPendingPath()
	if path != "" {
		os.Remove(path)
	}
}

// SessionID returns the current session ID
func (s *SimpleSession) SessionID() string {
	return s.sessionID
}

// HasSeenMemory returns true if a memory trace has been sent to this session
func (s *SimpleSession) HasSeenMemory(id string) bool {
	return s.seenMemoryIDs[id]
}

// MarkMemoriesSeen marks memory traces as having been sent to Claude
func (s *SimpleSession) MarkMemoriesSeen(ids []string) {
	for _, id := range ids {
		s.seenMemoryIDs[id] = true
	}
}

// GetOrAssignMemoryID returns the display ID for a trace, assigning one if needed.
// Uses the first 5 chars of the real engram ID so it can be queried directly via
// GET /v1/engrams/<id>.
func (s *SimpleSession) GetOrAssignMemoryID(traceID string) string {
	if id, exists := s.memoryIDMap[traceID]; exists {
		return id
	}
	id := traceID
	if len(id) > 5 {
		id = id[:5]
	}
	s.memoryIDMap[traceID] = id
	return id
}

// ResolveMemoryEval takes a memory_eval map like {"a3f9c": 5, "b2e1d": 1} (5-char engram prefix)
// and returns a map of trace_id -> rating by reversing the memoryIDMap lookup.
// Also skips legacy "M1", "M2" format keys which can no longer be resolved.
// Unknown display IDs are skipped.
func (s *SimpleSession) ResolveMemoryEval(eval map[string]any) map[string]int {
	// Build reverse map: display_id -> trace_id
	reverseMap := make(map[string]string, len(s.memoryIDMap))
	for traceID, displayID := range s.memoryIDMap {
		reverseMap[displayID] = traceID
	}

	resolved := make(map[string]int)
	for key, val := range eval {
		// Parse rating value first
		var rating int
		switch v := val.(type) {
		case float64:
			rating = int(v)
		case int:
			rating = v
		default:
			continue
		}

		// Try new format (tr_xxxxx) first
		if traceID, ok := reverseMap[key]; ok {
			resolved[traceID] = rating
			continue
		}

		// Legacy format: Parse "M1" -> look up in old sequential map
		// This won't work for new sessions but provides graceful degradation
		var displayID int
		if _, err := fmt.Sscanf(key, "M%d", &displayID); err == nil {
			// Can't resolve legacy IDs in new system, skip
			continue
		}
	}
	return resolved
}

// PrepareNewSession rotates the session ID and clears per-prompt state so the
// caller can record the correct ID with the session tracker before sending.
// Must be called before StartSession + SendPrompt.
func (s *SimpleSession) PrepareNewSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = generateSessionUUID()
	s.sessionStartTime = time.Now()
	s.memoryIDMap = make(map[string]string)
	s.seenMemoryIDs = make(map[string]bool)
	s.claudeSessionID = ""
	s.isResuming = false
}

// PrepareForResume prepares for a new turn in an ongoing Claude session.
// Unlike PrepareNewSession, it preserves claudeSessionID (for --resume),
// seenMemoryIDs (to avoid re-injecting seen memories). It clears memoryIDMap
// so memory self-eval display IDs are fresh for this turn, and sets isResuming
// so buildPrompt skips static context already present in the session history.
//
// Call this instead of PrepareNewSession when ClaudeSessionID() is non-empty
// and ShouldReset() is false.
func (s *SimpleSession) PrepareForResume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = generateSessionUUID() // Fresh tracking ID per turn
	s.sessionStartTime = time.Now()
	s.memoryIDMap = make(map[string]string) // Fresh display IDs for memory eval
	// claudeSessionID preserved — used for --resume flag
	// seenMemoryIDs preserved — avoids re-injecting already-sent memories
	s.isResuming = true
}

// IsResuming returns true if this turn is resuming an existing Claude session.
// Used by buildPrompt to skip static context already in the session history.
func (s *SimpleSession) IsResuming() bool {
	return s.isResuming
}

// SetSessionLog sets the file path where conversation events are streamed.
// Call this before SendPrompt; the file is created (or appended to) when SendPrompt runs.
func (s *SimpleSession) SetSessionLog(path string) {
	s.sessionLogPath = path
}

// openSessionLog opens (or creates) the session log file for appending.
// Returns nil if sessionLogPath is empty.
func (s *SimpleSession) openSessionLog() *os.File {
	if s.sessionLogPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.sessionLogPath), 0755); err != nil {
		log.Printf("[simple-session] cannot create log dir: %v", err)
		return nil
	}
	f, err := os.OpenFile(s.sessionLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("[simple-session] cannot open session log %s: %v", s.sessionLogPath, err)
		return nil
	}
	return f
}

// writeLog appends a timestamped line to the session log file (if open).
func writeLog(f *os.File, format string, args ...any) {
	if f == nil {
		return
	}
	line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	f.WriteString(line)
}

// OnToolCall sets the callback for tool calls (informational — actual execution
// happens inside the Claude subprocess via MCP).
func (s *SimpleSession) OnToolCall(fn func(name string, args map[string]any) (string, error)) {
	s.onToolCall = fn
}

// OnOutput sets the callback for Claude's text output
func (s *SimpleSession) OnOutput(fn func(text string)) {
	s.onOutput = fn
}

// SendPromptWithCfg sends a prompt to Claude with explicit config and blocks until the response is complete.
func (s *SimpleSession) SendPromptWithCfg(ctx context.Context, prompt string, cfg ClaudeConfig) error {
	s.lastCfg = cfg
	// Check for reset pending before sending
	if s.isResetPending() {
		log.Printf("[simple-session] Reset pending, waiting for reset_session signal...")
		deadline := time.Now().Add(10 * time.Second)
		for s.isResetPending() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if s.isResetPending() {
			log.Printf("[simple-session] Warning: reset still pending after timeout, clearing flag")
			s.clearResetPending()
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.currentPromptHasText = false

	// Open session log for this prompt (append if resuming).
	logFile := s.openSessionLog()
	if logFile != nil {
		defer logFile.Close()
		writeLog(logFile, "=== PROMPT (%d chars) ===", len(prompt))
		// Write full prompt with a header/footer so it's easy to scroll past.
		fmt.Fprintf(logFile, "%s\n=== END PROMPT ===\n\n", prompt)
	}

	// Build base options (no WithResume) so we can retry without it if needed.
	baseOpts := []claudecode.Option{
		claudecode.WithPermissionMode(claudecode.PermissionModeBypassPermissions),
		claudecode.WithPartialStreaming(), // captures SessionID from first StreamEvent
	}
	if cfg.MCPServerURL != "" {
		baseOpts = append(baseOpts, claudecode.WithMcpServers(map[string]claudecode.McpServerConfig{
			"bud2": &claudecode.McpHTTPServerConfig{
				Type: claudecode.McpServerTypeHTTP,
				URL:  cfg.MCPServerURL,
			},
		}))
	}
	if cfg.Model != "" {
		baseOpts = append(baseOpts, claudecode.WithModel(cfg.Model))
	}
	if cfg.WorkDir != "" {
		baseOpts = append(baseOpts, claudecode.WithCwd(cfg.WorkDir))
	}
	if len(cfg.AgentDefs) > 0 {
		baseOpts = append(baseOpts, claudecode.WithAgents(cfg.AgentDefs))
	}
	localPlugins, manifestPlugins := s.cachedPlugins()
	for _, pluginPath := range localPlugins {
		baseOpts = append(baseOpts, claudecode.WithLocalPlugin(pluginPath))
	}
	for _, pluginPath := range manifestPlugins {
		baseOpts = append(baseOpts, claudecode.WithLocalPlugin(pluginPath))
	}
	generateZettelLibraries(s.statePath)

	// Resume existing Claude session when available, otherwise let the SDK
	// create a new session (no --session-id equivalent in SDK).
	opts := append([]claudecode.Option{}, baseOpts...)
	if s.claudeSessionID != "" && s.isResuming {
		log.Printf("[simple-session] Resuming Claude session %s (bud turn %s)", s.claudeSessionID, s.sessionID)
		opts = append(opts, claudecode.WithResume(s.claudeSessionID))
	}

	const sessionTimeout = 30 * time.Minute
	timeoutCtx, cancel := context.WithTimeout(ctx, sessionTimeout)
	defer cancel()

	logging.Debug("simple-session", "SendPrompt: prompt_len=%d resuming=%v", len(prompt), s.isResuming)

	sendWithOpts := func(sendOpts []claudecode.Option) error {
		return claudecode.WithClient(timeoutCtx, func(client claudecode.Client) error {
			if err := client.Query(timeoutCtx, prompt); err != nil {
				return err
			}
			msgCount := 0
			heartbeatDone := make(chan struct{})
			go func() {
				t := time.NewTicker(60 * time.Second)
				defer t.Stop()
				lastCount := 0
				for {
					select {
					case <-t.C:
						if msgCount == lastCount {
							log.Printf("[simple-session] WARNING: no new messages for 60s (msgs_so_far=%d)", msgCount)
						}
						lastCount = msgCount
					case <-heartbeatDone:
						return
					case <-timeoutCtx.Done():
						return
					}
				}
			}()
			cb := receiveLoopCallbacks{
				LogPrefix: "simple-session",
				OnMsg:     func() { msgCount++ },
				OnStreamEvent: func(sessionID string) {
					// Capture session ID from the first streaming event — this arrives
					// within milliseconds and ensures claudeSessionID is set before
					// signal_done can cancel the context (which skips ResultMessage).
					if s.claudeSessionID == "" {
						s.claudeSessionID = sessionID
						logging.Debug("simple-session", "Captured Claude session ID early: %s", sessionID)
					}
				},
				OnText: func(text string) {
					if !s.currentPromptHasText {
						s.currentPromptHasText = true
					}
					logging.Debug("simple-session", "Text block (%d chars)", len(text))
					if s.onOutput != nil {
						s.onOutput(text)
					}
				},
				OnTool: func(name string, input map[string]any) {
					logging.Debug("simple-session", "Tool call: %s", name)
					if s.onToolCall != nil {
						s.onToolCall(name, input)
					}
				},
				OnResult: func(m *claudecode.ResultMessage) {
					s.claudeSessionID = m.SessionID
					s.lastUsage = parseUsageFromResult(m)
					log.Printf("[simple-session] Claude session ID: %s (turns=%d duration=%dms)",
						m.SessionID, m.NumTurns, m.DurationMs)
				},
			}
			receiveLoop(timeoutCtx, client, logFile, cb) //nolint:errcheck
			close(heartbeatDone)
			return nil
		}, sendOpts...)
	}

	err := sendWithOpts(opts)

	// Graceful recovery: if --resume pointed at a session that no longer exists,
	// clear the session ID and retry without it so the wake doesn't fail entirely.
	if err != nil && s.isResuming && isSessionNotFoundError(err) {
		log.Printf("[simple-session] WARNING: Claude session %s not found, starting fresh", s.claudeSessionID)
		s.claudeSessionID = ""
		s.isResuming = false
		writeLog(logFile, "WARNING: session not found, retrying without --resume")
		err = sendWithOpts(baseOpts)
	}

	// Detect broken/cascading sessions: a resumed session that returns 0 turns with 0
	// tokens (and the context wasn't cancelled) means the session is in a bad server-side
	// state and will continue to cascade. Clear the session ID and retry fresh.
	if err == nil && ctx.Err() == nil && s.isResuming &&
		s.lastUsage != nil && s.lastUsage.NumTurns == 0 &&
		s.lastUsage.InputTokens == 0 && s.lastUsage.OutputTokens == 0 {
		log.Printf("[simple-session] WARNING: resumed session %s returned 0 turns/tokens, session may be corrupted — retrying fresh", s.claudeSessionID)
		s.claudeSessionID = ""
		s.isResuming = false
		writeLog(logFile, "WARNING: 0-turn resume detected, retrying without --resume")
		err = sendWithOpts(baseOpts)
	}

	if err != nil {
		if ctx.Err() != nil {
			log.Printf("[simple-session] Session %s interrupted (context cancelled)", s.sessionID)
			return ErrInterrupted
		}
		if errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			log.Printf("[simple-session] Session %s timed out after %v", s.sessionID, sessionTimeout)
			return fmt.Errorf("claude session timed out after %v", sessionTimeout)
		}
		// Ignore unknown message type errors (e.g. rate_limit_event) — non-fatal
		if strings.Contains(err.Error(), "unknown message type") {
			return nil
		}
		return err
	}

	return nil
}

// isSessionNotFoundError returns true if the error indicates the Claude session
// referenced by --resume no longer exists. Used to trigger graceful recovery.
func isSessionNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "session not found") ||
		strings.Contains(msg, "no conversation found") ||
		strings.Contains(msg, "invalid session") ||
		strings.Contains(msg, "session does not exist") ||
		strings.Contains(msg, "conversation not found")
}

// parseUsageFromResult extracts SessionUsage from a ResultMessage.
func parseUsageFromResult(m *claudecode.ResultMessage) *SessionUsage {
	usage := &SessionUsage{
		NumTurns:      m.NumTurns,
		DurationMs:    m.DurationMs,
		DurationApiMs: m.DurationAPIMs,
	}
	if m.Usage != nil {
		u := *m.Usage
		usage.InputTokens = intFromUsage(u, "input_tokens")
		usage.OutputTokens = intFromUsage(u, "output_tokens")
		usage.CacheCreationInputTokens = intFromUsage(u, "cache_creation_input_tokens")
		usage.CacheReadInputTokens = intFromUsage(u, "cache_read_input_tokens")
	}
	if usage.InputTokens > 0 || usage.OutputTokens > 0 {
		logging.Debug("simple-session", "Usage: input=%d output=%d cache_read=%d turns=%d duration=%dms",
			usage.InputTokens, usage.OutputTokens, usage.CacheReadInputTokens, usage.NumTurns, usage.DurationMs)
	}
	return usage
}

// intFromUsage extracts an int value from the Usage map by key.
func intFromUsage(u map[string]any, key string) int {
	v, ok := u[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// Close is a no-op since there's no persistent process to clean up
func (s *SimpleSession) Close() error {
	return nil
}

// Reset clears the session state for a fresh start
func (s *SimpleSession) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionID = generateSessionUUID()
	s.sessionStartTime = time.Now()
	s.seenMemoryIDs = make(map[string]bool)
	s.memoryIDMap = make(map[string]string)
	s.lastUsage = nil      // Clear usage data
	s.claudeSessionID = "" // Force new session (no resume)
	s.isResuming = false
	s.clearResetPending() // Clear the pending flag so new sessions can start
	log.Printf("[simple-session] Session reset complete, new session ID: %s", s.sessionID)
}

// LastUsage returns the usage metrics from the last completed prompt, or nil
func (s *SimpleSession) LastUsage() *SessionUsage {
	return s.lastUsage
}

// ClaudeSessionID returns the Claude-assigned session ID from the last completed
// prompt. Use this value with `claude --resume` to reload the session.
func (s *SimpleSession) ClaudeSessionID() string {
	return s.claudeSessionID
}

// ShouldReset returns true if the session should be reset before sending
// the next prompt. Uses context token count from the API response.
func (s *SimpleSession) ShouldReset() bool {
	if s.lastUsage == nil {
		return false
	}

	// Total context = cached history + new input tokens
	totalContext := s.lastUsage.CacheReadInputTokens + s.lastUsage.InputTokens
	if totalContext > MaxContextTokens {
		log.Printf("[simple-session] Context tokens %d exceeds threshold %d, should reset",
			totalContext, MaxContextTokens)
		return true
	}

	return false
}

// safeUsage extracts the usage map from a ResultMessage, returning nil if absent.
func safeUsage(m *claudecode.ResultMessage) map[string]any {
	if m.Usage == nil {
		return nil
	}
	return *m.Usage
}
