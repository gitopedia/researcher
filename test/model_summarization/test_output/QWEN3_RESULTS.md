# Qwen3 Model Test Results

## Test Setup
- **Topic**: Solar Energy Technologies
- **Test File**: `solar-energy-technologies--earth-org-3-raw.txt` (12,404 chars)
- **Target**: 1200-2000 words, valid JSON format

## Results Summary

### qwen3:4b (2.5 GB)
- **JSON Compliance**: ❌ Failed - returned plain text instead of JSON
- **Word Count**: ~1,450 words (from raw output, couldn't parse JSON)
- **Duration**: ~N/A (test didn't complete JSON parsing)
- **Notes**: Produced a comprehensive, well-structured summary but completely ignored JSON format requirement. The content quality is good, but format compliance is poor.

### qwen3:8b (5.2 GB)
- **JSON Compliance**: ✅ Valid JSON
- **Word Count**: 746 words (below 1200-2000 target)
- **Duration**: ~55 seconds
- **Notes**: Produces valid JSON consistently, but summaries are still too short. Better than qwen2.5:14b (414 words) but still well below target.

### qwen3:14b (9.3 GB)
- **JSON Compliance**: ✅ Valid JSON
- **Word Count**: 588 words (below 1200-2000 target)
- **Duration**: ~130 seconds (2m 10s)
- **Notes**: Produces valid JSON, but summaries are shorter than qwen3:8b. Slower than qwen3:8b but similar quality.

## Comparison with Previous Models

| Model | Word Count | JSON | Duration | Notes |
|-------|------------|------|----------|-------|
| qwen3:4b | ~1,450 | ❌ | N/A | Long summary but no JSON |
| qwen3:8b | 746 | ✅ | 55s | Best balance so far |
| qwen3:14b | 588 | ✅ | 130s | Valid JSON but shorter |
| qwen2.5:14b | 414 | ✅ | 62s | Previous best |
| deepseek-llm:7b-32k | ~317 | ❌ | 5s | Fast but no JSON |
| deepseek-r1:14b | 113 | ⚠️ | 40s | Inconsistent |

## Key Findings

1. **qwen3:4b** produces the longest summaries (~1,450 words) but completely ignores JSON format
2. **qwen3:8b** has the best balance: valid JSON + longest summaries (746 words) among JSON-compliant models
3. **qwen3:14b** is slower and produces shorter summaries than qwen3:8b
4. **None of the models** meet the 1200-2000 word target in JSON format
5. **Larger models don't necessarily help**: qwen3:14b (14B) produces shorter summaries than qwen3:8b (8B)

## Recommendations

1. **For JSON compliance**: Use qwen3:8b or qwen3:14b
2. **For length**: qwen3:4b produces longest text but needs JSON extraction work
3. **Best overall**: qwen3:8b offers the best balance of JSON compliance and summary length
4. **Prompt engineering**: Still needed - all models produce summaries well below the 1200-2000 word target when constrained by JSON format

## Next Steps

- Test qwen3:32b when download completes (may produce longer summaries)
- Consider prompt engineering to improve length compliance
- Test with chunking approach (break content into sections)

