---
name: voice
description: "Interview the author to extract their authentic voice, then rewrite a blog post to match it. Use when a draft feels too polished, generic, or not quite right. Trigger phrases: /voice, voice interview, rewrite this post, sounds like a press release, doesn't sound like me."
user-invocable: true
---

# Voice Interview

Extracts the author's authentic voice via targeted interview questions, then rewrites an existing draft to match it.

The problem this solves: drafts written with AI help (or written quickly) often sound like tech writing — structured, complete, and wrong. The real version is messier and more interesting.

---

## When to Use

Invoke when:
- A draft exists but "doesn't sound like me"
- The post is too polished, too structured, or too promotional
- Something feels off but it's hard to say what

---

## Step 1: Find the Draft

Parse the invocation for a file path, slug, or post title.

- If a file path or slug is provided, read it directly.
- If no post is specified, ask: "Which post? (file path or slug)"
- Read the draft. Identify the key claims, technical content, and any phrases that feel off.

---

## Step 2: The Interview

Call `talk_to_user` with **five questions**, all at once. Do not ask them one at a time.

```
Before I rewrite this, I need five quick answers:

1. Describe this project to someone at a bar — what do you actually say? Not the polished version. What comes out naturally?

2. Why did you *really* build this? There's usually a real reason underneath the stated one.

3. What's the most technically interesting thing you did? The part you'd explain to another engineer because it was genuinely hard or surprising?

4. What do you hate about how it reads right now? Name one specific thing.

5. Find the sentence that sounds least like you. Paste it here.
```

Wait for the answers before proceeding.

---

## Step 3: Voice Analysis

Read the answers carefully. Extract:

- **Actual lead**: What the person says first at the bar is usually the real lede. Compare to where the post starts.
- **Real motivation**: The actual reason often differs from the stated one. Use the real one.
- **Technical core**: Whatever they're most proud of technically deserves more space than it currently gets.
- **Problem phrases**: The sentence they identify (and anything that sounds like it) should be cut or rewritten.
- **Tone signals**: How they write in the interview answers is how the post should sound. Short? Use short. Casual? Use casual. Self-deprecating? Keep it.

---

## Step 4: Rewrite

Rewrite the post. Rules:

- **Lead with the interesting thing**, not context-setting. Cut any preamble that exists to orient the reader rather than hook them.
- **Write for nerds.** Assume the reader is technically competent. Don't apologize for depth.
- **Architecture over feelings.** Technical decisions and tradeoffs are the substance. Reactions ("I was surprised", "what I learned") are decoration — cut or bury them.
- **No section headers that are vibes.** "What Surprised Me", "Lessons Learned", "Key Takeaways" → cut the section or rename it to something concrete.
- **Specificity over generality.** Replace "I learned a lot about networking" with what you actually learned.
- **No standalone hype sentences.** "The result is pretty amazing" as its own paragraph → cut it or merge it.
- **Passive constructions signal distance.** If the author says "it was decided to use X", that's a sign they're not owning the decision. Rewrite to "I used X because Y."

Save the rewrite to the original file, replacing the draft. Keep a summary of the major changes made.

---

## Step 5: Report

Call `talk_to_user` with:
- What changed and why (2-4 bullets, concrete)
- Anything that felt ambiguous or where you made a judgment call
- End with: "Give it a read and tell me what feels wrong."

Do not summarize the whole post. The user is about to read it.

---

## Style Reference: What Good Looks Like

Good:
> I love old computers. Not just using them — hunting for them on eBay, fixing broken electronics, restoring yellowed plastic.

Bad:
> As a retro computing enthusiast, I've always been passionate about preserving digital history.

Good:
> The gateway decodes enough of HTTP that it can use the browser's built-in `fetch()` to transparently proxy the content.

Bad:
> I implemented a sophisticated proxy solution that handles modern web compatibility challenges.

Good:
> It adds up. *(after listing computers)*

Bad:
> My collection has grown significantly over the years, reflecting my deep interest in computing history.

The good versions are specific, un-hedged, and would survive being said out loud. The bad versions are marketing.
