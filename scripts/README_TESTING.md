# Model Testing for Summarization

This directory contains tools for testing different LLM models on the summarization task.

## Setup

1. **Pull models** (if not already available):
   ```bash
   cd researcher/infra
   docker exec ollama ollama pull deepseek-llm:7b-32k
   docker exec ollama ollama pull qwen2.5:14b
   docker exec ollama ollama pull deepseek-r1:14b
   ```

2. **Build the test binary**:
   ```bash
   cd researcher
   go build -o bin/test_summarization cmd/test_summarization/main.go
   ```

## Running Tests

### Single Model Test

```bash
cd researcher
./bin/test_summarization \
  -raw ../gitopedia/Compendium/_debug/sources/solar-energy-technologies--earth-org-3-raw.txt \
  -topic "Solar Energy Technologies" \
  -url "https://earth.org/solar-energy/" \
  -model "deepseek-llm:7b-32k" \
  -output test_output
```

### Batch Testing Multiple Models

```bash
cd researcher
./scripts/test_models.sh
```

This will test all models listed in the script and save results to `test_output/`.

## Results

Results are saved as JSON files in the output directory with the following structure:

```json
{
  "model": "deepseek-llm:7b-32k",
  "topic": "Solar Energy Technologies",
  "url": "https://earth.org/solar-energy/",
  "relevant": true,
  "reason": "...",
  "word_count": 1234,
  "duration_ms": 5000,
  "summary": "...",
  "raw_output": "..."
}
```

## Comparing Models

Key metrics to compare:
- **Word count**: Should be 1200-2000 words (target)
- **Relevance**: Whether the model correctly identified relevant content
- **Duration**: Time taken for inference
- **JSON compliance**: Whether the output was valid JSON
- **Summary quality**: Comprehensiveness and accuracy

## Using Different Models for Different Tasks

You can configure per-task models in your `.env` file:

```bash
# Default model (fallback)
OPENAI_MODEL=deepseek-llm:7b-32k

# Per-task models (optional)
OPENAI_MODEL_GENERATE_ARTICLE=
OPENAI_MODEL_EXTRACT_ENTITIES=
OPENAI_MODEL_SUGGEST_TOPICS=
OPENAI_MODEL_SUMMARIZE_SOURCE=qwen2.5:14b  # Use larger model for summarization
```

If a per-task model is not specified, it falls back to `OPENAI_MODEL`.

