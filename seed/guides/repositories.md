# Working with Code Repositories

This guide covers how I work with source code repositories.

## Repository Locations

Repositories live in `~/src/`. When working on a project, I operate within that directory.

Examples:
- `~/src/bud2` - this project
- `~/src/other-project` - other projects

## Making Changes

### Branch Workflow

1. **Check current state**: `git status`, `git branch`
2. **Create feature branch** (for non-trivial changes): `git checkout -b feature/description`
3. **Make changes**: edit files, run tests
4. **Commit**: with clear messages explaining the "why"
5. **Push**: `git push -u origin branch-name`

### When to Branch

- **Direct to main**: Trivial fixes, documentation, small self-contained changes
- **Feature branch + PR**: New features, refactors, anything that benefits from review

## Pull Requests

### Creating PRs

Use `gh pr create` with:
- Clear title describing the change
- Summary of what and why
- Test plan if applicable

### Merging PRs

**IMPORTANT**: I do NOT merge PRs without explicit owner approval.

Workflow:
1. Create PR
2. Notify owner (via talk_to_user)
3. Wait for review/approval
4. Owner merges, or owner grants permission to merge

Even if tests pass and the PR looks good, I wait for human approval before merging.

## Multi-Repository Work

When work spans multiple repositories:
1. Track the overall goal in beads
2. Create separate PRs for each repo
3. Note dependencies between PRs
4. Coordinate merging order with owner

## Commit Messages

Format:
```
type: short description

Longer explanation if needed.

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
```

Types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`

## MCP Server Changes

When modifying MCP server code (e.g., `efficient-notion-mcp`):

1. **Make changes** in the source repo
2. **Commit and push** to remote
3. **Update dependency** in bud2: `go get github.com/vthunder/efficient-notion-mcp@latest`
4. **Rebuild**: `./scripts/build.sh`
5. **Restart bud** - MCP servers are spawned at startup, so changes won't take effect until restart

**Common mistake**: Forgetting step 5. The MCP server binary is rebuilt, but the running instance is still the old code. Always restart after MCP changes.

## Safety

- Never force push to main/master
- Never commit secrets or credentials
- Run tests before committing
- Keep commits atomic and reversible
