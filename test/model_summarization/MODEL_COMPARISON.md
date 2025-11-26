# Model Comparison for Summarization Task

## Test Setup
- **Topic**: Solar Energy Technologies
- **Test File**: `solar-energy-technologies--earth-org-3-raw.txt` (12,404 chars)
- **Target**: 1200-2000 words, valid JSON format

---

# Current Results (Two-Stage Pipeline, Current Models Only)

*Models currently installed: `gemma3:12b`, `gemma3:27b`, `qwen3:14b`, `qwen3:32b`. Tests use Stage 1 (plain-text summarization) + Stage 2 (JSON conversion with `gemma3:12b`).*

## Stage 1 – Summarization Quality (Plain Text)

Single-source sanity check: `solar-energy-technologies--earth-org-3-raw.txt`

| Model       | Size   | Words | Duration | Notes |
|------------|--------|-------|----------|-------|
| gemma3:12b | 8.1 GB | 788   | 68s      | Clean, fact-dense summary; includes minor site-specific footer text |
| gemma3:27b | 17 GB  | 867   | 288s     | Very close to original fact list; strong structure |
| qwen3:14b  | 9.3 GB | 874   | 112s     | Good narrative, keeps most key facts and numbers |
| qwen3:32b  | 20 GB  | 745   | 484s     | Good quality, but slower and slightly shorter |

**Observations (Stage 1):**
- All four models produce **700–900 word** summaries with high factual coverage for this article.
- **gemma3:27b** is closest to a faithful re-expression of the original bullet list.
- **qwen3:14b** offers a strong narrative style while preserving key statistics.
- **qwen3:32b** does not clearly justify its additional size/cost on this test case.

### Multi-source aggregate results (27 `_debug/sources` pages)

For a broader view, the same four models were run (Stage 1 only) against **27 raw pages** under `gitopedia/Compendium/_debug/sources/solar-energy-technologies-*-raw.txt`. The table below shows averages over all sources:

| Model       | Avg Words | Min–Max Words | Avg Duration (s) | Notes |
|------------|-----------|---------------|------------------|-------|
| gemma3:12b | ~572      | 1 – 902       | **~56 s**        | Fastest; solid factual coverage; occasionally keeps minor site/footer text |
| gemma3:27b | ~550      | 1 – 1081      | ~183 s           | Similar length/quality to 12b but ~3× slower |
| qwen3:14b  | **~684**  | 1 – 1489      | ~123 s           | Longest, most detailed summaries overall; good narrative and fact retention |
| qwen3:32b  | ~542      | 1 – 1283      | **~457 s**       | Very slow, no clear quality advantage over 14b/12b |

**Takeaways (multi-source):**
- **qwen3:14b** consistently produces the longest and most detailed summaries, with strong factual preservation across many different sites.
- **gemma3:12b** is the best **speed/robustness** tradeoff: much faster than the larger models while still capturing the key facts.
- **gemma3:27b** and **qwen3:32b** do not provide enough extra quality to justify their much higher latency on current hardware.

## Stage 2 – JSON Conversion (Using `gemma3:12b` as Converter)

Converter: `gemma3:12b`  
Input: Stage 1 summaries from each model above.

- **gemma3:12b → JSON**: ✅ valid JSON
- **gemma3:27b → JSON**: ✅ valid JSON
- **qwen3:14b → JSON**: ✅ valid JSON
- **qwen3:32b → JSON**: ✅ valid JSON

The converter reliably:
- Preserves the full summary text in the `summary` field.
- Sets `relevant: true` for this article.
- Produces a consistent `language` field (`"en"`).

In the multi-source run, `gemma3:12b` successfully produced valid JSON for almost all (source, model) combinations. Occasional `json_valid: false` results were tied to particularly messy inputs (e.g., partial pages, paywalls), not to a specific summarizer family.

---

## Hardware and performance notes

- Current machine has **~8 GB of VRAM**, which is not enough to comfortably run the largest models fully on GPU.
- In this setup:
  - **gemma3:12b** can benefit from GPU acceleration and is relatively fast (~56 s average per page).
  - **gemma3:27b** and **qwen3:32b** are effectively **CPU-bound** under these constraints, which explains their very high latencies (~180 s and ~460 s on average).
  - **qwen3:14b** sits in the middle: slower than gemma3:12b, but still usable, and gives noticeably longer summaries.
- On future hardware (more VRAM and/or faster GPUs), it may become viable to:
  - Re-test **gemma3:27b** and **qwen3:32b** as primary summarizers.
  - Add new models (e.g. bigger Qwen or Gemma variants) to the same two-stage test harness and compare them against the baselines here.


## Recommendations (Current Setup)

- **Stage 1 (Summarization)**:
  - Default: **gemma3:27b** for maximum fidelity to source facts.
  - Alternative: **qwen3:14b** if you prefer more narrative flow.
- **Stage 2 (JSON conversion)**:
  - Use **gemma3:12b** as the dedicated conversion model (tested 4/4 valid JSON on current models).

Future work can add more sources and automated quality scoring, but this comparison only tracks the four models that are still installed.

### Updated recommendations after multi-source tests

Based on the full run across 27 `_debug/sources` pages:

- **Stage 1 (Summarization)**:
  - **Default**: **gemma3:12b** – best balance of speed and quality on current 8 GB VRAM hardware.
  - **When you want maximum detail**: **qwen3:14b** – produces the longest, richest summaries, but is roughly 2× slower than gemma3:12b.
  - **Not recommended for now**: **gemma3:27b** and **qwen3:32b** – too slow when forced to run mostly on CPU, with limited quality upside.
- **Stage 2 (JSON conversion)**:
  - Use **gemma3:12b** as the dedicated conversion model (tested reliably in both single-source and multi-source runs).

Going forward:
- Add automated quality scoring (fact coverage, hallucination checks, fluff detection).
- Re-run the same scripts when:
  - New models are pulled into Ollama.
  - Hardware changes (e.g. more VRAM, faster GPU) make it feasible to promote larger models to the primary summarization role.

