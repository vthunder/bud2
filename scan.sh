#!/usr/bin/env bash
# ============================================================================
# Axios Supply-Chain Compromise Scanner
# Date: 2026-03-31
# Based on: https://gist.github.com/joe-desimone/36061dabd2bc2913705e0d083a9673e7
#
# Scans for:
#   1. Compromised axios versions (1.14.1, 0.30.4) in lockfiles & node_modules
#   2. Malicious package "plain-crypto-js" anywhere on disk
#   3. Platform-specific stage-2 payload IOCs (filesystem)
#   4. Active C2 connections to sfrclak.com
#   5. Global npm/yarn/pnpm packages
#   6. npm cache contamination
#
# Usage:
#   chmod +x scan_axios_compromise.sh
#   ./scan_axios_compromise.sh                    # scan common paths
#   ./scan_axios_compromise.sh /path/to/projects  # scan specific directory
# ============================================================================

set -euo pipefail

RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

FOUND_ISSUES=0

banner() {
  echo ""
  echo -e "${BOLD}========================================${NC}"
  echo -e "${BOLD}  Axios Compromise Scanner (2026-03-31)${NC}"
  echo -e "${BOLD}========================================${NC}"
  echo ""
}

section() {
  echo ""
  echo -e "${CYAN}[*] $1${NC}"
  echo -e "${CYAN}$(printf '%.0s-' {1..50})${NC}"
}

found() {
  echo -e "${RED}[!!!] FOUND: $1${NC}"
  FOUND_ISSUES=$((FOUND_ISSUES + 1))
}

warn() {
  echo -e "${YELLOW}[!] WARNING: $1${NC}"
}

safe() {
  echo -e "${GREEN}[✓] $1${NC}"
}

info() {
  echo -e "    $1"
}

# Determine scan root
SCAN_ROOT="${1:-$HOME}"
OS="$(uname -s)"

banner
echo "Platform detected: $OS"
echo "Scan root:         $SCAN_ROOT"
echo "Date:              $(date -u '+%Y-%m-%dT%H:%M:%SZ')"

# ============================================================================
# 1. FILESYSTEM IOCs — Stage-2 Payloads
# ============================================================================
section "Checking for stage-2 payload IOCs on disk"

# macOS
if [[ "$OS" == "Darwin" ]]; then
  if [[ -f "/Library/Caches/com.apple.act.mond" ]]; then
    found "macOS stage-2 binary at /Library/Caches/com.apple.act.mond"
    ls -la "/Library/Caches/com.apple.act.mond" 2>/dev/null || true
  else
    safe "No macOS stage-2 binary found"
  fi
fi

# Linux
if [[ "$OS" == "Linux" ]]; then
  if [[ -f "/tmp/ld.py" ]]; then
    found "Linux stage-2 payload at /tmp/ld.py"
    ls -la "/tmp/ld.py" 2>/dev/null || true
    echo "    First 5 lines:"
    head -5 "/tmp/ld.py" 2>/dev/null | sed 's/^/    /' || true
  else
    safe "No Linux stage-2 payload (/tmp/ld.py) found"
  fi
fi

# Windows (WSL / Git Bash / MSYS)
if [[ -d "/mnt/c" ]] || [[ "$OS" == "MINGW"* ]] || [[ "$OS" == "MSYS"* ]]; then
  PROGRAMDATA="${PROGRAMDATA:-/mnt/c/ProgramData}"
  TEMP_DIR="${TEMP:-${LOCALAPPDATA:-/mnt/c/Users/$USER/AppData/Local}/Temp}"

  for f in "$PROGRAMDATA/wt.exe" "$TEMP_DIR/6202033.vbs" "$TEMP_DIR/6202033.ps1"; do
    if [[ -f "$f" ]]; then
      found "Windows IOC found: $f"
    fi
  done
  safe "Windows IOC check complete"
fi

# ============================================================================
# 2. NETWORK — Active C2 Connections
# ============================================================================
section "Checking for active connections to C2 (sfrclak.com)"

C2_FOUND=0
if command -v ss &>/dev/null; then
  if ss -tunap 2>/dev/null | grep -qi "sfrclak"; then
    found "Active connection to sfrclak.com detected!"
    ss -tunap 2>/dev/null | grep -i "sfrclak" | sed 's/^/    /'
    C2_FOUND=1
  fi
elif command -v netstat &>/dev/null; then
  if netstat -an 2>/dev/null | grep -qi "sfrclak"; then
    found "Active connection to sfrclak.com detected!"
    netstat -an 2>/dev/null | grep -i "sfrclak" | sed 's/^/    /'
    C2_FOUND=1
  fi
elif command -v lsof &>/dev/null; then
  if lsof -i -n 2>/dev/null | grep -qi "sfrclak"; then
    found "Active connection to sfrclak.com detected!"
    lsof -i -n 2>/dev/null | grep -i "sfrclak" | sed 's/^/    /'
    C2_FOUND=1
  fi
fi

if [[ $C2_FOUND -eq 0 ]]; then
  safe "No active C2 connections detected"
fi

# Also check DNS cache / hosts if possible
if command -v dig &>/dev/null; then
  C2_IP=$(dig +short sfrclak.com 2>/dev/null || true)
  if [[ -n "$C2_IP" ]]; then
    info "C2 domain sfrclak.com resolves to: $C2_IP"
    info "Check firewall/DNS logs for connections to this IP"
  fi
fi

# ============================================================================
# 3. GLOBAL NPM / YARN / PNPM PACKAGES
# ============================================================================
section "Checking globally installed npm packages"

check_global_package() {
  local pkg="$1"
  local manager="$2"
  local list_output="$3"

  if echo "$list_output" | grep -q "$pkg"; then
    found "$pkg found in global $manager packages!"
    echo "$list_output" | grep "$pkg" | sed 's/^/    /'
  fi
}

# npm global
if command -v npm &>/dev/null; then
  info "Scanning npm global packages..."
  NPM_GLOBAL=$(npm list -g --depth=0 2>/dev/null || true)
  check_global_package "axios@1.14.1" "npm" "$NPM_GLOBAL"
  check_global_package "axios@0.30.4" "npm" "$NPM_GLOBAL"
  check_global_package "plain-crypto-js" "npm" "$NPM_GLOBAL"

  # Also check all global with deep dependencies
  NPM_GLOBAL_DEEP=$(npm list -g --all 2>/dev/null || true)
  if echo "$NPM_GLOBAL_DEEP" | grep -q "plain-crypto-js"; then
    found "plain-crypto-js found as a transitive global npm dependency!"
    echo "$NPM_GLOBAL_DEEP" | grep "plain-crypto-js" | sed 's/^/    /'
  fi

  # Check npm global root for the actual files
  NPM_GLOBAL_ROOT=$(npm root -g 2>/dev/null || true)
  if [[ -n "$NPM_GLOBAL_ROOT" ]]; then
    if [[ -d "$NPM_GLOBAL_ROOT/plain-crypto-js" ]]; then
      found "plain-crypto-js directory exists at $NPM_GLOBAL_ROOT/plain-crypto-js"
    fi
    if [[ -d "$NPM_GLOBAL_ROOT/axios" ]]; then
      GLOBAL_AXIOS_VER=$(node -p "require('$NPM_GLOBAL_ROOT/axios/package.json').version" 2>/dev/null || echo "unknown")
      if [[ "$GLOBAL_AXIOS_VER" == "1.14.1" || "$GLOBAL_AXIOS_VER" == "0.30.4" ]]; then
        found "Compromised global axios version: $GLOBAL_AXIOS_VER"
      else
        safe "Global axios version $GLOBAL_AXIOS_VER (not compromised)"
      fi
    fi
  fi
else
  warn "npm not found, skipping npm global scan"
fi

# yarn global
if command -v yarn &>/dev/null; then
  info "Scanning yarn global packages..."
  YARN_GLOBAL=$(yarn global list 2>/dev/null || true)
  check_global_package "axios@1.14.1" "yarn" "$YARN_GLOBAL"
  check_global_package "axios@0.30.4" "yarn" "$YARN_GLOBAL"
  check_global_package "plain-crypto-js" "yarn" "$YARN_GLOBAL"
fi

# pnpm global
if command -v pnpm &>/dev/null; then
  info "Scanning pnpm global packages..."
  PNPM_GLOBAL=$(pnpm list -g 2>/dev/null || true)
  check_global_package "axios@1.14.1" "pnpm" "$PNPM_GLOBAL"
  check_global_package "axios@0.30.4" "pnpm" "$PNPM_GLOBAL"
  check_global_package "plain-crypto-js" "pnpm" "$PNPM_GLOBAL"
fi

# bun global
if command -v bun &>/dev/null; then
  info "Scanning bun global packages..."
  BUN_GLOBAL_ROOT="$HOME/.bun/install/global/node_modules"
  if [[ -d "$BUN_GLOBAL_ROOT/plain-crypto-js" ]]; then
    found "plain-crypto-js found in bun global packages!"
  fi
  if [[ -d "$BUN_GLOBAL_ROOT/axios" ]]; then
    BUN_AXIOS_VER=$(node -p "require('$BUN_GLOBAL_ROOT/axios/package.json').version" 2>/dev/null || echo "unknown")
    if [[ "$BUN_AXIOS_VER" == "1.14.1" || "$BUN_AXIOS_VER" == "0.30.4" ]]; then
      found "Compromised bun global axios version: $BUN_AXIOS_VER"
    fi
  fi
fi

# ============================================================================
# 4. NPM CACHE
# ============================================================================
section "Checking npm cache for compromised packages"

if command -v npm &>/dev/null; then
  NPM_CACHE_DIR=$(npm config get cache 2>/dev/null || echo "$HOME/.npm")

  # Check _cacache for plain-crypto-js entries
  if [[ -d "$NPM_CACHE_DIR/_cacache" ]]; then
    CACHE_HITS=$(find "$NPM_CACHE_DIR/_cacache" -name "*.json" -exec grep -l "plain-crypto-js" {} \; 2>/dev/null | head -20 || true)
    if [[ -n "$CACHE_HITS" ]]; then
      warn "plain-crypto-js found in npm cache (may indicate past install):"
      echo "$CACHE_HITS" | sed 's/^/    /'
      info "Run: npm cache clean --force"
    else
      safe "npm cache clean of plain-crypto-js"
    fi

    CACHE_AXIOS=$(find "$NPM_CACHE_DIR/_cacache" -name "*.json" -exec grep -l '"axios","version":"1.14.1"\|"axios","version":"0.30.4"' {} \; 2>/dev/null | head -20 || true)
    if [[ -n "$CACHE_AXIOS" ]]; then
      warn "Compromised axios version found in npm cache:"
      echo "$CACHE_AXIOS" | sed 's/^/    /'
    fi
  fi
fi

# ============================================================================
# 5. PROJECT LOCKFILES — Comprehensive Scan
# ============================================================================
section "Scanning lockfiles under $SCAN_ROOT (this may take a moment...)"

LOCKFILE_COUNT=0

scan_lockfile() {
  local lockfile="$1"
  local project_dir
  project_dir=$(dirname "$lockfile")
  local hit=0

  # Check for compromised axios versions
  # axios@X.Y.Z covers yarn.lock and pnpm-lock.yaml
  # -B5 context check covers package-lock.json (version is on a separate line from the package name)
  if grep -qE 'axios@1\.14\.1' "$lockfile" 2>/dev/null \
     || grep -B5 '"1\.14\.1"' "$lockfile" 2>/dev/null | grep -q '"axios'; then
    found "axios@1.14.1 in $lockfile"
    hit=1
  fi
  if grep -qE 'axios@0\.30\.4' "$lockfile" 2>/dev/null \
     || grep -B5 '"0\.30\.4"' "$lockfile" 2>/dev/null | grep -q '"axios'; then
    found "axios@0.30.4 in $lockfile"
    hit=1
  fi

  # Check for plain-crypto-js (should NEVER appear in any legit project)
  if grep -q "plain-crypto-js" "$lockfile" 2>/dev/null; then
    found "plain-crypto-js in $lockfile — THIS PACKAGE IS MALICIOUS"
    hit=1
  fi

  if [[ $hit -eq 0 ]]; then
    LOCKFILE_COUNT=$((LOCKFILE_COUNT + 1))
  fi
}

# Find all lockfiles (limit depth to avoid extremely deep traversals)
while IFS= read -r -d '' lockfile; do
  scan_lockfile "$lockfile"
done < <(find "$SCAN_ROOT" \
  -maxdepth 8 \
  -name "node_modules" -prune -o \
  -name ".git" -prune -o \
  \( -name "package-lock.json" -o -name "yarn.lock" -o -name "pnpm-lock.yaml" -o -name "bun.lockb" \) \
  -print0 2>/dev/null || true)

# For bun.lockb (binary format), try bun to inspect
if command -v bun &>/dev/null; then
  while IFS= read -r -d '' bunlock; do
    BUN_TEXT=$(cd "$(dirname "$bunlock")" && bun bun.lockb 2>/dev/null || true)
    if echo "$BUN_TEXT" | grep -q "plain-crypto-js"; then
      found "plain-crypto-js in $bunlock"
    fi
    if echo "$BUN_TEXT" | grep -qE "axios@1\.14\.1|axios@0\.30\.4"; then
      found "Compromised axios version in $bunlock"
    fi
  done < <(find "$SCAN_ROOT" -maxdepth 8 -name "bun.lockb" -print0 2>/dev/null || true)
fi

safe "$LOCKFILE_COUNT lockfiles scanned clean"

# ============================================================================
# 6. NODE_MODULES — Direct Inspection
# ============================================================================
section "Scanning node_modules directories for compromised packages"

NM_SCANNED=0

while IFS= read -r -d '' nm_dir; do
  NM_SCANNED=$((NM_SCANNED + 1))

  # Check for plain-crypto-js (should never exist)
  if [[ -d "$nm_dir/plain-crypto-js" ]]; then
    found "plain-crypto-js installed at $nm_dir/plain-crypto-js"

    # Check if setup.js still exists (it self-deletes, but worth checking)
    if [[ -f "$nm_dir/plain-crypto-js/setup.js" ]]; then
      found "setup.js payload STILL PRESENT at $nm_dir/plain-crypto-js/setup.js"
    fi

    # Check if package.json was swapped (anti-forensics check)
    if [[ -f "$nm_dir/plain-crypto-js/package.json" ]]; then
      if grep -q "postinstall" "$nm_dir/plain-crypto-js/package.json" 2>/dev/null; then
        found "package.json still contains postinstall hook (payload not yet cleaned up)"
      else
        warn "package.json has NO postinstall — likely swapped by anti-forensics (package.md → package.json)"
        warn "This means the payload ALREADY EXECUTED on this system"
      fi
    fi
  fi

  # Check axios version
  if [[ -f "$nm_dir/axios/package.json" ]]; then
    AXIOS_VER=$(node -p "try{require('$nm_dir/axios/package.json').version}catch(e){'parse-error'}" 2>/dev/null || true)
    if [[ "$AXIOS_VER" == "1.14.1" || "$AXIOS_VER" == "0.30.4" ]]; then
      found "Compromised axios@$AXIOS_VER at $nm_dir/axios/"

      # Check if it has plain-crypto-js as dependency
      if node -e "const p=require('$nm_dir/axios/package.json');process.exit(p.dependencies&&p.dependencies['plain-crypto-js']?0:1)" 2>/dev/null; then
        found "This axios has plain-crypto-js as a dependency — CONFIRMED COMPROMISED"
      fi
    fi
  fi
done < <(find "$SCAN_ROOT" -maxdepth 7 -type d -name "node_modules" -print0 2>/dev/null || true)

safe "$NM_SCANNED node_modules directories scanned"

# ============================================================================
# 7. RUNNING PROCESSES — Check for suspicious payloads
# ============================================================================
section "Checking running processes for known payload indicators"

PROC_HIT=0

if command -v ps &>/dev/null; then
  PS_OUTPUT=$(ps aux 2>/dev/null || true)

  for pattern in "com.apple.act.mond" "ld.py" "6202033" "sfrclak" "wt.exe.*hidden"; do
    if echo "$PS_OUTPUT" | grep -v "grep" | grep -qi "$pattern"; then
      found "Suspicious process matching '$pattern':"
      echo "$PS_OUTPUT" | grep -i "$pattern" | grep -v "grep" | sed 's/^/    /'
      PROC_HIT=1
    fi
  done
fi

if [[ $PROC_HIT -eq 0 ]]; then
  safe "No suspicious processes detected"
fi

# ============================================================================
# 8. SHELL HISTORY — Check if npm install ran recently (informational)
# ============================================================================
section "Checking shell history for recent npm installs (informational)"

for histfile in "$HOME/.bash_history" "$HOME/.zsh_history" "$HOME/.local/share/fish/fish_history"; do
  if [[ -f "$histfile" ]]; then
    RECENT_INSTALLS=$(grep -i "npm install\|npm i \|yarn add\|pnpm add\|bun add\|bun install" "$histfile" 2>/dev/null | tail -20 || true)
    if [[ -n "$RECENT_INSTALLS" ]]; then
      info "Recent install commands from $(basename "$histfile"):"
      echo "$RECENT_INSTALLS" | tail -10 | sed 's/^/    /'
    fi
  fi
done

# ============================================================================
# SUMMARY
# ============================================================================
echo ""
echo -e "${BOLD}========================================${NC}"
echo -e "${BOLD}  SCAN COMPLETE${NC}"
echo -e "${BOLD}========================================${NC}"
echo ""

if [[ $FOUND_ISSUES -gt 0 ]]; then
  echo -e "${RED}${BOLD}⚠️  $FOUND_ISSUES ISSUE(S) FOUND — YOUR SYSTEM MAY BE COMPROMISED${NC}"
  echo ""
  echo -e "${YELLOW}Immediate actions:${NC}"
  echo "  1. Disconnect from the network if stage-2 IOCs were found"
  echo "  2. Remove compromised packages: npm uninstall axios && npm install axios@1.14.0"
  echo "  3. Delete plain-crypto-js from all node_modules"
  echo "  4. Remove stage-2 payloads:"
  echo "     • macOS: sudo rm -f /Library/Caches/com.apple.act.mond"
  echo "     • Linux: rm -f /tmp/ld.py"
  echo "     • Windows: del %PROGRAMDATA%\\wt.exe, %TEMP%\\6202033.*"
  echo "  5. Clean npm cache: npm cache clean --force"
  echo "  6. ROTATE ALL CREDENTIALS — tokens, API keys, SSH keys, passwords"
  echo "  7. Block sfrclak.com at your DNS/firewall"
  echo "  8. Check CI/CD pipelines for the same compromise"
  echo ""
else
  echo -e "${GREEN}${BOLD}✅ No indicators of compromise found.${NC}"
  echo ""
  echo "Preventive recommendations:"
  echo "  • Pin axios to 1.14.0 in your lockfiles"
  echo "  • Run: npm audit"
  echo "  • Consider enabling npm's --ignore-scripts for untrusted installs"
  echo "  • Block sfrclak.com at your DNS/firewall as a precaution"
fi

echo ""
echo "Scanner finished at $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
