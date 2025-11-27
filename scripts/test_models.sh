#!/bin/bash
# Test summarization with different models using raw files from PR #32

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESEARCHER_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
GITOPEDIA_DIR="$(cd "$RESEARCHER_DIR/../gitopedia" && pwd)"
OUTPUT_DIR="$RESEARCHER_DIR/test_output"

# Models to test (adjust based on what's available)
MODELS=(
    "deepseek-llm:7b-32k"
    "qwen2.5:14b"
    "qwen3:8b"
    "qwen3:14b"
    "qwen3:32b"
    "deepseek-r1:14b"
)

# Test file (use first raw file found)
RAW_FILE="$GITOPEDIA_DIR/Compendium/_debug/sources/solar-energy-technologies--earth-org-3-raw.txt"
TOPIC="Solar Energy Technologies"
URL="https://earth.org/solar-energy/"

if [ ! -f "$RAW_FILE" ]; then
    echo "Error: Raw file not found: $RAW_FILE"
    exit 1
fi

echo "Testing summarization with multiple models"
echo "Raw file: $RAW_FILE"
echo "Topic: $TOPIC"
echo "Output directory: $OUTPUT_DIR"
echo ""

# Build test binary
echo "Building test binary..."
cd "$RESEARCHER_DIR"
go build -o "$RESEARCHER_DIR/bin/test_summarization" "$RESEARCHER_DIR/cmd/test_summarization/main.go"

# Test each model
for model in "${MODELS[@]}"; do
    echo ""
    echo "=========================================="
    echo "Testing model: $model"
    echo "=========================================="
    
    # Check if model is available
    if ! docker exec ollama ollama list | grep -q "$model"; then
        echo "WARNING: Model $model not found, skipping..."
        continue
    fi
    
    "$RESEARCHER_DIR/bin/test_summarization" \
        -raw "$RAW_FILE" \
        -topic "$TOPIC" \
        -url "$URL" \
        -model "$model" \
        -output "$OUTPUT_DIR"
    
    echo "Completed: $model"
    sleep 2  # Brief pause between tests
done

echo ""
echo "=========================================="
echo "All tests completed!"
echo "Results saved to: $OUTPUT_DIR"
echo "=========================================="

