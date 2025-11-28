# Model Comparison for Summarization Task

## Test Setup
- **Topic**: Solar Energy Technologies
- **Test File**: `solar-energy-technologies--earth-org-3-raw.txt` (12,404 chars)
- **Target**: 1200-2000 words, valid JSON format
- **Test Environment**: 8 GB VRAM
- **Pipeline**: Stage 1 (plain-text summarization) + Stage 2 (JSON conversion with `gemma3:12b`)

## Stage 1: Summarization Performance

### Single Source Test

| Model       | Size   | Words | Duration | Notes |
|------------|--------|-------|----------|-------|
| gemma3:27b | 17 GB  | 867   | 288s     | Closest to original fact list |
| gemma3:12b | 8.1 GB | 788   | 68s      | Fact-dense, minor footer text |
| gemma3:4b  | 3.3 GB | 895   | 20s      | Fastest, efficient GPU usage |
| qwen3:32b  | 20 GB  | 745   | 484s     | Slower, no quality advantage |
| qwen3:14b  | 9.3 GB | 874   | 112s     | Narrative style, ~75-85% fact retention |
| qwen3:8b   | 5.5 GB | 800*  | 65s      | *Estimated from similar runs. 2.1× faster than 14b |

### Multi-Source Test (27 pages)

| Model       | Avg Words | Range      | Avg Duration | Notes |
|------------|-----------|------------|--------------|-------|
| gemma3:27b | 550       | 1-1081     | 183s         | 3× slower than 12b |
| gemma3:12b | 572       | 1-902      | 56s          | Fast, solid coverage |
| gemma3:4b  | N/A       | N/A        | N/A          | Not tested (multi-source) |
| qwen3:32b  | 542       | 1-1283     | 457s         | Very slow, no advantage |
| qwen3:14b  | 684       | 1-1489     | 123s         | Longest summaries |

**Findings:**
- All models produce 700-900 word summaries (single source)
- gemma3:4b fastest (20s), good quality (895 words)
- qwen3:14b produces longest summaries (multi-source)
- gemma3:12b fast with good quality
- Larger models (27b, 32b) too slow on 8GB VRAM (CPU-bound)

## Quantization & Custom Import Analysis

**Objective**: Evaluate impact of quantization (Q5_K_M, Q8_0) on performance vs standard Q4_K_M.
**Method**: Manually imported GGUF files for `qwen3:4b` and `qwen3:8b`.

| Model Variant | Quant | Size | Duration | Words Generated | GPU Util | Result |
|--------------|-------|------|----------|-----------------|----------|--------|
| **qwen3:4b** (Library) | Q4_K_M | 3.3 GB | **~20s** | ~900 | High | ✅ Success |
| qwen3:4b (Imported) | Q5_K_M | 2.9 GB | 780s | 13,653 | 92% | ❌ Failed (Infinite loop) |
| qwen3:4b (Imported) | Q8_0 | 4.3 GB | 966s | 29,789 | 93% | ❌ Failed (Infinite loop) |
| **qwen3:8b** (Library) | Q4_K_M | 5.2 GB | **65s** | ~800 | 96% | ✅ Success |
| qwen3:8b (Imported) | Q5_K_M | 5.9 GB | 1358s | 33,130 | 96% | ❌ Failed (Infinite loop) |
| qwen3:8b (Imported) | Q8_0 | 8.7 GB | 1878s | 26,330 | 48% | ❌ Failed (OOM, Slow) |

**Critical Finding**:
Manually imported GGUF models failed to adhere to stop tokens, resulting in "hallucinated" infinite generation (generating 13k-30k words of repetitive or nonsensical text). This highlights the importance of correctly configured `Modelfile` templates (stop parameters, chat templates) which the official library images provide out-of-the-box.

**Hardware Note**:
- `qwen3:8b-Q8_0` (8.7 GB) exceeded the 8GB VRAM, causing a massive performance drop (48% GPU util vs 96%) and spilling to system RAM/CPU.

## Stage 2: JSON Conversion

**Converter**: `gemma3:12b`

| Input Model | JSON Valid | Notes |
|------------|------------|-------|
| gemma3:27b | ✅ | Reliable |
| gemma3:12b | ✅ | Reliable |
| gemma3:4b  | N/A | Not tested |
| qwen3:32b  | ✅ | Reliable |
| qwen3:14b  | ✅ | Reliable |

**Multi-source**: Valid JSON for almost all combinations. Failures tied to messy inputs (paywalls, partial pages), not model-specific.

## Fact Retention Analysis

**Model Tested**: `qwen3:14b`  
**Test Date**: November 2025  
**Sources**: Behavioral Economics PR (#40) - Investopedia, SocialStudiesHelp.com  
**Overall Retention**: 75-85%

| Category | Retention | Notes |
|----------|-----------|-------|
| Core Concepts | 95% | Excellent |
| Key Figures & Dates | 90% | Very good |
| Main Examples | 80% | Good, sometimes without numbers |
| Specific Statistics | 60% | Numbers/percentages often lost |
| Detailed Examples | 70% | Good, sometimes simplified |
| Additional Entities | 65% | Less famous figures omitted |
| Quotes | 20% | Rarely verbatim |

### Source 1 (Investopedia): 70-80% captured
**Captured**: Nobel laureates (Kahneman 2002, Thaler 2017), timeline, core concepts, main examples  
**Missing**: Specific stats (.342 batting avg, 2,000 calories), additional laureates (Becker, Akerlof), detailed examples

### Source 2 (SocialStudiesHelp.com): 85-90% captured
**Captured**: Specific numbers ("90% effective", "$50", "20% less"), organizations, countries, statistics

**Pattern**: Concepts prioritized over specific numbers

## Hardware Performance (8GB VRAM)

### GPU/CPU Monitoring Results

| Model       | GPU Util | GPU Memory | CPU Usage | Power | Performance |
|------------|----------|------------|-----------|-------|-------------|
| gemma3:27b | CPU-bound | N/A        | N/A       | N/A   | Slow (~183s avg) |
| gemma3:12b | N/A      | N/A        | N/A       | N/A   | Fast (~56s avg) |
| gemma3:4b  | 80.2% avg (96% peak) | 4.4 GB | 11.2% avg | 35.5W | 20s (fastest) |
| qwen3:32b  | CPU-bound | N/A        | N/A       | N/A   | Very slow (~457s avg) |
| qwen3:14b  | 34% avg (93% peak) | 7.3 GB | 42.4% avg | 49.9W | 136s (test), ~123s (multi-source) |
| qwen3:8b   | 95.7% avg (99% peak) | 5.5 GB | 10.4% avg | 40.7W | 65s (2.1× faster than 14b) |

**Findings:**
- gemma3:4b fastest (20s), efficient GPU usage (80.2% avg), lowest memory (4.4 GB), lowest power (35.5W)
- qwen3:8b uses GPU efficiently (95.7% avg), 2.1× faster than 14b, 25% less memory than 14b
- qwen3:14b uses GPU but with low utilization (34% avg), suggesting hybrid CPU/GPU
- Larger models (27b, 32b) are CPU-bound on 8GB VRAM

## Recommendations

**Stage 1 (Summarization)**:
- **Default**: `gemma3:4b` - fastest (20s), efficient GPU (80.2%), lowest memory (4.4 GB), good quality
- **Alternative**: `gemma3:12b` - good speed/quality balance on 8GB VRAM
- **Alternative**: `qwen3:8b` - 2.1× faster than 14b, better GPU utilization, 25% less memory
- **Maximum detail**: `qwen3:14b` - longest summaries, but slower and less efficient GPU usage
- **Not recommended**: `gemma3:27b`, `qwen3:32b` - too slow, limited quality gain
- **Avoid**: Manually imported GGUFs without strict `Modelfile` configuration (risk of infinite generation).

**Stage 2 (JSON Conversion)**:
- Use `gemma3:12b` - reliable converter for all models

## Future Work

- Automated quality scoring (fact coverage, hallucination checks)
- Improve fact retention prompts (emphasize statistics/numbers)
- Fact-checking validation (automated source vs summary comparison)
- Re-test when hardware improves or new models available
