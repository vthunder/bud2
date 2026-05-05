package executive

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vthunder/bud2/internal/logging"
	"gopkg.in/yaml.v3"
)

// clawhubDownloadBase is the ClaWHub HTTP download API endpoint.
// Discovered by reverse-engineering the ClaWHub frontend bundle.
const clawhubDownloadBase = "https://wry-manatee-359.convex.site/api/v1/download"

// skillManifestEntry is a single entry in the skills: section of plugins.yaml.
// Supports three source types via explicit prefix:
//
//	clawhub:slug[@version]                        — ClaWHub registry
//	clawhub:owner/slug[@version]                  — owner prefix is ignored (slugs are global)
//	clawhub:https://clawhub.ai/owner/slug[@ver]   — full browser URL
//	git:owner/repo[:dir][@ref]                    — GitHub git repo
//	path:/local/path                              — local filesystem
//
// Object form uses the source type as the key name:
//
//	clawhub: trello
//	version: "1.0.0"
//
//	git: owner/repo
//	dir: path/containing/skills/subdir
//	ref: v1.0.0
//
//	path: /local/path
type skillManifestEntry struct {
	// ClaWHub fields
	clawSlug    string // globally unique slug
	clawVersion string // empty = floating/latest

	// Git fields (shared clone cache with plugins)
	owner string
	repo  string
	dir   string // subdir within repo treated as plugin dir (should contain skills/ subdir)
	ref   string // branch/tag/commit; empty = default branch

	// Local path
	localPath string
}

// UnmarshalYAML handles both string shorthand and object form for skill entries.
func (e *skillManifestEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		return e.parseSkillString(value.Value)
	}
	// Object form: key name is the source type
	var raw struct {
		ClaWHub string `yaml:"clawhub"`
		Version string `yaml:"version"`
		Git     string `yaml:"git"`
		Dir     string `yaml:"dir"`
		Ref     string `yaml:"ref"`
		Path    string `yaml:"path"`
	}
	if err := value.Decode(&raw); err != nil {
		return err
	}
	switch {
	case raw.ClaWHub != "":
		if err := e.parseClawhubSlug(raw.ClaWHub); err != nil {
			return err
		}
		e.clawVersion = raw.Version
	case raw.Git != "":
		if err := e.parseGitString(raw.Git); err != nil {
			return err
		}
		if raw.Dir != "" {
			e.dir = raw.Dir
		}
		if raw.Ref != "" {
			e.ref = raw.Ref
		}
	case raw.Path != "":
		e.localPath = raw.Path
	default:
		return fmt.Errorf("skill entry object must have one of: clawhub, git, path")
	}
	return nil
}

// parseSkillString parses a string skill entry with an explicit type prefix.
func (e *skillManifestEntry) parseSkillString(s string) error {
	switch {
	case strings.HasPrefix(s, "clawhub:"):
		rest := strings.TrimPrefix(s, "clawhub:")
		// Extract @version suffix before slug parsing
		if idx := strings.LastIndex(rest, "@"); idx != -1 {
			e.clawVersion = rest[idx+1:]
			rest = rest[:idx]
		}
		return e.parseClawhubSlug(rest)
	case strings.HasPrefix(s, "git:"):
		return e.parseGitString(strings.TrimPrefix(s, "git:"))
	case strings.HasPrefix(s, "path:"):
		e.localPath = strings.TrimPrefix(s, "path:")
		return nil
	default:
		return fmt.Errorf("skill entry %q requires a prefix: clawhub:, git:, or path:", s)
	}
}

// parseClawhubSlug extracts the globally-unique slug from any of these forms:
//
//	trello                              → trello
//	steipete/trello                     → trello  (owner prefix is cosmetic, slugs are global)
//	https://clawhub.ai/steipete/trello  → trello
func (e *skillManifestEntry) parseClawhubSlug(s string) error {
	// Strip full URL prefix if present
	s = strings.TrimPrefix(s, "https://clawhub.ai/")
	// Extract last path component (handles both "owner/slug" and bare "slug")
	if idx := strings.LastIndex(s, "/"); idx != -1 {
		s = s[idx+1:]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty clawhub slug")
	}
	e.clawSlug = s
	return nil
}

// parseGitString parses "owner/repo[:dir][@ref]" into the entry's fields.
func (e *skillManifestEntry) parseGitString(s string) error {
	if idx := strings.LastIndex(s, "@"); idx != -1 {
		e.ref = s[idx+1:]
		s = s[:idx]
	}
	if idx := strings.Index(s, ":"); idx != -1 {
		e.dir = s[idx+1:]
		s = s[:idx]
	}
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid git skill entry %q: expected owner/repo", s)
	}
	e.owner = parts[0]
	e.repo = parts[1]
	return nil
}

// clawhubSkillsDir returns the virtual plugin dir root for ClaWHub skills.
// Skills are cached at {clawhubSkillsDir}/skills/{slug}/SKILL.md, which matches
// the path pattern that LoadSkillContent already searches.
func clawhubSkillsDir(cacheBase string) string {
	return filepath.Join(cacheBase, "bud", "skills-clawhub")
}

// loadManifestSkills reads the skills: section of plugins.yaml and ensures
// all remote skills are downloaded/cloned into the local cache. Called once per
// process from cachedPlugins().
//
// ClaWHub pinned skills: downloaded once, never re-fetched while cached version matches.
// ClaWHub floating skills: re-fetched if the cached _meta.json is older than updateInterval.
// Git skills: cloned once (with sparse checkout if dir is set); updated on existing clones.
func loadManifestSkills(statePath string, updateInterval time.Duration) {
	manifestPath := pluginsManifestPath(statePath)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("[skills] failed to read manifest: %v", err)
		}
		return
	}

	var manifest pluginManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		log.Printf("[skills] failed to parse manifest: %v", err)
		return
	}

	if len(manifest.Skills) == 0 {
		return
	}

	cacheBase, err := os.UserCacheDir()
	if err != nil {
		log.Printf("[skills] failed to resolve user cache dir: %v", err)
		return
	}

	for _, e := range manifest.Skills {
		switch {
		case e.clawSlug != "":
			downloadClawhubSkill(e.clawSlug, e.clawVersion, cacheBase, updateInterval)
		case e.owner != "":
			cloneOrUpdateGitSkillEntry(e, cacheBase)
		// path: entries are used directly via resolvedManifestSkillDirs — nothing to download
		}
	}
}

// resolvedManifestSkillDirs returns the already-on-disk directories for git and
// local skill entries in plugins.yaml. No git operations — only returns dirs
// that exist. ClaWHub skills are served via clawhubSkillsDir, not per-entry dirs.
func resolvedManifestSkillDirs(statePath string) []string {
	manifestPath := pluginsManifestPath(statePath)
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

	var dirs []string
	for _, e := range manifest.Skills {
		var localPath string
		switch {
		case e.owner != "":
			repoDir := filepath.Join(cacheBase, "bud", "plugins", e.owner, e.repo)
			localPath = repoDir
			if e.dir != "" {
				localPath = filepath.Join(repoDir, e.dir)
			}
		case e.localPath != "":
			localPath = e.localPath
		default:
			continue // ClaWHub: handled via clawhubSkillsDir
		}
		if _, err := os.Stat(localPath); err == nil {
			dirs = append(dirs, localPath)
		}
	}
	return dirs
}

// downloadClawhubSkill downloads a ClaWHub skill zip and extracts it into the
// skills-clawhub virtual plugin dir. The extracted layout is:
//
//	{clawhubSkillsDir}/skills/{slug}/SKILL.md
//	{clawhubSkillsDir}/skills/{slug}/assets/...   (multi-file skills)
func downloadClawhubSkill(slug, version, cacheBase string, updateInterval time.Duration) {
	skillDir := filepath.Join(clawhubSkillsDir(cacheBase), "skills", slug)
	metaPath := filepath.Join(skillDir, "_meta.json")
	pinned := version != ""

	if pinned {
		// Pinned: skip download if the cached version already matches.
		if readClawhubMetaVersion(metaPath) == version {
			logging.Debug("skills", "clawhub:%s@%s cached, skipping", slug, version)
			return
		}
	} else {
		// Floating: skip if recently checked (updateInterval == 0 means never auto-update).
		if updateInterval > 0 {
			if info, err := os.Stat(metaPath); err == nil {
				if time.Since(info.ModTime()) < updateInterval {
					logging.Debug("skills", "clawhub:%s recently checked, skipping", slug)
					return
				}
			}
		} else {
			// Auto-update disabled: only download on cache miss.
			if _, err := os.Stat(metaPath); err == nil {
				return
			}
		}
	}

	// Build download URL
	dlURL := fmt.Sprintf("%s?slug=%s", clawhubDownloadBase, slug)
	if pinned {
		dlURL += "&version=" + version
	}

	resp, err := http.Get(dlURL) //nolint:noctx
	if err != nil {
		log.Printf("[skills] failed to download clawhub:%s: %v", slug, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("[skills] clawhub:%s download returned HTTP %d", slug, resp.StatusCode)
		return
	}

	zipData, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[skills] failed to read clawhub:%s response: %v", slug, err)
		return
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		log.Printf("[skills] failed to parse clawhub:%s zip: %v", slug, err)
		return
	}

	// For floating skills: check version before overwriting to avoid unnecessary writes.
	if !pinned {
		newVersion := clawhubVersionFromZip(zr)
		if newVersion != "" && readClawhubMetaVersion(metaPath) == newVersion {
			// Already up to date — touch _meta.json to reset the staleness clock.
			now := time.Now()
			os.Chtimes(metaPath, now, now) //nolint:errcheck
			logging.Debug("skills", "clawhub:%s already at latest (%s), touched meta", slug, newVersion)
			return
		}
	}

	// Extract all files, preserving the directory tree.
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		log.Printf("[skills] failed to create skill dir %s: %v", skillDir, err)
		return
	}
	extracted := 0
	for _, f := range zr.File {
		if err := extractZipEntry(f, skillDir); err != nil {
			log.Printf("[skills] extract %s from clawhub:%s: %v", f.Name, slug, err)
			continue
		}
		extracted++
	}
	log.Printf("[skills] downloaded clawhub:%s (%d files)", slug, extracted)
}

// readClawhubMetaVersion reads the version string from a cached _meta.json.
// Returns "" if the file is missing or unparseable.
func readClawhubMetaVersion(metaPath string) string {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var meta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.Version
}

// clawhubVersionFromZip reads the version from the _meta.json inside a zip archive.
func clawhubVersionFromZip(zr *zip.Reader) string {
	for _, f := range zr.File {
		if f.Name != "_meta.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			return ""
		}
		var meta struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &meta); err != nil {
			return ""
		}
		return meta.Version
	}
	return ""
}

// extractZipEntry writes a single zip file entry to destDir, preserving the
// relative path. Rejects entries whose resolved path escapes destDir.
func extractZipEntry(f *zip.File, destDir string) error {
	destPath := filepath.Join(destDir, filepath.FromSlash(f.Name))
	// Path-traversal guard
	cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(destPath)+string(os.PathSeparator), cleanDest) {
		return fmt.Errorf("zip entry %q would escape destination directory", f.Name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(destPath, f.Mode())
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return err
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

// cloneOrUpdateGitSkillEntry clones or updates a git-sourced skill repo.
// Uses sparse checkout when dir is specified to fetch only the relevant subtree.
// Clones share the same cache dir as manifest plugins (~/Library/Caches/bud/plugins/).
func cloneOrUpdateGitSkillEntry(e skillManifestEntry, cacheBase string) {
	repoDir := filepath.Join(cacheBase, "bud", "plugins", e.owner, e.repo)

	if _, err := os.Stat(repoDir); os.IsNotExist(err) {
		cloneURL := fmt.Sprintf("https://github.com/%s/%s.git", e.owner, e.repo)
		var args []string
		if e.dir != "" {
			// Sparse checkout: fetch only the needed subtree.
			args = []string{"clone", "--depth=1", "--filter=blob:none", "--no-checkout", "--sparse"}
		} else {
			args = []string{"clone", "--depth=1"}
		}
		if e.ref != "" {
			args = append(args, "--branch", e.ref)
		}
		args = append(args, cloneURL, repoDir)

		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			log.Printf("[skills] failed to clone git:%s/%s: %v\n%s", e.owner, e.repo, err, out)
			return
		}

		if e.dir != "" {
			sparse := exec.Command("git", "-C", repoDir, "sparse-checkout", "set", e.dir)
			if out, err := sparse.CombinedOutput(); err != nil {
				log.Printf("[skills] sparse-checkout set failed for git:%s/%s: %v\n%s", e.owner, e.repo, err, out)
			}
			ref := e.ref
			if ref == "" {
				ref = "HEAD"
			}
			if out, err := exec.Command("git", "-C", repoDir, "checkout", ref).CombinedOutput(); err != nil {
				log.Printf("[skills] checkout failed for git:%s/%s: %v\n%s", e.owner, e.repo, err, out)
			}
		}
		log.Printf("[skills] cloned git:%s/%s", e.owner, e.repo)
		return
	}

	// Repo exists — update it.
	if e.ref != "" {
		fetch := exec.Command("git", "-C", repoDir, "fetch", "--depth=1", "origin", e.ref)
		if out, err := fetch.CombinedOutput(); err != nil {
			log.Printf("[skills] failed to fetch git:%s/%s@%s: %v\n%s", e.owner, e.repo, e.ref, err, out)
			return
		}
		if e.dir != "" {
			exec.Command("git", "-C", repoDir, "sparse-checkout", "set", e.dir).CombinedOutput() //nolint:errcheck
		}
		if out, err := exec.Command("git", "-C", repoDir, "checkout", e.ref).CombinedOutput(); err != nil {
			log.Printf("[skills] failed to checkout git:%s/%s@%s: %v\n%s", e.owner, e.repo, e.ref, err, out)
		}
	} else {
		if out, err := exec.Command("git", "-C", repoDir, "pull", "--ff-only").CombinedOutput(); err != nil {
			log.Printf("[skills] failed to pull git:%s/%s: %v\n%s", e.owner, e.repo, err, out)
		}
	}
}
