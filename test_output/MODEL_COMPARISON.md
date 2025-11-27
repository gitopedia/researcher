# Model Comparison for Summarization Task

## Test Setup
- **Topic**: Solar Energy Technologies
- **Test Files**: 
  - `solar-energy-technologies--earth-org-3-raw.txt` (12,404 chars)
  - `solar-energy-technologies--education-nationalgeographic-org-9-raw.txt` (30,665 chars)

## Results Summary

### deepseek-llm:7b-32k
- **JSON Compliance**: ❌ Failed - returned plain text instead of JSON
- **Word Count**: ~317 words (from raw output, couldn't parse JSON)
- **Duration**: ~5 seconds
- **Notes**: Model ignored JSON format requirement entirely. Produced a summary-like text but not in JSON format.

### qwen2.5:14b
- **JSON Compliance**: ✅ Valid JSON
- **Word Count**: 414 words (well below 1200-2000 target)
- **Duration**: ~62 seconds
- **Notes**: Produces valid JSON but summaries are too short. Model seems to be summarizing rather than extracting comprehensively.

### deepseek-r1:14b
- **JSON Compliance**: ⚠️ Mixed - valid JSON on first test, plain text on second
- **Word Count**: 113 words (from JSON summary, very short)
- **Raw Output**: ~182 words (when JSON parsing failed)
- **Duration**: ~40 seconds
- **Notes**: Inconsistent JSON output. When it does produce JSON, summaries are extremely brief. Produces longer text when not constrained by JSON format.

## Key Findings

1. **All models struggle with length requirement**: None of the tested models produced summaries in the 1200-2000 word target range.

2. **JSON compliance varies**: 
   - `qwen2.5:14b` is most consistent with JSON format
   - `deepseek-r1:14b` is inconsistent
   - `deepseek-llm:7b-32k` completely ignores JSON requirement

3. **Larger models don't necessarily help**: The 14B models (qwen2.5:14b, deepseek-r1:14b) don't produce longer summaries than the 7B model, and in some cases produce shorter ones.

4. **Speed vs Quality tradeoff**: 
   - `deepseek-llm:7b-32k`: Fastest (~5s) but worst compliance
   - `deepseek-r1:14b`: Medium speed (~40s) but inconsistent
   - `qwen2.5:14b`: Slowest (~62s) but most consistent JSON

## Recommendations

1. **Prompt Engineering**: The issue may be more about prompt design than model selection. All models are producing "summaries" rather than "comprehensive extractions."

2. **Consider chunking approach**: Instead of asking for one 1200-2000 word summary, break the content into sections and ask for detailed summaries of each section.

3. **Try different prompt strategies**:
   - Use few-shot examples showing the desired format and length
   - Be more explicit about "extract all useful information" vs "summarize"
   - Consider using a two-step process: extract key points, then expand each

4. **Model selection**: If forced to choose, `qwen2.5:14b` has the best JSON compliance, but none of the models meet the length requirement without further prompt engineering.


## Detailed Word Counts

From raw outputs (including any non-JSON text):
- **deepseek-llm:7b-32k**: 317 words
- **deepseek-r1:14b**: 182 words (when JSON failed)
- **qwen2.5:14b**: 351 words (from JSON summary field)

## Conclusion

**Winner: qwen2.5:14b** - Best JSON compliance and longest summaries, though still well below the 1200-2000 word target. All models need prompt engineering improvements to meet the length requirement.

**Recommendation**: Focus on prompt engineering rather than model selection. The models are capable of producing longer text (as seen in the raw outputs), but they're not following the length requirement in the structured JSON format.
