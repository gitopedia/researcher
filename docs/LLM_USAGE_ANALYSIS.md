# LLM Usage Analysis and Optimization Recommendations

## Current LLM Usage Flow

The Researcher agent uses LLMs in a multi-stage pipeline to generate encyclopedia articles. Currently, **all tasks use the same model** (configured via `LLM_MODEL`, default: `qwen3:14b`).

### Stage 1: Topic Suggestion (Optional - Category Expansion)

**When:** When processing a "research category" issue to expand coverage.

**Task Complexity:** **LOW** - Simple list generation
- **Input:** Category name + list of existing topics
- **Output:** JSON array of 20-30 topic suggestions
- **Prompt:** `suggest_topics_user.txt`
- **Temperature:** 0.8 (creative)
- **Current Model:** Same as all tasks (qwen3:14b)

**Recommendation:** ✅ **Use small model (7B-14B)**
- This is a straightforward task requiring basic knowledge
- No complex reasoning needed
- Fast response time is more important than depth

---

### Stage 2: Source Research & Summarization

**When:** For each topic, the agent searches the web and processes 20-30 sources.

**Current Approach:** **Code-based** (default) - No LLM used!
- Uses `PreFilterContent()`, `FormatContent()`, `ExtractTopicsFromContent()`
- Fast, deterministic, no content loss
- ✅ **Already optimized!**

**Legacy LLM Approach** (if `USE_LLM_SUMMARIZATION=true`):

#### 2a. Source Summarization (Step 1)
**Task Complexity:** **MEDIUM-HIGH** - Content extraction and compression
- **Input:** Full web page content (up to 200k chars)
- **Output:** Plain text summary (1200-2000 words) or "NOT_RELEVANT"
- **Prompt:** `phase_1_step_1_summarize_source_user.txt`
- **Temperature:** 0.3 (focused)
- **Current Model:** Same as all tasks

**Recommendation:** ⚠️ **Use medium model (14B-32B)**
- Needs to understand context and extract key information
- Must filter out junk while preserving useful content
- Large input context window required

#### 2b. JSON Conversion (Step 2)
**Task Complexity:** **LOW** - Simple format conversion
- **Input:** Plain text summary from Step 1
- **Output:** JSON with `{relevant, reason, summary, language, topics}`
- **Prompt:** `phase_1_step_2_convert_summary_user.txt`
- **Temperature:** 0.0 (deterministic)
- **Current Model:** Same as all tasks

**Recommendation:** ✅ **Use small model (7B-14B)**
- Pure format conversion task
- No reasoning required
- Could even use a structured output model or regex

---

### Stage 3: Entity Extraction

**When:** After generating article, extract entities for knowledge base.

**Task Complexity:** **MEDIUM** - Named entity recognition with disambiguation
- **Input:** Full article content (~2000-3000 words)
- **Output:** JSON array of entities with types (person, org, place, topic)
- **Prompt:** `extract_entities_user.txt`
- **Temperature:** 0.0 (deterministic)
- **Current Model:** Same as all tasks

**Recommendation:** ⚠️ **Use medium model (14B-32B)**
- Needs to identify entities and add disambiguation qualifiers
- Requires understanding context to avoid false positives
- Accuracy is important for knowledge base quality

**Alternative:** Consider using a specialized NER model (e.g., spaCy + LLM for disambiguation)

---

### Stage 4: Article Generation

**When:** After collecting 20-30 source summaries, generate the final article.

**Task Complexity:** **VERY HIGH** - Complex synthesis and writing
- **Input:** 20-30 source summaries (~40k-60k tokens of context)
- **Output:** 1500-2500 word encyclopedia article with proper structure
- **Prompt:** `generate_article_user.txt`
- **Temperature:** 0.7 (balanced creativity)
- **Current Model:** Same as all tasks

**Requirements:**
- Synthesize information from multiple sources
- Write coherent, well-structured prose
- Follow strict formatting (YAML frontmatter, markdown, citations)
- Maintain factual accuracy
- Create logical flow between sections

**Recommendation:** ✅ **Use large model (32B+)**
- This is the most complex task requiring deep understanding
- Needs strong reasoning and synthesis capabilities
- Quality directly impacts article quality
- Worth the computational cost

---

## Current Model Configuration

**Single Model for All Tasks:**
```env
LLM_MODEL=qwen3:14b  # Used for everything
```

**Problems:**
1. **Overkill for simple tasks** - Using 14B model for JSON conversion wastes compute
2. **Underpowered for complex tasks** - 14B may struggle with article synthesis
3. **No optimization** - Can't use faster models where quality isn't critical

---

## Recommended Optimization Strategy

### Multi-Model Configuration

```env
# Small models for simple tasks
LLM_MODEL_FAST=qwen3:7b              # Topic suggestions, JSON conversion
LLM_MODEL_ENTITY=qwen3:14b           # Entity extraction

# Medium model for summarization (if using LLM approach)
LLM_MODEL_SUMMARIZE=qwen3:14b

# Large model for article generation
LLM_MODEL_ARTICLE=qwen3:32b          # Or gemma3:27b, llama3:70b
```

### Task Breakdown Recommendations

#### 1. **Topic Suggestion** → Small Model (7B)
- **Current:** Single prompt, single model call
- **Optimization:** Already simple, just use smaller model
- **Speedup:** 2-3x faster

#### 2. **Source Summarization** → Keep Code-Based (Current)
- **Current:** Code-based (no LLM) ✅
- **Optimization:** Already optimal!
- **Alternative:** If switching to LLM, use 14B model

#### 3. **JSON Conversion** → Small Model (7B) or Structured Output
- **Current:** LLM call with JSON prompt
- **Optimization Options:**
  - **Option A:** Use 7B model (simple format conversion)
  - **Option B:** Use structured output API (if available)
  - **Option C:** Regex-based extraction (fastest, but less robust)
- **Speedup:** 2-3x faster

#### 4. **Entity Extraction** → Medium Model (14B) or Hybrid
- **Current:** Single LLM call for all entities
- **Optimization Options:**
  - **Option A:** Use 14B model (current is fine)
  - **Option B:** Two-stage approach:
    1. Use NER library (spaCy, etc.) to find candidate entities
    2. Use small LLM (7B) to add disambiguation qualifiers
  - **Option C:** Extract by type separately (person, org, place, topic) with smaller models
- **Speedup:** 1.5-2x faster with hybrid approach

#### 5. **Article Generation** → Large Model (32B+)
- **Current:** Single large prompt with all sources
- **Optimization Options:**
  - **Option A:** Use 32B+ model (recommended)
  - **Option B:** Two-stage approach:
    1. **Stage 1 (14B):** Generate article outline/sections from sources
    2. **Stage 2 (32B):** Expand each section into full prose
  - **Option C:** Section-by-section generation:
    1. Use 14B to identify key sections needed
    2. Use 32B to write each section independently
    3. Use 14B to combine and ensure coherence
- **Speedup:** Option B/C could be 1.5-2x faster, but may reduce coherence

---

## Detailed Task Breakdown for Article Generation

### Current Approach (Single Large Model)
```
Input: 20-30 source summaries (40k-60k tokens)
↓
Single LLM call (32B model)
↓
Output: Complete 2000-word article
```

**Complexity:** Very high - model must:
- Understand all sources simultaneously
- Synthesize information
- Plan article structure
- Write coherent prose
- Format correctly

### Recommended: Hierarchical Breakdown

#### Option 1: Outline-First Approach
```
Step 1 (14B model): Generate article outline
  Input: Source summaries
  Output: Structured outline with key points per section
  
Step 2 (32B model): Expand each section
  Input: Outline + relevant sources for section
  Output: Full prose for each section
  
Step 3 (14B model): Final coherence check
  Input: All sections
  Output: Minor edits for flow/consistency
```

**Benefits:**
- Smaller context windows per step
- Can parallelize section writing
- Easier to debug/improve individual sections
- Faster overall (parallelization)

**Drawbacks:**
- May lose some cross-section coherence
- More complex orchestration

#### Option 2: Section-by-Section with Context
```
For each required section:
  Step 1 (14B): Identify relevant sources for this section
  Step 2 (32B): Write section using identified sources
  Step 3 (14B): Check section quality and relevance
  
Final Step (14B): Combine sections, ensure flow
```

**Benefits:**
- Very focused context per section
- Can use smaller models for filtering/combining
- Easy to retry individual sections

**Drawbacks:**
- Sequential processing (slower)
- May miss cross-section connections

#### Option 3: Hybrid - Chunk Sources, Generate Sections
```
Step 1 (14B): Group sources by theme/section
Step 2 (14B): Generate section outlines from grouped sources
Step 3 (32B): Write sections in parallel (one per section)
Step 4 (14B): Combine and ensure coherence
```

**Benefits:**
- Best of both worlds
- Parallel section writing
- Focused context per section
- Maintains coherence

**Recommended:** ✅ **Option 3 (Hybrid)**

---

## Implementation Recommendations

### Phase 1: Quick Wins (Low Effort, High Impact)

1. **Separate models for simple tasks:**
   ```go
   modelSuggestTopics := "qwen3:7b"
   modelSummarizeJSON := "qwen3:7b"  // If using LLM approach
   ```

2. **Use large model only for article generation:**
   ```go
   modelGenerateArticle := "qwen3:32b"
   ```

3. **Keep entity extraction at 14B** (good balance)

**Expected Speedup:** 2-3x for topic suggestion, 1.5-2x overall

### Phase 2: Medium Effort (Break Down Article Generation)

1. **Implement Option 3 (Hybrid approach):**
   - Add `GenerateArticleOutline()` using 14B model
   - Add `GenerateSection()` using 32B model (parallelizable)
   - Add `CombineSections()` using 14B model

2. **Parallelize section generation:**
   - Generate multiple sections concurrently
   - Use goroutines with semaphore for rate limiting

**Expected Speedup:** 2-3x for article generation, 1.5-2x overall

### Phase 3: Advanced (Hybrid NER)

1. **Two-stage entity extraction:**
   - Stage 1: Use spaCy/transformers for candidate extraction
   - Stage 2: Use 7B model for disambiguation qualifiers

**Expected Speedup:** 1.5-2x for entity extraction

---

## Complexity Analysis Summary

| Task | Current Complexity | Recommended Model | Speedup Potential |
|------|-------------------|-------------------|-------------------|
| Topic Suggestion | LOW | 7B | 2-3x |
| Source Summarization | N/A (code-based) | N/A | Already optimal |
| JSON Conversion | LOW | 7B | 2-3x |
| Entity Extraction | MEDIUM | 14B (or hybrid) | 1.5-2x |
| Article Generation | VERY HIGH | 32B+ (or hierarchical) | 1.5-3x |

**Overall Expected Speedup:** 2-3x with multi-model approach, 3-5x with hierarchical breakdown

---

## Code Changes Required

### Minimal Changes (Phase 1)
1. Add environment variables for model selection:
   ```env
   LLM_MODEL_FAST=qwen3:7b
   LLM_MODEL_ARTICLE=qwen3:32b
   ```

2. Update `internal/llm/client.go` to use different models per task

3. Update `NewClient()` to read separate model configs

### Medium Changes (Phase 2)
1. Add new methods to `Generator` interface:
   - `GenerateArticleOutline(ctx, topic, contextData) (Outline, error)`
   - `GenerateSection(ctx, section, sources) (string, error)`
   - `CombineSections(ctx, sections) (string, error)`

2. Refactor `processTopic()` to use hierarchical approach

3. Add parallel section generation with goroutines

### Advanced Changes (Phase 3)
1. Integrate NER library (spaCy via Python subprocess or Go library)
2. Implement two-stage entity extraction
3. Add caching for entity disambiguation

---

## Cost/Performance Trade-offs

**Current (Single 14B Model):**
- Cost: Medium
- Speed: Medium
- Quality: Good for most tasks, may struggle with complex articles

**Recommended (Multi-Model):**
- Cost: Similar (7B + 14B + 32B ≈ 14B for all tasks, but faster)
- Speed: 2-3x faster
- Quality: Better (32B for articles, appropriate models for each task)

**Hierarchical (Phase 2):**
- Cost: Similar
- Speed: 3-5x faster (parallelization)
- Quality: Similar or better (more focused context per step)

