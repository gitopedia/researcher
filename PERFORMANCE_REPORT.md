# Gitopedia Researcher Performance Report

**Generated:** January 26, 2026  
**Analyzed Run:** January 26, 2026 (10:50 - 16:38 NZST)  
**Log File:** `researcher.log` (93,392 lines)

## Executive Summary

The last research run processed multiple quantum physics articles over approximately **5 hours 48 minutes**. The primary performance bottlenecks are:

1. **Image Generation (ComfyUI)** - 7-11 minutes per image (~70% of total time when generating images)
2. **Entity Extraction** - Variable from 4-35 seconds depending on input size
3. **Article Generation** - 8-27 seconds per mini-article

---

## Detailed Timing Analysis

### 1. LLM Operations (Local Ollama)

| Operation | Min Time | Avg Time | Max Time | Notes |
|-----------|----------|----------|----------|-------|
| **ExtractEntities** | 2.8s | ~12s | 35.5s | Highly variable; depends on input size. Large inputs (>4000 chars) take longer |
| **ExtractConcepts** | 2.4s | ~11s | 27.6s | Finds 0-20 concepts per extraction |
| **GenerateMiniArticle** | 5.9s | ~18s | 27.8s | Output varies from 134-3897 chars |
| **SuggestNewSection** | 6.8s | ~14s | 20.7s | Consistent performance |
| **CompareSections** | 5.9s | ~15s | 27.2s | Performance depends on article complexity |
| **OrderSections** | 10.6s | ~15s | 23.5s | Scales with number of sections (2-14) |
| **MapConceptToSection** | 6.3s | ~11s | 23.7s | Maps concepts to existing or new sections |
| **RewriteSectionWithConcept** | 9.8s | ~15s | 21.1s | Rewrites existing content with new concepts |
| **GenerateNewSection** | 12.0s | ~14s | 18.4s | Creates new section content |
| **ExtractVisualElements** | 13.1s | ~15s | 16.8s | For image prompt generation |
| **GenerateImagePrompt** | 12.3s | ~15s | 18.3s | Creates image generation prompts |

### 2. Image Generation (ComfyUI with Qwen Image 25B FP8)

From `regen-with-images.log`:

| Step | Time | Notes |
|------|------|-------|
| **EvaluateSectionImage** | ~11s | Evaluates if section needs an image |
| **GenerateSectionImagePrompt** | 16-22s | Generates detailed prompt |
| **ComfyUI Image Generation** | **7-11 minutes** | **MAJOR BOTTLENECK** |

**Image Generation Details:**
- quantum-decoherence key-concepts: 11 min 42s (12:38:05 → 12:49:47)
- quantum-mechanics fundamental-principles: 7 min 33s (12:49:47 → 12:57:20)
- quantum-tunneling interpretations: 6 min 12s (12:57:20 → 13:03:32)
- wave-function key-concepts: 6 min 17s (13:03:32 → 13:09:49)

**Average image generation time: ~8 minutes per image**

### 3. Web Operations

| Operation | Time | Notes |
|-----------|------|-------|
| **Content Fetching (headless)** | 3-45s | Varies by site; some timeout at 45s |
| **Summarization (Stage 1)** | 5-27s | Plain-text LLM summarization |
| **JSON Conversion (Stage 2)** | 8-22s | Convert to structured JSON |
| **Total per source** | ~30-90s | Including fetch, summarize, extract |

### 4. Fast Operations (<1s)

| Operation | Time | Notes |
|-----------|------|-------|
| **ExtractSections** | 500-600µs | Local text parsing |
| **IsEncyclopediaSource** | 0-543µs | Domain checking |
| **Search API calls** | 2-3s | Web search requests |

---

## Time Distribution by Phase

Based on a typical article improvement cycle:

```
Article Improvement Iteration (~3-5 minutes per iteration):
├── Search for sources              [2-3s]
├── Fetch content (headless)        [3-45s]  ← Variable
├── Summarize source (2 stages)     [15-40s]
├── Extract concepts                [8-15s]
├── For each concept (5 concepts avg):
│   ├── Map to section              [8-12s]
│   └── Generate/Rewrite section    [12-18s]
├── Extract entities                [8-15s]
└── Git operations                  [<1s]

Image Generation (when enabled):
├── Evaluate sections               [11s]
├── Generate prompts                [16-22s]
└── ComfyUI generation              [7-11min] ← BOTTLENECK
```

---

## Key Bottlenecks Identified

### 🔴 Critical: Image Generation (~70% of image-enabled run time)

**Problem:** ComfyUI with Qwen Image 25B FP8 takes 7-11 minutes per image.

**Recommendations:**
1. Batch image generation during off-peak hours
2. Consider smaller/faster image models for drafts
3. Implement image caching to avoid regeneration
4. Run image generation in parallel (if GPU memory permits)

### 🟡 Moderate: Entity Extraction Variability (4-35s)

**Problem:** Large variance in ExtractEntities timing, with some calls taking >30s.

**Observations:**
- Short inputs (<500 chars): ~4-8s
- Medium inputs (500-3000 chars): ~10-15s
- Large inputs (>3000 chars): ~20-35s
- Retries on JSON parse failures add additional time

**Recommendations:**
1. Pre-chunk large inputs consistently
2. Implement entity extraction caching for repeated content
3. Consider parallel extraction for multiple chunks

### 🟡 Moderate: Content Fetching Timeouts (up to 45s)

**Problem:** Headless browser fetches can take up to 45s (timeout) for slow/blocked sites.

**Observations from log:**
- Some sites (livescience.com) consistently timeout
- 403/blocked responses waste ~5s per attempt

**Recommendations:**
1. Maintain a blocklist of slow/problematic domains
2. Reduce timeout for non-critical fetches
3. Implement adaptive timeout based on domain history

### 🟢 Minor: JSON Conversion Failures

**Problem:** Many JSON conversions fail (relevant=false), falling back to plain text.

**Impact:** Adds ~8-22s for the JSON conversion attempt before fallback.

**Recommendations:**
1. Evaluate if JSON conversion adds enough value
2. Skip JSON conversion for sources marked as low-relevance
3. Improve prompt for better JSON compliance

---

## Performance Metrics Summary

### Last Run Statistics

| Metric | Value |
|--------|-------|
| **Total Run Duration** | ~5h 48min |
| **Articles Processed** | 8-10 (estimated) |
| **Improvement Iterations** | 100+ |
| **Sources Fetched** | ~150+ |
| **LLM Calls** | ~500+ |
| **Images Generated** | 4 (in regen-with-images run) |

### Model Configuration

```
Fast Model: qwen3:8b
Article/Entity/Summarize Model: deepseek-r1:14b
Image Model: qwen_image_2512_fp8_e4m3fn.safetensors
```

---

## Recommendations Summary

### Immediate Optimizations

1. **Parallelize image generation** - Process multiple prompts while waiting for ComfyUI
2. **Skip JSON conversion** for sources that consistently fail
3. **Add domain blocklist** for chronically slow/blocked sites

### Medium-term Improvements

1. **Implement caching** for:
   - Entity extraction results
   - Section comparisons
   - Image prompts
2. **Batch processing** for image generation during low-usage periods
3. **Progress persistence** to resume interrupted runs efficiently

### Long-term Considerations

1. **GPU upgrade** or **distributed inference** for faster image generation
2. **Consider cloud LLM API** for specific high-latency operations
3. **Adaptive model selection** based on content complexity

---

## Appendix: Sample Timing Data

### Slowest Operations in Last Run

| Timestamp | Operation | Duration |
|-----------|-----------|----------|
| 16:36:57 | ExtractEntities | 35.5s |
| 16:29:05 | ExtractEntities | 28.7s |
| 15:47:00 | CompareSections | 27.2s |
| 16:16:50 | GenerateMiniArticle | 27.8s |
| 14:53:29 | ExtractConcepts | 27.6s |

### Fastest Operations

| Timestamp | Operation | Duration |
|-----------|-----------|----------|
| 16:36:57 | ExtractSections | 528µs |
| 15:27:25 | IsEncyclopediaSource | 543µs |
| 16:49:58 | ExtractConcepts | 2.4s |
| 16:34:40 | ExtractEntities | 4.1s |

---

*Report generated by analyzing `researcher.log` and `regen-with-images.log`*
