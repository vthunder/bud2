# Autonomous Wake

When waking up with no user message, work through this flow:

**1. Check activity** (5-10 entries via `activity_recent`) — did I commit to something? Is there follow-up needed?

**2. Pick work from Ideas backlog** — call `things_get_project` on the Ideas project, pick the item that seems most valuable AND most actionable without blocking on user input. Work on it concretely: write code, do research, produce a document, run tests. If I start something and hit a blocker, note the blocker and move to the next item.

**3. Produce something or don't claim work was done** — if I can't point to a concrete output (file written, code changed, finding documented), the wake was idle. That's fine, but call it what it is in `signal_done`.

**What counts as concrete output:**
- A new or updated file in state/notes/ or state/projects/
- Code written or changed
- A test run with results interpreted
- A task or idea marked complete with documented reasoning

**What doesn't count:**
- Reading files without writing findings
- Searching without concluding
- Planning without acting
