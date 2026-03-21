---
name: made
description: MADE decision framework — structured candidate evaluation for non-obvious choices
invocations:
  - /made
  - use made
  - run made
---

You are running the MADE (Multidimensional Adversarial Decision Evaluation) framework. Follow these steps precisely and in order.

---

## Step 1 — Intake

If a decision or problem was provided with the invocation, use it. Otherwise ask:
> "What decision are you trying to make? Describe the problem, any hard constraints, and relevant context."

Wait for the response before proceeding.

---

## Step 2 — Candidate Generation

Generate **at least 5 diverse candidates** for addressing the decision.

Explicitly vary across these axes:
- **Approach type**: build vs buy vs adapt vs delegate vs defer
- **Scale**: minimal/scoped vs full/ambitious
- **Risk profile**: conservative vs aggressive
- **Time horizon**: short-term (days-weeks) vs long-term (months+)

Format each candidate as:

```
**Candidate N: [Short Name]**
Approach: [1-2 sentence description]
Type: [build/buy/adapt/delegate/defer]
Scale: [minimal/moderate/ambitious]
Risk: [low/medium/high]
Time horizon: [short/medium/long]
```

---

## Step 3 — Rubric Design

Generate **at least 8 binary (yes/no) rubrics** for evaluating candidates. Each rubric should be a question answerable with yes or no.

Good rubrics test things like:
- Reversibility ("Can this be undone if it fails?")
- Ownership ("Does this keep the decision in our control?")
- Learning ("Does this generate useful signal quickly?")
- Alignment ("Does this fit existing systems/constraints?")
- Ceiling ("Does this scale to the full need if successful?")
- Cost ("Is the cost acceptable if this fails?")
- Speed ("Can this be completed within the desired time horizon?")
- Dependencies ("Does this avoid blocking external dependencies?")

**Apply the discrimination filter**: for each rubric, score all candidates mentally. If ≥80% of candidates score the same (all yes or all no), **drop the rubric** — it doesn't discriminate. Replace it with a more targeted rubric until you have at least 6 discriminative rubrics (aim for 8).

List the final rubrics:
```
R1: [Rubric question]
R2: [Rubric question]
...
```

---

## Step 4 — Scoring

Score each candidate against each rubric. Use `Y` (yes=1) or `N` (no=0).

Display a scoring table:

```
Candidate          | R1 | R2 | R3 | R4 | R5 | R6 | R7 | R8 | Score
-------------------|----|----|----|----|----|----|----|----|-------
Candidate 1 Name   |  Y |  N |  Y |  Y |  N |  Y |  Y |  N |  5/8
Candidate 2 Name   |  ...
```

Compute **fitness score** = (sum of Y) / (total rubrics). Show as fraction and percentage.

---

## Step 5 — Selection

Select the top-scoring candidate. In case of ties, prefer:
1. Lower risk
2. Shorter time horizon
3. Higher reversibility

State clearly:
```
**Selected: [Candidate Name]**
Score: X/Y rubrics (Z%)
Rationale: [2-3 sentences on why this candidate won and what the key tradeoffs are]
```

---

## Step 6 — Falsifiable Predictions

Generate **3–5 falsifiable predictions** about what will happen if the selected direction is pursued.

Each prediction must:
- Be verifiable (has a clear true/false outcome)
- Have a specific review date (use absolute dates, e.g. "by 2026-06-15")
- State what "success" looks like concretely

Format:
```
P1: [Prediction statement] — verify by [YYYY-MM-DD]
P2: ...
```

---

## Step 7 — Output Actions

Perform all of the following:

### 7a — Save to Engram
Call `save_thought` with:
```
MADE decision: [problem in ≤10 words]. Chose [direction name] because [rationale in ≤20 words]. Score: [X/Y rubrics]. Candidates evaluated: [N].
```

### 7b — Task decomposition in Things
Break the chosen direction into concrete next actions. For each action:
- Call `things_add_todo` with the task title
- Tag: `made-action`
- Set a due date if the task has a natural deadline
- Assign to the Bud area if applicable

Aim for 3–7 tasks that represent the real work of executing the decision.

### 7c — Prediction tasks in Things
For each prediction (P1–P5):
- Call `things_add_todo` with title: "Verify prediction: [prediction statement]"
- Tag: `prediction`
- Due date: the prediction's review date

### 7d — Summary
After completing the above, output:

```
**MADE complete.**

Decision: [problem]
Selected: [candidate name] ([score])

Tasks created: [N] action tasks, [N] prediction tasks
Engram: saved

Next step: [first concrete action from the task list]
```

---

## Guiding principles

- Be adversarial in rubric design — the point is to find reasons candidates fail, not to confirm a preferred answer
- Discrimination matters more than coverage: 6 sharp rubrics beat 12 redundant ones
- Predictions should be uncomfortable to make — if they feel obvious, they're not falsifiable enough
- The framework exists to surface tradeoffs, not to automate the decision. Flag if the top scorer feels wrong and explain why
