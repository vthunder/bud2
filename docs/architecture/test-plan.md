# Memory System v2 Test Plan

**Created**: 2026-01-13
**Purpose**: Verify the new memory architecture works as designed

---

## Summary of What's Implemented

All major components from the architecture have code:

| Package | Status | Key Files |
|---------|--------|-----------|
| `buffer` | ✅ Implemented | buffer.go, types.go, summarizer.go |
| `filter` | ✅ Implemented | entropy.go, dialogueact.go |
| `graph` | ✅ Implemented | db.go, activation.go, episodes.go, traces.go, entities.go |
| `focus` | ✅ Implemented | attention.go, queue.go, types.go |
| `extract` | ✅ Implemented | fast.go, deep.go, resolve.go |
| `metacog` | ✅ Implemented | patterns.go, compiler.go, reflection.go |

---

## Test Categories

### 1. Unit Tests (Package-Level)

These test individual components in isolation.

#### 1.1 Conversation Buffer (`internal/buffer/`)

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestAdd_CreatesBuffer` | Add entry to non-existent scope | Creates new buffer with entry |
| `TestAdd_TokenCounting` | Add multiple entries | Token count accumulates correctly |
| `TestGet_ByScope` | Retrieve buffer by channel ID | Returns correct buffer |
| `TestGetContext_FormatOutput` | Get context string | Returns summary + formatted recent messages |
| `TestFindReplyContext` | Find what a message replies to | Returns correct parent message |
| `TestCompress_TokenThreshold` | Exceed token limit | Triggers compression, summary created |
| `TestCompress_AgeThreshold` | Message older than 10min | Triggers compression |
| `TestCompress_WithoutSummarizer` | Compression without summarizer | Trims to last 10 entries |
| `TestPersistence` | Save and Load | State survives restart |

#### 1.2 Quality Filter (`internal/filter/`)

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestClassifyDialogueAct_Backchannel` | "yes", "ok", "👍" | Returns `ActBackchannel` |
| `TestClassifyDialogueAct_Question` | "What time is it?" | Returns `ActQuestion` |
| `TestClassifyDialogueAct_Command` | "Please run the build" | Returns `ActCommand` |
| `TestClassifyDialogueAct_Greeting` | "hi", "good morning" | Returns `ActGreeting` |
| `TestClassifyDialogueAct_Statement` | Normal text | Returns `ActStatement` |
| `TestIsLowInfo` | Backchannels and greetings | Returns true |
| `TestShouldAttachToPrevious` | Short responses | Returns true for backchannels |
| `TestEntropy_HighNovelty` | Text with many proper nouns | High entity novelty score |
| `TestEntropy_LowNovelty` | "yes ok thanks" | Low entity novelty score |
| `TestEntropy_SemanticDivergence` | Novel vs repetitive text | Divergence correctly computed |
| `TestEntropy_Threshold` | Score vs threshold | PassesThreshold correct |
| `TestShouldCreateEpisode` | Various inputs | Correct filter decisions |

#### 1.3 Memory Graph (`internal/graph/`)

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestCreateEpisode` | Create episode node | Episode saved with all fields |
| `TestCreateEntity` | Create entity node | Entity saved with type/salience |
| `TestCreateTrace` | Create trace node | Trace saved, embedding stored |
| `TestAddEdge` | Create relationship | Edge with correct type/weight |
| `TestGetTraceNeighbors` | Get connected traces | Returns neighbors with weights |
| `TestSpreadActivation_SingleHop` | Activation from one seed | Spreads to neighbors |
| `TestSpreadActivation_MultiHop` | Activation across 3 iterations | Reaches distant nodes |
| `TestSpreadActivation_Decay` | Activation decay | Decreases over hops |
| `TestLateralInhibition` | Weak activations | Filtered out |
| `TestFeelingOfKnowing` | Low max activation | Returns empty result |
| `TestRetrieve_Integration` | Full retrieval flow | Returns ranked traces |
| `TestBiTemporalTracking` | T vs T' timestamps | Both stored correctly |

#### 1.4 Focus/Attention (`internal/focus/`)

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestAddPending` | Add items to queue | Items stored with salience |
| `TestSelectNext_P0Wins` | P0 vs P1 items | P0 always selected |
| `TestSelectNext_UserInputWins` | User input vs tasks | User input selected |
| `TestSelectNext_HighestSalience` | Equal priority items | Highest salience wins |
| `TestFocus_SuspendsCurrent` | Focus on new item | Current pushed to suspended |
| `TestComplete_ResumesSuspended` | Complete current | Pops from suspended |
| `TestSetMode_BypassReflex` | Set attention mode | Mode stored and active |
| `TestIsAttending` | Check domain attention | Correct mode matching |
| `TestModeExpiration` | Expired mode | Not returned by IsAttending |
| `TestArousalAdjustment` | High priority input | Arousal increases |
| `TestArousalDecay` | Call DecayArousal | Arousal decreases |
| `TestSelectionThreshold` | High vs low arousal | Threshold varies correctly |

#### 1.5 Entity Extraction (`internal/extract/`)

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestFastExtract_Mentions` | "@username" | Extracts person entity |
| `TestFastExtract_Times` | "3:30pm", "tomorrow" | Extracts time entities |
| `TestFastExtract_Capitalized` | "Meet with John" | Extracts "John" |
| `TestFastExtract_SkipCommon` | "I think The answer" | Skips "I", "The" |
| `TestFastExtract_Deduplication` | Same entity twice | Returns once |

#### 1.6 Metacognition (`internal/metacog/`)

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestRecord_NewPattern` | First occurrence | Pattern created |
| `TestRecord_Repetition` | Same input again | Occurrence count increases |
| `TestRecord_Correction` | Response corrected | Success rate decreases |
| `TestGetCandidates_MinReps` | < 3 occurrences | Not a candidate |
| `TestGetCandidates_SuccessRate` | < 100% success | Not a candidate |
| `TestGetCandidates_Valid` | 3+ reps, 100% success | Returns as candidate |
| `TestMarkProposed` | Mark pattern | IsProposed = true |
| `TestMarkRejected` | Reject pattern | IsRejected = true |
| `TestPrune_OldPatterns` | Pattern > 7 days old | Removed |

---

### 2. Integration Tests

These test components working together.

#### 2.1 Buffer + Filter Integration

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestBufferWithDialogueActs` | Add entries with act classification | Acts stored in buffer |
| `TestBufferFiltering` | Low-info messages | Stay in buffer but no episode |
| `TestReplyChainWithBackchannel` | "yes" after question | Question found as reply context |

#### 2.2 Graph + Activation Integration

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestTraceToEntityLinks` | Create trace with entities | Edges created |
| `TestEpisodeToTraceConsolidation` | Episodes → trace | SOURCED_FROM edges |
| `TestRetrievalWithEntities` | Query with entity mention | Entity-related traces boosted |

#### 2.3 Focus + Reflex Integration

| Test | Description | Expected Behavior |
|------|-------------|-------------------|
| `TestReflexBypass` | Mode set, reflex triggered | Reflex skipped |
| `TestReflexNotBypassed` | No mode, reflex triggered | Reflex fires |

---

### 3. Scenario Tests (End-to-End)

These test the original problems from memory-research.md.

#### 3.1 The "Yes" Problem

**Scenario**: User asks a question, user responds "yes"

```
[10:00] Bud: Should I proceed with the deployment?
[10:01] User: yes
```

**Test Steps**:
1. Add Bud's question to buffer
2. Add user's "yes" to buffer
3. Classify "yes" → should be `ActBackchannel`
4. Check entropy filter → should NOT create episode for "yes" alone
5. Get context for channel → should show both messages together
6. Find reply context for "yes" → should return the question

**Expected**: "yes" stays with its question, executive sees full context.

#### 3.2 The Interruption Problem

**Scenario**: User messaging, then scheduled reminder fires

```
[10:00] User: Can you help me with...
[10:01] [Reminder: Meeting in 15 min]
[10:01] User: ...this code review?
```

**Test Steps**:
1. Add user message to pending (P1)
2. Add reminder impulse to pending (P0)
3. SelectNext → should return reminder (P0 wins)
4. Focus on reminder, handle it
5. Complete, SelectNext → should return user message
6. Buffer still has all messages in order

**Expected**: P0 preempts, then returns to P1, no context loss.

#### 3.3 The Low-Info Pollution Problem

**Scenario**: Series of messages with varying information content

```
[10:00] User: I'm working on the Bud memory system refactor
[10:01] User: ok
[10:02] User: 👍
[10:03] User: The three-tier graph uses spreading activation
```

**Test Steps**:
1. Score each message with entropy filter
2. Message 1: High entity novelty ("Bud", "memory system") → HIGH score
3. Message 2: "ok" → LOW score
4. Message 3: "👍" → LOW score
5. Message 4: High novelty → HIGH score
6. Only messages 1 and 4 should create episodes
7. All messages stay in buffer for context

**Expected**: Low-info filtered from memory, but present in buffer.

#### 3.4 The Memory Retrieval Problem

**Scenario**: Query that requires multi-hop reasoning

Setup: Create traces:
- "John works on Project Alpha"
- "Project Alpha uses React"
- "React components need testing"

**Query**: "What does John need to do?"

**Test Steps**:
1. Seed activation from query embedding
2. Spread activation through graph
3. "John" → "Project Alpha" → "React" → "testing"
4. Check if "testing" gets activated through chain

**Expected**: Multi-hop reasoning via spreading activation.

#### 3.5 The "Feeling of Knowing" Test

**Scenario**: Query about unknown topic

**Query**: "What's the status of Project Omega?" (never mentioned)

**Test Steps**:
1. Attempt retrieval
2. Find similar traces → none or very low similarity
3. Max activation < 0.12 threshold
4. Return empty/minimal result

**Expected**: Don't hallucinate - reject low-confidence queries.

#### 3.6 The Knowledge Compilation Flow

**Scenario**: Repeated greeting pattern

```
Occurrence 1: "good morning" → "Good morning! How can I help?"
Occurrence 2: "good morning" → "Good morning! How can I help?"
Occurrence 3: "good morning" → "Good morning! How can I help?"
```

**Test Steps**:
1. Record each occurrence in pattern detector
2. After 3rd, check GetCandidates → should return pattern
3. Mark as proposed
4. Verify pattern has correct structure for reflex generation

**Expected**: 3+ successful repetitions → reflex candidate.

---

### 4. Performance Tests

| Test | Description | Target |
|------|-------------|--------|
| Buffer add/get latency | Single operation | < 1ms |
| Entropy filter scoring | Per message | < 10ms |
| Spreading activation (1k nodes) | 3 iterations | < 100ms |
| Fast entity extraction | Per message | < 5ms |
| Dialogue act classification | Per message | < 1ms |

---

### 5. Edge Cases

| Test | Description | Expected |
|------|-------------|----------|
| Empty message | "" | Classified as backchannel |
| Very long message | > 10k chars | Handled without OOM |
| Unicode/emoji only | "🎉🎊🎈" | Classified correctly |
| Multiple scopes | Many channels | Independent buffers |
| Graph with no edges | Isolated nodes | Activation stays local |
| All P4 items | Only low-priority | Selected when nothing else |
| Mode for "all" domains | Wildcard attention | Bypasses all reflexes |

---

## Running Tests

```bash
# Run all new package tests
go test ./internal/buffer/... ./internal/filter/... ./internal/graph/... ./internal/focus/... ./internal/extract/... ./internal/metacog/...

# Run with verbose output
go test -v ./internal/...

# Run specific scenario test
go test -v -run TestYesProblem ./internal/integration_test.go
```

---

## Test Data / Fixtures

For consistent testing, create fixtures:

1. **Sample conversations**: Realistic Discord exchanges
2. **Entity-rich text**: Text with known entities for extraction testing
3. **Pre-computed embeddings**: For deterministic similarity tests
4. **Graph snapshots**: Pre-built graphs for activation testing

---

## Questions / Gaps

1. **Integration with existing code**: How do the new packages connect to the existing executive/reflex system?
2. **Migration path**: How do existing traces/percepts migrate to the new graph structure?
3. **Embedder dependency**: Tests need a mock embedder - should we use fixed vectors or a test implementation?
4. **Summarizer dependency**: Buffer compression needs a summarizer - how should this be mocked?

---

## Next Steps

1. [ ] Create test fixtures
2. [ ] Implement unit tests for each package
3. [ ] Implement integration tests
4. [ ] Run scenario tests manually to verify behavior
5. [ ] Add performance benchmarks
