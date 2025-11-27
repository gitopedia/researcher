# Qwen3 Models Information

## Available Qwen3 Models in Ollama

Based on user information, Qwen3 is available in the following sizes:
- **qwen3:8b** - 8 billion parameters
- **qwen3:14b** - 14 billion parameters  
- **qwen3:32b** - 32 billion parameters

## Comparison with Qwen2.5

Qwen3 is the newer generation of Qwen models, released in April 2025. It introduces:
- Hybrid reasoning capabilities
- Enhanced adaptability and efficiency
- Improved performance over Qwen2.5

## Testing Plan

Once downloaded, we should test:
1. **qwen3:8b** - Smaller, faster option (if 8B is available)
2. **qwen3:14b** - Same size as qwen2.5:14b for direct comparison
3. **qwen3:32b** - Largest option, likely best quality but slowest

## Expected Results

Qwen3 models should theoretically:
- Better follow JSON format requirements
- Produce longer, more comprehensive summaries
- Better understand the 1200-2000 word requirement

We'll compare against:
- `qwen2.5:14b` (current best performer: 414 words, good JSON compliance)
- `deepseek-llm:7b-32k` (fastest but no JSON)
- `deepseek-r1:14b` (inconsistent JSON, very short summaries)

