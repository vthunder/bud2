# Jobs Guide

Jobs are parameterized prompt templates for subagent tasks you run repeatedly. Unlike skills (which guide the current session) or profiles (which configure subagent capabilities), jobs define *what* a subagent should do — with named parameters filled in at spawn time.

## Jobs vs Skills vs Profiles

| | Jobs | Skills | Profiles |
|---|---|---|---|
| **What** | Parameterized task templates | In-session prompt guides | Subagent capability configs |
| **Who uses** | Executive (spawn_subagent) | Current session | Subagent runtime |
| **Params** | Yes, typed and validated | No | No |
| **Reuse** | High — same task, different inputs | Medium | High |

Use a job when you'd write the same subagent prompt twice with minor variations. Use a skill when you need to guide your own current session. Profiles are infrastructure — you don't usually create them per-task.

## Namespacing

Jobs live in two places:

- **Global jobs**: `seed/jobs/` → seeded to `state/system/jobs/` at startup. Available to all projects.
- **Project jobs**: `state/projects/<project>/jobs/`. Specific to that project.

When referencing a job:
- `job="disk-cleanup"` → global job (`state/system/jobs/disk-cleanup.md`)
- `job="project/sandmill/disk-cleanup"` → sandmill project job

## Discovering Available Jobs

Use `list_jobs` to see what's available:

```
list_jobs()                      # global jobs only
list_jobs(project="sandmill")    # global + sandmill jobs, each tagged with source
```

Each result includes the job's `name`, `description`, `params`, and source.

## Invoking a Job

Pass `job` and `params` to `spawn_subagent`:

```
spawn_subagent(job="disk-cleanup", params={"disk_name": "Mac OS 8.0 HD"})
spawn_subagent(job="git-commit", params={"path": "~/src/sandmill", "message": "Fix COOP headers"})
spawn_subagent(job="project/sandmill/observe", params={})
```

Required params must be provided. Optional params use their default value if omitted. The executor will error if a required param is missing.

## Writing a New Job

Job files are Markdown with YAML frontmatter. Global jobs go in `seed/jobs/`, project-specific jobs in `state/projects/<project>/jobs/`.

```yaml
---
name: job-name
description: One-line description of what this job does
profile: coder
params:
  - name: param_name
    required: true
    description: What this param is
  - name: optional_param
    required: false
    default: some-default-value
    description: What this param is
---
The prompt body goes here. Reference params with {{param_name}}.

All standard Markdown. The subagent receives this as its task prompt
with params substituted before dispatch.
```

### Template syntax

Params are substituted with `{{param_name}}`. Every occurrence is replaced. No logic or conditionals — keep it simple. If you need conditional behavior, document it in the prompt ("if X is empty, skip this step").

### Choosing a profile

- `coder` — file editing, code execution, full tool access
- `researcher` — web search, reading, no file writes
- `writer` — documentation and file writing tasks
- Omit `profile` to use the default

## When to Use a Job

- You've written the same subagent prompt twice → extract it
- A recurring maintenance task (cleanup, commit, deploy step)
- A task that operates on different inputs each time (disk name, repo path, URL)
- A task you want to hand off quickly without re-explaining it

Don't create a job for a one-off task or a task so specific it'll never recur. Inline the prompt in that case.
