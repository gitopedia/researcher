# Summarization Model Tests – HOWTO

This document explains:
- What the summarization tests evaluate.
- How to run the tests.
- How an LLM (or a human) should evaluate the outputs, with a focus on preserving source content.
- How hardware constraints (8 GB VRAM, CPU vs GPU) affect the results and when to re-test with new models or hardware.

---

## 1. What the tests evaluate

The tests are designed around a **two-stage pipeline**:

### Stage 1 – Summarization (plain text, structured)

Binary: `test_stage1_summarize`  
Prompt: `test_stage1_summarize/prompts/summarize_plaintext_system.txt`

Stage 1 asks the model to:
- Read a raw web page from `gitopedia/Compendium/_debug/sources/*-raw.txt`.
- Extract **all encyclopedia-worthy information**, not just content tied to a single topic.
- Filter out web fluff:
  - Navigation, menus, headers/footers, cookie banners, “boost this article”, ads, social links, etc.
- Produce a **structured plain-text summary** using:
  - `# <Heading>` lines for each sub-topic.
  - `- <fact>` bullet lines under each heading.
  - Neutral, encyclopedic prose inside bullets.
- Target **1200–2000 words** when the source has enough content.

Metrics recorded per run:
- `word_count` – number of words in the summary.
- `duration_ms` – how long the call took.
- `relevant` – whether the page was considered worth summarizing.
- `summary` – the full text, saved as:
  - `stage1-<model>.txt`
  - `stage1-<model>.json` (metadata wrapper)

### Stage 2 – JSON conversion (structure only)

Binary: `test_stage2_json`  
Prompt: `test_stage2_json/prompts/convert_json_system.txt`

Stage 2 is deliberately **pure formatting**:
- Input: a Stage 1 plain-text summary.
- Output: a single JSON object with fields:
  - `relevant` – boolean.
  - `reason` – short description of what the content covers.
  - `summary` – the full text from Stage 1, copied **verbatim**.
  - `language` – ISO 639‑1 language code (e.g. `en`).
  - `topics` – array of short topic labels, usually derived from `#` headings.
- Rules:
  - Do **not** change or shorten the summary text.
  - Derive `topics` from `#` headings where possible; otherwise infer 3–10 concise topic labels.
  - Output **only valid JSON** (no markdown fences, no commentary).

Metrics recorded per run:
- `json_valid` – whether the response parsed as JSON.
- `duration_ms` – conversion latency.
- If valid: `summary_length`, `language`, `relevant`, `reason`.

---

## 2. How to run the tests

All commands assume you are in the `researcher` repo root:

```bash
cd /home/mantis/Projects/Solus/gitopedia/researcher
```

### Requirements

- `go` installed (for building the test binaries).
- Ollama running and reachable at the configured base URL (default: `http://localhost:11434`).
- Models already pulled into Ollama:
  - `gemma3:12b`
  - `gemma3:27b`
  - `qwen3:14b`
  - `qwen3:32b`

### 2.1 Single-source test (Earth.org article only)

To test just the `earth-org-3` source:

```bash
./test/model_summarization/scripts/run_two_stage_tests.sh
```

This script:
- Builds the old single-source test binary (kept mainly for reference).
- Runs Stage 1 + Stage 2 for a single article.
- Writes outputs to `test_output/` (this directory is usually safe to clear between runs).

### 2.2 Multi-source test (all `_debug/sources`)

The main, up-to-date harness is:

```bash
./test/model_summarization/scripts/run_two_stage_multi.sh
```

What it does:
- Builds the Stage 1 and Stage 2 binaries:
  - `bin/test_stage1_summarize`
  - `bin/test_stage2_json`
- Scans:  
  `../gitopedia/Compendium/_debug/sources/solar-energy-technologies-*-raw.txt`
- For each raw file and each model:
  - Runs Stage 1 summarization.
  - Stores per-source outputs under:  
    `test/model_summarization/test_output_two_stage/<slug>/`
    - `stage1-<model>.txt`
    - `stage1-<model>.json`
- Then, for each summary and the converter model (`gemma3:12b` by default):
  - Runs Stage 2 JSON conversion.
  - Writes `stage2-gemma3-12b-from-<model>.json` into the same directory.
- At the end, prints:
  - A per-source **Stage 1 summary table** (words + duration).
  - A per-source **Stage 2 JSON validity table**.

To clear previous multi-source outputs safely:

```bash
rm -rf test/model_summarization/test_output_two_stage
mkdir -p test/model_summarization/test_output_two_stage
```

---

## 3. How to evaluate the tests (for humans or LLMs)

When inspecting results, look at both:
- The **raw source** in `gitopedia/Compendium/_debug/sources/*.txt`.
- The corresponding summaries and JSON in `test_output_two_stage/<slug>/`.

### 3.1 Content preservation (most important)

For each (source, model) pair:
- Identify the **key factual payload** in the raw page:
  - Named entities: people, places, organisations.
  - Dates, historical milestones.
  - Numerical facts: percentages, capacities, GW/TWh, acreage, lifetimes, cost deltas.
  - Definitions and conceptual explanations.
- Check that the Stage 1 summary:
  - Includes **most or all** of these key facts.
  - Does **not invent** numbers or entities not present in the source (hallucinations).
  - Avoids copying site-specific boilerplate (donation footers, navigation, “boost this article”).

Good signs:
- Facts are rephrased but **semantically equivalent**.
- All important numbers from the source reappear in the summary.
- Unrelated site content (about the publisher, newsletter, social links) is dropped.

### 3.2 Structure and headings

Given the strict Stage 1 format:

- Check that headings follow:
  - One optional main title: `# <Main topic>`.
  - Multiple sub-topic headings: `# <Short heading>`.
- Under each heading:
  - Facts appear as bullets: `- <complete sentence>`.
  - Related facts are grouped logically (e.g. “History”, “Cost trends”, “Environmental impact”).
- No numbered lists, bold text, or markdown code blocks should appear – only `#` and `-`.

For an LLM evaluator:
- You can parse lines starting with `# ` as section titles.
- You can parse `- ` lines as atomic facts for comparison with the raw source.

### 3.3 JSON conversion quality

For Stage 2 outputs:
- Confirm `json_valid` is `true`.
- Check:
  - `summary` matches the Stage 1 text exactly (no truncation or edits).
  - `language` is correct (`"en"` for English pages).
  - `topics`:
    - Are short (2–6 words).
    - Roughly correspond to the headings / major sections.
    - Do not introduce hallucinated topics unrelated to the summary.

If you are an LLM evaluating these:
- Explicitly compare the Stage 2 `summary` to Stage 1 to ensure they are identical.
- Compare `topics` to the set of headings in Stage 1 (or infer them from clusters of facts).

---

## 4. Hardware constraints and why large models are slow

Current constraints:
- GPU VRAM: **~8 GB**.
- Some Ollama models (e.g. `gemma3:27b`, `qwen3:32b`) are too large to fit fully in VRAM and end up running mostly on **CPU**.

Practical impact from the tests:

- **gemma3:12b**
  - Average ~572 words per summary.
  - Average ~56 s per page.
  - Likely leveraging GPU; good balance of speed + quality.
- **qwen3:14b**
  - Average ~684 words per summary (most detailed).
  - Average ~123 s per page.
  - Partially/mostly CPU-bound, but still practical for offline runs.
- **gemma3:27b**
  - Average ~550 words per summary.
  - Average ~183 s per page.
  - 3× slower than 12b with similar length/quality → poor tradeoff on this hardware.
- **qwen3:32b**
  - Average ~542 words per summary.
  - Average ~457 s per page (~7.6 minutes).
  - No clear gain in quality vs 14b/12b, but dramatically slower due to CPU-bound execution.

Given these constraints, the recommended setup **today** (8 GB VRAM):
- **Stage 1**:
  - Use **gemma3:12b** by default.
  - Use **qwen3:14b** when you explicitly want the richest, longest summaries and can tolerate roughly 2× the latency.
- **Stage 2**:
  - Use **gemma3:12b** as the converter; it was stable across many sources.

---

## 5. Adding new models or re-testing on new hardware

When you:
- Pull new models into Ollama, or
- Upgrade hardware (more VRAM, faster GPU/CPU),

you should:

1. **Install and verify models**
   ```bash
   docker exec ollama ollama list
   ```

2. **Add them to the test scripts**
   - Edit `SUMMARIZE_MODELS` and/or `CONVERTER_MODEL` in:
     - `test/model_summarization/scripts/run_two_stage_multi.sh`
   - Keep at least one stable baseline (e.g. `gemma3:12b`) so you can compare relative performance.

3. **Re-run the multi-source tests**
   ```bash
   ./test/model_summarization/scripts/run_two_stage_multi.sh
   ```

4. **Update `MODEL_COMPARISON.md`**
   - Summarize:
     - Average words per model.
     - Average duration.
     - Any JSON failures or consistent issues.
   - Note whether models are primarily GPU- or CPU-bound under the new hardware.

5. **Re-evaluate recommendations**
   - If a larger model becomes fast enough on the new GPU, it might be worth promoting it as the default summarizer.
   - If a smaller model is “good enough” and much faster, prefer it for bulk runs.

This workflow keeps the testing and evaluation process reproducible for both humans and LLMs, and makes it easy to re-benchmark as the model set or hardware changes.


