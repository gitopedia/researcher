#!/bin/bash
# Run summarization tests for all available models
# This script loops through models and tests each one

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESEARCHER_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
TEST_DIR="$SCRIPT_DIR/.."
OUTPUT_DIR="$TEST_DIR/test_output"
RAW_FILE="$RESEARCHER_DIR/../gitopedia/Compendium/_debug/sources/solar-energy-technologies--earth-org-3-raw.txt"

# Models to test (in order of size)
MODELS=(
    "qwen3:4b"
    "qwen3:8b"
    "gemma3:12b"
    "qwen3:14b"
    "qwen2.5:14b"
    "deepseek-r1:14b"
    "gemma3:27b"
    "qwen3:32b"
)

# Check if test binary exists, build if not
if [ ! -f "$RESEARCHER_DIR/bin/test_summarization" ]; then
    echo "Building test binary..."
    cd "$RESEARCHER_DIR"
    go build -o bin/test_summarization ./test/model_summarization/test_summarization/
fi

# Create output directory if needed
mkdir -p "$OUTPUT_DIR"

# Get list of available models
echo "Checking available models..."
AVAILABLE_MODELS=$(docker exec ollama ollama list | tail -n +2 | awk '{print $1}')

echo ""
echo "========================================"
echo "Starting model tests with updated prompts"
echo "========================================"
echo ""

for model in "${MODELS[@]}"; do
    echo "----------------------------------------"
    echo "Testing: $model"
    echo "----------------------------------------"
    
    # Check if model is available
    if ! echo "$AVAILABLE_MODELS" | grep -q "^${model}$"; then
        echo "⏭️  Model $model not available (still downloading?), skipping..."
        echo ""
        continue
    fi
    
    # Run the test
    "$RESEARCHER_DIR/bin/test_summarization" \
        -raw "$RAW_FILE" \
        -topic "Solar Energy Technologies" \
        -url "https://earth.org/solar-energy/" \
        -model "$model" \
        -output "$OUTPUT_DIR"
    
    echo ""
done

echo "========================================"
echo "All tests complete!"
echo "========================================"
echo ""
echo "Results saved to: $OUTPUT_DIR"
echo ""

# Generate quick summary
echo "Quick Summary:"
echo "-------------"
for model in "${MODELS[@]}"; do
    model_file=$(echo "$model" | tr ':' '-')
    result_file="$OUTPUT_DIR/${model_file}-results.json"
    if [ -f "$result_file" ]; then
        words=$(jq -r '.word_count' "$result_file" 2>/dev/null || echo "N/A")
        relevant=$(jq -r '.relevant' "$result_file" 2>/dev/null || echo "N/A")
        duration=$(jq -r '.duration_ms' "$result_file" 2>/dev/null || echo "N/A")
        if [ "$duration" != "N/A" ] && [ "$duration" != "null" ]; then
            duration_s=$((duration / 1000))
            echo "$model: ${words} words, relevant=${relevant}, ${duration_s}s"
        else
            echo "$model: ${words} words, relevant=${relevant}"
        fi
    else
        echo "$model: (not tested)"
    fi
done

