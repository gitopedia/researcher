#!/bin/bash
# Two-stage summarization test
# Stage 1: Test summarization quality (plain text output)
# Stage 2: Test JSON conversion reliability

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESEARCHER_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
TEST_DIR="$SCRIPT_DIR/.."
OUTPUT_DIR="$TEST_DIR/test_output_two_stage"
RAW_FILE="$RESEARCHER_DIR/../gitopedia/Compendium/_debug/sources/solar-energy-technologies--earth-org-3-raw.txt"

# Models to test for summarization (Stage 1)
SUMMARIZE_MODELS=(
    "qwen3:4b"
    "qwen3:8b"
    "gemma3:12b"
    "qwen3:14b"
    "qwen2.5:14b"
    "gemma3:27b"
    "qwen3:32b"
)

# Models to test for JSON conversion (Stage 2)
JSON_MODELS=(
    "gemma3:12b"
    "qwen2.5:14b"
    "qwen3:8b"
)

# Build test binaries
echo "Building test binaries..."
cd "$RESEARCHER_DIR"
go build -o bin/test_stage1_summarize ./test/model_summarization/test_stage1_summarize/
go build -o bin/test_stage2_json ./test/model_summarization/test_stage2_json/

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Get list of available models
echo "Checking available models..."
AVAILABLE_MODELS=$(docker exec ollama ollama list | tail -n +2 | awk '{print $1}')

echo ""
echo "========================================"
echo "STAGE 1: SUMMARIZATION TESTS"
echo "========================================"
echo ""

for model in "${SUMMARIZE_MODELS[@]}"; do
    echo "----------------------------------------"
    echo "Testing summarization: $model"
    echo "----------------------------------------"
    
    if ! echo "$AVAILABLE_MODELS" | grep -q "^${model}$"; then
        echo "⏭️  Model $model not available, skipping..."
        echo ""
        continue
    fi
    
    "$RESEARCHER_DIR/bin/test_stage1_summarize" \
        -raw "$RAW_FILE" \
        -topic "Solar Energy Technologies" \
        -url "https://earth.org/solar-energy/" \
        -model "$model" \
        -output "$OUTPUT_DIR"
    
    echo ""
done

echo ""
echo "========================================"
echo "STAGE 2: JSON CONVERSION TESTS"
echo "========================================"
echo ""

# For each summary from stage 1, test JSON conversion with each JSON model
for summary_file in "$OUTPUT_DIR"/stage1-*.txt; do
    [ -f "$summary_file" ] || continue
    
    source_model=$(basename "$summary_file" .txt | sed 's/stage1-//')
    
    for json_model in "${JSON_MODELS[@]}"; do
        echo "----------------------------------------"
        echo "Converting $source_model summary → JSON with $json_model"
        echo "----------------------------------------"
        
        if ! echo "$AVAILABLE_MODELS" | grep -q "^${json_model}$"; then
            echo "⏭️  Model $json_model not available, skipping..."
            echo ""
            continue
        fi
        
        "$RESEARCHER_DIR/bin/test_stage2_json" \
            -summary "$summary_file" \
            -model "$json_model" \
            -source-model "$source_model" \
            -output "$OUTPUT_DIR"
        
        echo ""
    done
done

echo ""
echo "========================================"
echo "ALL TESTS COMPLETE"
echo "========================================"
echo ""

# Generate summary report
echo "=== STAGE 1 SUMMARY (Summarization Quality) ==="
echo ""
printf "%-20s %10s %10s\n" "Model" "Words" "Duration"
printf "%-20s %10s %10s\n" "--------------------" "----------" "----------"
for f in "$OUTPUT_DIR"/stage1-*.json; do
    [ -f "$f" ] || continue
    model=$(jq -r '.model' "$f")
    words=$(jq -r '.word_count' "$f")
    duration=$(jq -r '.duration_ms' "$f")
    duration_s=$((duration / 1000))
    printf "%-20s %10s %10ss\n" "$model" "$words" "$duration_s"
done

echo ""
echo "=== STAGE 2 SUMMARY (JSON Conversion Reliability) ==="
echo ""
printf "%-20s %-20s %10s %10s\n" "Source" "Converter" "Valid" "Duration"
printf "%-20s %-20s %10s %10s\n" "--------------------" "--------------------" "----------" "----------"
for f in "$OUTPUT_DIR"/stage2-*.json; do
    [ -f "$f" ] || continue
    source=$(jq -r '.source_model' "$f")
    converter=$(jq -r '.converter_model' "$f")
    valid=$(jq -r '.json_valid' "$f")
    duration=$(jq -r '.duration_ms' "$f")
    duration_s=$((duration / 1000))
    printf "%-20s %-20s %10s %10ss\n" "$source" "$converter" "$valid" "$duration_s"
done

echo ""
echo "Results saved to: $OUTPUT_DIR"

