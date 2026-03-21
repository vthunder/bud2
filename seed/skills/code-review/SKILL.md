---
name: code-review
description: Structured code review guidance
---

When reviewing code:

- Read the full diff/file before commenting — don't react to the first thing you see
- Organize feedback by priority: correctness bugs > security issues > performance > style
- Be specific: quote the exact line, explain why it's a problem, suggest a fix
- Distinguish blocking issues from suggestions (use "Must fix:" vs "Consider:")
- Note what's done well — a purely negative review is less useful
- Check for: error handling, edge cases, resource leaks, input validation, test coverage
- Don't nitpick style if a linter/formatter handles it
