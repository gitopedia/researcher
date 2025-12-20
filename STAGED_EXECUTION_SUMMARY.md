# Staged Execution Process Summary

This document outlines what each step does and the prompts used for each LLM operation.

## Step 1: Discovery

### What It Does:
- Creates a research branch (e.g., `research/climate-change`)
- Searches for potential sources using the query: `{topic} encyclopedia overview`
- Saves search results to `Compendium/_debug/articles/{slug}/step-1-discovery/results.json`
- Updates `state.json` to mark discovery as completed
- **No LLM prompts used** - this step only uses web search

### Output Files:
- `step-1-discovery/results.json` - Array of search results (title, URL, snippet)

---

## Step 2: Summarization

### What It Does:
1. Loads discovery results from `step-1-discovery/results.json`
2. For each source (skipping PDFs):
   - Fetches the full content from the URL
   - Runs **Stage 1: Plain-text Summarization** (LLM prompt)
   - Runs **Stage 2: JSON Conversion** (LLM prompt) to check relevance
   - If relevant, extracts entities using **Entity Extraction** (LLM prompt)
   - Saves the summary and creates authority entries
3. Saves the first relevant source summary to `step-2-summarization/summary-1.md`
4. Creates a source file in `Compendium/_incoming/sources/`
5. Updates `state.json`

### LLM Prompts Used:

#### Prompt 1: Plain-Text Summarization
**System Prompt** (`phase_1_step_1_summarize_source_system.txt`):
- Role: Extract and compress informative content from web pages
- Extract ALL useful factual content (facts, dates, people, organizations, technical details)
- Filter out navigation, ads, cookies, social media buttons
- Output format: Heading + bullet-list structure
- Target: 1200-2000 words
- Use markdown headings (`# Topic`) and bullet points (`- fact`)

**User Prompt** (`phase_1_step_1_summarize_source_user.txt`):
```
You are extracting informative content from a web page. The page was found while researching "{{.Topic}}", but your extraction should capture ALL useful encyclopedia-worthy information, not just content about that specific topic.

**When to skip extraction (output "NOT_RELEVANT" only):**
- The page is mostly ads, spam, or promotional content
- The page has minimal actual content (just links, navigation, or stubs)
- The content is very low quality or incoherent
- The page is a paywall, login page, or error page

If the page has useful content, extract it comprehensively (1200-2000 words).

Page URL: {{.URL}}
Page Content: {{.Content}}
```

#### Prompt 2: JSON Conversion (Relevance Check)
**System Prompt** (`phase_1_step_2_convert_summary_system.txt`):
- Converts text summaries into structured JSON
- Outputs ONLY valid JSON with fields: `relevant`, `reason`, `summary`, `language`, `topics`
- Copies summary text exactly into the "summary" field
- Derives topics from headings or infers 3-10 topic labels

**User Prompt** (`phase_1_step_2_convert_summary_user.txt`):
```
Convert this summary into JSON format. Copy the content exactly into the "summary" field and derive topics from the headings and main sections.

Summary to convert: {{.Content}}
```

#### Prompt 3: Entity Extraction
**System Prompt** (`extract_entities_system.txt`):
- Extracts people, organizations, places, and topics
- Outputs ONLY a valid JSON array (no markdown, no explanation)
- Requires disambiguation for ambiguous names (e.g., "John Smith (economist)")
- Returns array of objects with `type` and `name` fields

**User Prompt** (`extract_entities_user.txt`):
```
Extract key entities from the text below. Return ONLY a JSON array, no other text.

Each object must have:
- "name": Entity name with qualifier if ambiguous
- "type": One of "person", "org", "place", "topic"

Rules:
1. Only extract entities significant enough to appear in multiple articles
2. Add qualifiers for ambiguous names
3. Use the most specific, commonly-known name
4. Skip generic terms

Text: {{.Content}}
```

### Output Files:
- `step-2-summarization/summary-1.md` - The full summary text
- `Compendium/_incoming/sources/{slug}--{domain}-{index}.md` - Source file with frontmatter

---

## Step 3: Drafting

### What It Does:
1. Loads the summary from `step-2-summarization/summary-1.md`
2. Generates a mini-encyclopedia article using **GenerateMiniArticle** (LLM prompt)
3. Creates the article file in `Compendium/_incoming/{slug}.md` with frontmatter
4. Saves a debug copy to `step-3-drafting/article-draft.md`
5. Updates `state.json`

### LLM Prompts Used:

#### GenerateMiniArticle
**System Prompt** (`generate_mini_article_system.txt`):
```
You are an expert encyclopedist. Your task is to write a standalone, mini-encyclopedia article based ONLY on the provided source text.
- Do not use outside knowledge.
- The article should be objective, factual, and well-structured.
- Use standard encyclopedia sections if applicable (Overview, History, Key Concepts, etc.), but adapt to the content available in the source.
- If the source is short, the article can be short.
- Do not include "Conclusion" or "Summary" sections unless they are substantive.
- Format in Markdown.
```

**User Prompt** (`generate_mini_article_user.txt`):
```
Topic: {{.Topic}}
Source Title: {{.SourceTitle}}
Source Content: {{.SourceSummary}}

Write a mini-encyclopedia article about {{.Topic}} using ONLY the information above.
```

### Output Files:
- `step-3-drafting/article-draft.md` - Debug copy of the generated article
- `Compendium/_incoming/{slug}.md` - The final article file

---

## Step 4: Finalize

### What It Does:
- Updates `state.json` to mark finalize as completed
- In local mode: Logs a message that PR creation requires manual push
- In remote mode: Creates a GitHub Pull Request
- **No LLM prompts used** - this step only updates state and creates PRs

### Output Files:
- Updated `state.json` with finalize step completed

---

## State Management

The `state.json` file tracks progress:
```json
{
  "topic": "Climate Change",
  "slug": "climate-change",
  "branch": "research/climate-change",
  "last_completed_step": "finalize",
  "steps": {
    "discovery": { "status": "completed", "timestamp": "..." },
    "summarization": { "status": "completed", "timestamp": "..." },
    "drafting": { "status": "completed", "timestamp": "..." },
    "finalize": { "status": "completed", "timestamp": "..." }
  }
}
```

## File Structure

```
Compendium/_debug/articles/{slug}/
├── state.json                          # Progress tracking
├── step-1-discovery/
│   └── results.json                    # Search results
├── step-2-summarization/
│   └── summary-1.md                    # Source summary
└── step-3-drafting/
    └── article-draft.md                # Generated article

Compendium/_incoming/
├── {slug}.md                           # Final article
└── sources/
    └── {slug}--{domain}-{index}.md    # Source file
```

