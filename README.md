# Gitopedia Researcher

The Researcher is an AI-powered agent that automatically creates encyclopedia articles for Gitopedia using a **multi-phase generation pipeline**. It monitors GitHub issues for research requests, gathers information from web sources, and generates well-sourced, comprehensive Markdown articles.

## Multi-Phase Architecture

The researcher builds articles iteratively through 7 phases, allowing for deeper research and higher quality output:

```mermaid
flowchart TB
    subgraph Phase1["Phase 1: Foundation Research"]
        Search["Web Search<br/>(DuckDuckGo)"]
        Fetch["Fetch Pages<br/>(Headless Chrome)"]
        Summarize["Summarize Sources<br/>(qwen3:14b)"]
        Sources["Source Summaries"]
    end

    subgraph Phase2["Phase 2: Outline & Gap Analysis"]
        Outline["Generate Outline<br/>(qwen3:32b)"]
        Gaps["Identify Gaps<br/>(qwen3:14b)"]
    end

    subgraph Phase3["Phase 3: Targeted Research"]
        TargetSearch["Targeted Searches"]
        NewSources["Additional Sources"]
    end

    subgraph Phase4["Phase 4: Section Generation"]
        Section1["Section 1"]
        Section2["Section 2"]
        SectionN["Section N"]
    end

    subgraph Phase5["Phase 5: Section Discovery"]
        Discover["Suggest New Sections<br/>(qwen3:14b)"]
        NewSections["Generate New Sections"]
    end

    subgraph Phase6["Phase 6: Integration"]
        Integrate["Polish & Integrate<br/>(qwen3:32b)"]
    end

    subgraph Phase7["Phase 7: Citations"]
        AddRefs["Add References<br/>(qwen3:14b)"]
        Final["Final Article"]
    end

    Search --> Fetch --> Summarize --> Sources
    Sources --> Outline --> Gaps
    Gaps -->|"Has gaps"| TargetSearch --> NewSources
    NewSources -->|"Re-analyze"| Gaps
    Gaps -->|"No gaps"| Section1 & Section2 & SectionN
    Section1 & Section2 & SectionN --> Discover
    Discover --> NewSections --> Integrate
    Integrate --> AddRefs --> Final
```

## Phase Details

### Phase 1: Foundation Research
- Executes multiple search queries for topic variety
- Fetches page content via headless Chrome
- Summarizes sources using LLM with thinking mode
- Filters for relevance and minimum word count
- Saves source summaries to `_incoming/sources/`

### Phase 2: Outline & Gap Analysis
- Generates structured article outline from sources
- Identifies gaps in research coverage
- Suggests additional sections needed

### Phase 3: Targeted Research (Iterative)
- Performs focused searches to fill identified gaps
- Adds new sources to the knowledge base
- Re-analyzes gaps after each round
- Configurable max rounds (default: 2)

### Phase 4: Section Generation
- Generates each section individually
- Uses RAG to select relevant sources per section
- Maintains coherence with already-written sections

### Phase 5: Section Discovery
- Analyzes sources for missed topics
- Suggests additional sections to add
- Generates content for discovered sections

### Phase 6: Integration & Polish
- Merges all sections into cohesive article
- Improves transitions and flow
- Ensures consistent style and tone

### Phase 7: Citation Addition
- Adds footnote markers `[^N]` to article
- Uses only provided sources (prevents hallucination)
- Appends References section

## Multi-Model Configuration

Different models are used for different task complexities:

| Task | Model | Thinking Mode |
|------|-------|---------------|
| Topic Suggestion | qwen3:8b | No |
| Source Summarization | qwen3:14b | Yes |
| Entity Extraction | qwen3:14b | Yes |
| Outline Generation | qwen3:32b | No |
| Gap Analysis | qwen3:14b | No |
| Section Generation | qwen3:32b | Yes |
| Section Discovery | qwen3:14b | No |
| Article Integration | qwen3:32b | No |
| Reference Addition | qwen3:14b | No |

## Prerequisites

### GitHub CLI

```bash
gh auth login
gh auth status
```

### Ollama

```bash
# Fast model
ollama pull qwen3:8b

# Medium model
ollama pull qwen3:14b

# Large model
ollama pull qwen3:32b

# Embedding model (for knowledge-base)
ollama pull nomic-embed-text
```

## Configuration

Configuration is loaded from `config/base.env` with optional overrides in `.env`.

### Key Settings

```bash
# Multi-model configuration
LLM_MODEL_FAST=qwen3:8b      # Fast tasks
LLM_MODEL_ENTITY=qwen3:14b   # Entity extraction
LLM_MODEL_ARTICLE=qwen3:32b  # Article generation

# LLM Thinking Mode
LLM_THINK_MODE=true

# Research settings
PHASE1_TARGET_SOURCES=20     # Initial sources to gather
MAX_RESEARCH_ROUNDS=2        # Gap-filling iterations
SOURCES_PER_SECTION=8        # Sources per section (RAG)

# Knowledge-base integration (optional)
USE_KNOWLEDGE_BASE=false
KB_API_URL=http://localhost:8081
```

## Running

### Docker Compose (Recommended)

```bash
cd researcher/infra
docker compose up -d

# View logs
docker compose logs -f researcher
```

### Direct Execution

```bash
cd researcher

# Normal mode (continuous loop)
go run . 

# Single run mode
go run . --once

# Merge-only mode (just merge pending PRs)
go run . --merge-only
```

## Workflow Sequence

```mermaid
sequenceDiagram
    participant Issue as GitHub Issue
    participant Agent as Researcher
    participant Web as Web Search
    participant LLM as Ollama
    participant PR as GitHub PR

    Issue->>Agent: research-request label
    
    Note over Agent: Phase 1: Foundation
    Agent->>Web: Search queries
    Web->>Agent: Results
    loop For each result
        Agent->>Web: Fetch page
        Agent->>LLM: Summarize (14b + think)
    end
    
    Note over Agent: Phase 2: Outline
    Agent->>LLM: Generate outline (32b)
    Agent->>LLM: Analyze gaps (14b)
    
    Note over Agent: Phase 3: Targeted Research
    loop While gaps exist
        Agent->>Web: Targeted searches
        Agent->>LLM: Summarize new sources
        Agent->>LLM: Re-analyze gaps
    end
    
    Note over Agent: Phase 4: Sections
    loop For each section
        Agent->>LLM: Generate section (32b + think)
    end
    
    Note over Agent: Phase 5: Discovery
    Agent->>LLM: Discover sections (14b)
    Agent->>LLM: Generate new sections
    
    Note over Agent: Phase 6: Integration
    Agent->>LLM: Integrate article (32b)
    
    Note over Agent: Phase 7: Citations
    Agent->>LLM: Add references (14b)
    
    Agent->>PR: Create PR with article
```

## Output Format

Articles are created with YAML frontmatter:

```yaml
---
id: 01KBCVQXJS3QK3JCRGTWBFH2A6
title: Quantum Mechanics
author: Gitopedia Researcher
summary: A comprehensive overview of quantum mechanics...
tags: [physics, quantum, science]
created: 2025-12-03T04:06:38Z
researcher_version: 0.3.6
---

# Quantum Mechanics

Content with citations[^1] to sources[^2]...

## References

[^1]: Source title - https://example.com
[^2]: Another source - https://example.org
```

## Versioning

The researcher uses semantic versioning:

- Version stored in `VERSION` file
- Patch version auto-increments on commit (via git hook)
- Changes documented in `CHANGELOG.md`
- Version embedded in generated articles

### Installing Git Hooks

```bash
cd researcher
./scripts/install-hooks.sh
```

## Debugging

Enable debug output to save thinking traces and intermediate outputs:

```bash
RESEARCH_DEBUG_SOURCES=true
```

Debug files are saved to `Compendium/_debug/` in the PR branch:
- `articles/{slug}/outline.json` - Generated outline
- `articles/{slug}/thinking.txt` - LLM reasoning traces
- `sources/{slug}/` - Raw fetched pages and summaries

## Project Structure

```
researcher/
├── cmd/
│   └── ingest/          # Source ingestion tool
├── config/
│   └── base.env         # Default configuration
├── internal/
│   ├── agent/
│   │   ├── agent.go     # Main agent logic
│   │   └── phases.go    # Multi-phase generation
│   ├── authority/       # Entity authority management
│   ├── github/          # GitHub API client
│   ├── kb/              # Knowledge-base client
│   ├── llm/             # LLM client and prompts
│   └── search/          # Web search and fetch
├── infra/
│   ├── docker-compose.yml
│   └── README.md        # Infrastructure docs
├── scripts/
│   ├── install-hooks.sh
│   └── update-version.sh
├── VERSION              # Current version
├── CHANGELOG.md         # Version history
└── main.go              # Entry point
```

## Related Documentation

- [Main Architecture](../gitopedia/docs/architecture.md)
- [Infrastructure Setup](infra/README.md)
- [LLM Prompts](internal/llm/prompts/README.md)
- [Integration Guide](../gitopedia/docs/integration.md)
