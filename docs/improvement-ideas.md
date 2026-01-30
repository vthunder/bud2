# Bud2 Improvement Ideas

Prioritized list of improvements to make bud's autonomous behavior and memory more effective.
Items #1-3 have been implemented. The rest are future work.

## Implemented

### 1. Better wake-up instructions (seed/wakeup.md)
Created a concrete checklist that gets injected into the wake prompt instead of
the vague "check for pending tasks, review commitments, or do background work."
The checklist tells Claude to: check bud_tasks, check GTD for overdue items,
review recent activity for untracked work, explore an idea, or run housekeeping.

### 2. Raised thinking budget from 30min to 6h/day
The 30min default meant a few real interactions would exhaust the budget and
block all autonomous wakes for the rest of the day. 6h gives plenty of room
for both interactive and autonomous work. Still configurable via
DAILY_THINKING_BUDGET env var.

### 3. Bypass arousal threshold for system wake impulses
System impulses (autonomous wakes, task impulses) now bypass the arousal-based
selection threshold. They already passed the budget gate, so double-gating was
causing them to silently get filtered at idle arousal levels.

---

## Future Work

### 4. Wire in the idea exploration system
**Effort: Low-Medium**

`internal/motivation/ideas.go` has `GenerateImpulses(isIdle)` but it's never
called during the autonomous wake path. Connecting it would give Claude specific
ideas to explore during idle time. The code exists — it just needs to be called
from the autonomous goroutine in main.go alongside `checkTaskImpulses()`.

### 5. Include task list in wake context automatically
**Effort: Low**

Currently Claude has to call `list_bud_tasks` MCP tool during a wake to see
pending tasks. Adding a `## Pending Tasks` section to the wake context bundle
(by calling the task store directly in buildContext) would give Claude immediate
awareness of what needs doing without an extra tool call roundtrip.

### 6. Improve memory retrieval precision
**Effort: Medium**

- Raise the FoK (Feeling of Knowing) threshold from 0.12 to ~0.3
- Raise the similarity seed threshold from 0.3 to ~0.5
- Increase top-N from 10 to 15-20 with relevance explanations
- Use the memory_eval ratings (already collected) to tune retrieval over time
- Consider adding a brief explanation of *why* each memory was retrieved

Current thresholds are very permissive (0.12 FoK means almost anything passes),
leading to noisy context that dilutes useful memories.

### 7. Add a "continue working" loop
**Effort: Medium**

Currently each wake is one prompt → one Claude response → done. If Claude's
response indicates work in progress (didn't call signal_done), the system could
automatically re-prompt with "Continue working on the current task." This would
allow multi-step autonomous work without requiring user interaction between steps.

Implementation: in processItem(), if Claude completes without calling signal_done,
queue a continuation focus item at the same priority.

### 8. Improve consolidation summaries
**Effort: Medium**

The consolidation system uses a single generic template for all memory summaries.
Domain-aware templates would produce more useful traces:
- Code discussion template (preserve file names, decisions, patterns)
- Decision template (preserve options considered, rationale, outcome)
- Task template (preserve commitments, deadlines, blockers)
- Person template (preserve preferences, context, relationship notes)

### 9. Make reconsolidation semantic instead of regex
**Effort: Medium-High**

Correction detection currently uses regex patterns ("actually", "scratch that",
"I was wrong"). Replace with a lightweight LLM call that evaluates whether a new
statement semantically contradicts or updates an existing labile trace. This would
catch subtle corrections like "it turns out the API uses OAuth, not API keys"
without requiring explicit correction language.

### 10. Add memory retrieval explanations
**Effort: Low-Medium**

When memories are injected into Claude's context, include a brief note about why
each was retrieved (e.g., "activated by similarity to 'project planning'" or
"entity match: ProjectX"). This gives Claude metacognitive context about its own
memory retrieval, allowing it to judge relevance rather than treating all
retrieved memories as equally important.
