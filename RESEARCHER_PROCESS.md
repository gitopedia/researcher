# Researcher Process Flow

This document explains the step-by-step process that the Gitopedia Researcher agent follows, including all LLM calls, iteration counts, and commit points.

## High-Level Flow

The researcher runs in a continuous loop (or once with `--once` flag) and follows this overall structure:

1. **Check for PRs to merge** - Attempts to merge any ready PRs
2. **Get research requests** - Finds GitHub issues with research category labels
3. **Select and process one issue** - Randomly picks an issue and expands the category
4. **Create PR and organize** - Creates a pull request and organizes articles

## Main Loop Iteration

The main loop runs continuously with a configurable interval (default: 60 seconds, set via `LOOP_INTERVAL_SECONDS`). Each iteration:

- Checks for PRs ready to merge
- Processes one research request (if available)
- Sleeps before next iteration

## Per-Issue Processing: Category Expansion

When processing a research request issue, the agent goes through these steps:

### Step 1: Topic Suggestion
- **LLM Call**: `SuggestTopics()` - 1 call
  - Model: `LLM_MODEL_FAST` (default: `qwen3:8b`)
  - Temperature: 0.8
  - Thinking mode: No
  - Purpose: Generate list of missing topics for the category
- **Output**: List of topic candidates (limited by `MAX_TOPICS_PER_RUN`, default: 10)

### Step 2: Branch Creation
- Creates a new branch: `expand/{category}-{timestamp}`
- No LLM calls

### Step 3: Authority Loading
- Loads authority files (people, orgs, places, topics)
- No LLM calls

### Step 4: Topic Processing Loop
For each selected topic, the agent runs through **7 phases** (detailed below).

**Iterations**: Up to `MAX_TOPICS_PER_RUN` topics (default: 10, test mode: 1)

### Step 5: Authority Updates
- Commits any authority file updates (people, orgs, places, topics)
- **Commits**: One commit per authority file that changed
- No LLM calls

### Step 6: Pull Request Creation
- Creates a draft PR with all generated articles
- **Commits**: PR creation itself (articles already committed)
- No LLM calls

### Step 7: Article Organization
- **LLM Call**: `CategorizeArticle()` - 1 call per article
  - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
  - Purpose: Determine which Compendium category the article belongs to
- **Commits**: 
  - One commit per article moved (create at new location)
  - One commit per article deleted from `_incoming/`
  - One commit to delete debug artifacts
- Marks PR as ready for review

## Per-Topic Processing: 7-Phase Pipeline

Each topic goes through 7 phases of processing:

### Phase 1: Foundation Research

**Purpose**: Gather initial sources and create article outline

**Steps**:
1. **Web Search** (no LLM)
   - Generates multiple search queries (default: 5, test mode: 3, configurable via `PHASE1_SEARCH_NUM_QUERIES`)
   - Executes searches via DuckDuckGo
   - Fetches page content in parallel (concurrency: 3, configurable via `SEARCH_MAX_FETCH_CONCURRENCY`)

2. **Source Summarization** (per source)
   - **LLM Call 1**: `SummarizeSource()` - Stage 1 (plain text summary)
     - Model: `LLM_MODEL_FAST` (default: `qwen3:14b`)
     - Temperature: 0.3
     - Thinking mode: Yes (if `LLM_THINK_MODE=true`)
     - Purpose: Generate plain text summary of source content
   - **LLM Call 2**: `SummarizeSource()` - Stage 2 (JSON conversion)
     - Model: `LLM_MODEL_FAST` (default: `qwen3:14b`)
     - Temperature: 0.0
     - Thinking mode: No
     - Purpose: Convert plain text summary to structured JSON with relevance flag
   - **LLM Call 3**: `ExtractEntities()` - 1 call per source
     - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
     - Temperature: 0.0
     - Thinking mode: No (disabled to prevent timeouts)
     - Purpose: Extract entities (people, orgs, places, topics) from source summary
     - Note: For large content (>8K chars), chunks the content and processes each chunk separately
   - **Filtering**: Sources must be relevant and meet minimum word count (`SOURCE_SUMMARY_MIN_WORDS`, default: 200)

**Iterations**: 
- Up to `PHASE1_TARGET_SOURCES` sources (default: 20, test mode: fewer)
- Each source: 3 LLM calls (summarize stage 1, summarize stage 2, extract entities)

**Commits**: 
- One commit per source summary saved to `Compendium/_incoming/sources/`
- One commit for Phase 1 debug artifact (`phase1_sources.json`)

3. **Outline Generation**
   - **LLM Call**: `GenerateOutline()` - 1 call
     - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
     - Temperature: 0.3
     - Thinking mode: No
     - Purpose: Generate structured article outline from collected sources

**Commits**: 
- One commit for outline debug artifact (`outline.json`)

### Phase 2: Gap Analysis

**Purpose**: Identify gaps in research coverage

**LLM Call**: `AnalyzeGaps()` - 1 call
- Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
- Temperature: 0.3
- Thinking mode: No
- Purpose: Analyze outline and sources to identify missing information

**Output**: List of gaps with search queries and suggested sections

**Commits**: 
- One commit for Phase 2 debug artifact (`phase2_gap_analysis.json`)

### Phase 3: Targeted Research (Iterative)

**Purpose**: Fill identified gaps with additional sources

**Iterations**: Up to `MAX_RESEARCH_ROUNDS` rounds (default: 2, test mode: 1, configurable)

**Per Round**:
1. **Targeted Searches** (no LLM)
   - Executes search queries from gap analysis
   - Limits to 5 results per query
   - Fetches and filters content

2. **New Source Processing** (per new source)
   - **LLM Call 1**: `SummarizeSource()` - Stage 1 (plain text)
     - Model: `LLM_MODEL_FAST` (default: `qwen3:14b`)
     - Temperature: 0.3
     - Thinking mode: Yes (if enabled)
   - **LLM Call 2**: `SummarizeSource()` - Stage 2 (JSON conversion)
     - Model: `LLM_MODEL_FAST` (default: `qwen3:14b`)
     - Temperature: 0.0
     - Thinking mode: No
   - Limits to 3 new sources per gap

3. **Re-analysis** (after each round)
   - **LLM Call**: `AnalyzeGaps()` - 1 call per round
     - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
     - Temperature: 0.3
     - Thinking mode: No
     - Purpose: Re-analyze gaps with newly collected sources
   - Loop continues if gaps still exist

**Total LLM Calls**: 
- Up to `MAX_RESEARCH_ROUNDS` × (3 sources per gap × 2 calls + 1 gap analysis call)
- Typically: 2 rounds × (6-12 source calls + 2 gap analysis calls) = 14-26 calls

**Commits**: 
- One commit per new source summary
- One commit for Phase 3 debug artifact (`phase3_targeted_research.json`)

### Phase 4: Section-by-Section Generation

**Purpose**: Generate content for each section using RAG

**Per Section**:
- **LLM Call**: `GenerateSection()` - 1 call per section
  - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
  - Temperature: 0.7
  - Thinking mode: Yes (if enabled)
  - Purpose: Generate section content based on relevant sources
  - Context: Includes previously written sections for coherence

**Per Subsection**:
- **LLM Call**: `GenerateSection()` - 1 call per subsection
  - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
  - Temperature: 0.7
  - Thinking mode: Yes (if enabled)
  - Purpose: Generate subsection content

**Iterations**: 
- One call per section in outline
- One call per subsection in each section
- Typical article: 5-8 sections, 0-3 subsections each = 5-20 calls

**Commits**: 
- One commit per section debug file (`phase4_sections/section-{N}-{slug}.md`)
- One commit per subsection debug file (`phase4_sections/section-{N}.{M}-{slug}.md`)

### Phase 5: Section Discovery

**Purpose**: Discover additional sections from sources

**LLM Call 1**: `DiscoverSections()` - 1 call
- Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
- Temperature: 0.3
- Thinking mode: No
- Purpose: Analyze sources to suggest missing sections

**Per Discovered Section**:
- **LLM Call**: `GenerateSection()` - 1 call per discovered section
  - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
  - Temperature: 0.7
  - Thinking mode: Yes (if enabled)
  - Purpose: Generate content for newly discovered section

**Iterations**: 
- 1 discovery call
- 0-3 discovered sections typically = 1-4 calls total

**Commits**: 
- One commit for Phase 5 debug artifact (`phase5_discovery.json`)

### Phase 6: Integration & Polish

**Purpose**: Polish and integrate all sections into cohesive article

**Per Section**:
- **LLM Call**: `PolishSection()` - 1 call per main section
  - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
  - Temperature: 0.3
  - Thinking mode: Yes (if enabled, but lower temp for stability)
  - Purpose: Polish section with context from previous/next sections

**Per Subsection**:
- **LLM Call**: `PolishSection()` - 1 call per subsection
  - Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
  - Temperature: 0.3
  - Thinking mode: Yes (if enabled)
  - Purpose: Polish subsection with context

**Iterations**: 
- One call per section (same count as Phase 4)
- One call per subsection
- Typical: 5-20 calls total

**Commits**: 
- One commit for Phase 6 debug artifact (`phase6_integrated.md`)

### Phase 7: Citation Addition

**Purpose**: Add inline citation markers to article

**LLM Call**: `AddReferences()` - 1 call (can be disabled via `DISABLE_PHASES=phase7`)
- Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
- Temperature: 0.3
- Thinking mode: Yes (if enabled)
- Purpose: Add citation markers `[^1]`, `[^2]`, etc. throughout article
- Retries: Up to 3 attempts if output is shorter than input
- Note: If disabled, still appends References section manually

**Iterations**: 1 call (or 0 if disabled)

**Commits**: 
- One commit for Phase 7 debug artifact (`phase7_cited.md`)

### Final: Entity Extraction & Article Save

**Purpose**: Extract entities and save final article

**LLM Call**: `ExtractEntities()` - 1 call
- Model: `LLM_MODEL_ARTICLE` (default: `qwen3:32b`)
- Temperature: 0.0
- Thinking mode: No (disabled to prevent timeouts)
- Purpose: Extract all entities from final article
- Note: For large content (>8K chars), chunks and processes separately

**Commits**: 
- One commit for entity extraction debug artifact (`entities.json`)
- One commit for final article (`Compendium/_incoming/{slug}.md`)

## Total LLM Call Count (Per Topic)

For a typical article with:
- 20 Phase 1 sources
- 2 Phase 3 research rounds with 6 new sources
- 6 sections with 2 subsections each
- 2 discovered sections
- Phase 7 enabled

**Total LLM Calls**: Approximately **80-100 calls per topic**

Breakdown:
- Phase 1: 20 sources × 3 calls + 1 outline = **61 calls**
- Phase 2: **1 call**
- Phase 3: 2 rounds × (6 sources × 2 calls + 1 gap analysis) = **26 calls**
- Phase 4: 6 sections + 12 subsections = **18 calls**
- Phase 5: 1 discovery + 2 discovered sections = **3 calls**
- Phase 6: 6 sections + 12 subsections = **18 calls**
- Phase 7: **1 call**
- Final: **1 call**

**Total: ~129 calls** (varies based on article complexity)

## Commit Points

The researcher creates commits at these points:

1. **During Phase 1**: 
   - Each source summary saved (via `CreateFile()`)
   - Phase 1 debug artifact

2. **After Phase 1**: 
   - Outline debug artifact

3. **After Phase 2**: 
   - Gap analysis debug artifact

4. **During Phase 3**: 
   - Each new source summary saved
   - Phase 3 debug artifact

5. **During Phase 4**: 
   - Each section debug file
   - Each subsection debug file

6. **After Phase 5**: 
   - Discovery debug artifact

7. **After Phase 6**: 
   - Integrated article debug artifact

8. **After Phase 7**: 
   - Cited article debug artifact

9. **After Final Processing**: 
   - Entity extraction debug artifact
   - Final article file

10. **After All Topics**: 
    - Authority file updates (one commit per file)

11. **During Organization**: 
    - Each article moved (create at new location)
    - Each article deleted from `_incoming/`
    - Debug artifacts cleanup

**Note**: All file operations use GitHub's API `CreateFile()`, `UpdateFile()`, and `DeleteFile()` methods, which automatically create commits. Each operation is a separate commit.

## Iteration Summary

- **Main Loop**: Continuous (or once with `--once`)
- **Topics per Run**: Up to `MAX_TOPICS_PER_RUN` (default: 10)
- **Phase 3 Rounds**: Up to `MAX_RESEARCH_ROUNDS` (default: 2)
- **Sources per Topic**: Up to `PHASE1_TARGET_SOURCES` (default: 20) + Phase 3 additions
- **Sections per Article**: Variable (typically 5-8)
- **Subsections per Section**: Variable (typically 0-3)

## Model Configuration

Different models are used for different tasks:

| Task | Model | Thinking Mode |
|------|-------|---------------|
| Topic Suggestion | `LLM_MODEL_FAST` (qwen3:8b) | No |
| Source Summarization | `LLM_MODEL_FAST` (qwen3:14b) | Yes |
| Entity Extraction | `LLM_MODEL_ARTICLE` (qwen3:32b) | No |
| Outline Generation | `LLM_MODEL_ARTICLE` (qwen3:32b) | No |
| Gap Analysis | `LLM_MODEL_ARTICLE` (qwen3:32b) | No |
| Section Generation | `LLM_MODEL_ARTICLE` (qwen3:32b) | Yes |
| Section Discovery | `LLM_MODEL_ARTICLE` (qwen3:32b) | No |
| Article Integration | `LLM_MODEL_ARTICLE` (qwen3:32b) | Yes |
| Section Polish | `LLM_MODEL_ARTICLE` (qwen3:32b) | Yes |
| Reference Addition | `LLM_MODEL_ARTICLE` (qwen3:32b) | Yes |
| Article Categorization | `LLM_MODEL_ARTICLE` (qwen3:32b) | No |

## Configuration Variables

Key environment variables that affect iterations and LLM calls:

- `MAX_TOPICS_PER_RUN`: Topics processed per run (default: 10)
- `PHASE1_TARGET_SOURCES`: Initial sources to collect (default: 20)
- `PHASE1_SEARCH_NUM_QUERIES`: Search queries per topic (default: 5)
- `MAX_RESEARCH_ROUNDS`: Gap-filling iterations (default: 2)
- `SOURCES_PER_SECTION`: Sources used per section (default: 8)
- `RUN_PROFILE`: "test" or "prod" (affects multiple defaults)
- `LLM_THINK_MODE`: Enable thinking mode (default: varies)
- `DISABLE_PHASES`: Comma-separated list of phases to skip

