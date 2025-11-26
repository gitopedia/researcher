#!/bin/bash
# Two-stage summarization tests across all _debug sources
# Stage 1: Plain-text summarization (structure-focused)
# Stage 2: JSON conversion (using a single converter model)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESEARCHER_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
TEST_DIR="$SCRIPT_DIR/.."
OUTPUT_BASE="$TEST_DIR/test_output_two_stage"

# Path to raw sources in gitopedia repo
RAW_DIR="$RESEARCHER_DIR/../gitopedia/Compendium/_debug/sources"

# Summarization models (Stage 1)
SUMMARIZE_MODELS=(
  "gemma3:12b"
  "gemma3:27b"
  "qwen3:14b"
  "qwen3:32b"
)

# JSON conversion model (Stage 2)
CONVERTER_MODEL="gemma3:12b"

TOPIC="Solar Energy Technologies"
BASE_URL="https://earth.org/solar-energy/"

echo "Building test binaries..."
cd "$RESEARCHER_DIR"
go build -o bin/test_stage1_summarize ./test/model_summarization/test_stage1_summarize/
go build -o bin/test_stage2_json ./test/model_summarization/test_stage2_json/

mkdir -p "$OUTPUT_BASE"

echo ""
echo "========================================"
echo "Scanning sources in: $RAW_DIR"
echo "========================================"
echo ""

mapfile -t RAW_FILES < <(find "$RAW_DIR" -maxdepth 1 -type f -name '*-raw.txt' | sort)

if [ "${#RAW_FILES[@]}" -eq 0 ]; then
  echo "No raw source files found in $RAW_DIR"
  exit 1
fi

echo "Found ${#RAW_FILES[@]} raw source files."
echo ""

echo "========================================"
echo "Stage 1: Summarization (per source x model)"
echo "========================================"
echo ""

for raw in "${RAW_FILES[@]}"; do
  base="$(basename "$raw")"
  slug="${base%-raw.txt}"
  OUT_DIR="$OUTPUT_BASE/$slug"
  mkdir -p "$OUT_DIR"

  echo "---- Source: $base ----"

  for model in "${SUMMARIZE_MODELS[@]}"; do
    echo "  Summarizing with $model..."
    "$RESEARCHER_DIR/bin/test_stage1_summarize" \
      -raw "$raw" \
      -topic "$TOPIC" \
      -url "$BASE_URL" \
      -model "$model" \
      -output "$OUT_DIR"
    echo ""
  done
done

echo ""
echo "========================================"
echo "Stage 2: JSON conversion (converter: $CONVERTER_MODEL)"
echo "========================================"
echo ""

for raw in "${RAW_FILES[@]}"; do
  base="$(basename "$raw")"
  slug="${base%-raw.txt}"
  OUT_DIR="$OUTPUT_BASE/$slug"

  echo "---- Source: $base ----"

  for summary_file in "$OUT_DIR"/stage1-*.txt; do
    [ -f "$summary_file" ] || continue
    src_model="$(basename "$summary_file" .txt | sed 's/^stage1-//')"

    echo "  Converting summary from $src_model -> JSON with $CONVERTER_MODEL..."
    "$RESEARCHER_DIR/bin/test_stage2_json" \
      -summary "$summary_file" \
      -model "$CONVERTER_MODEL" \
      -source-model "$src_model" \
      -output "$OUT_DIR"
    echo ""
  done
done

echo ""
echo "========================================"
echo "Summary Tables"
echo "========================================"
echo ""

echo "Stage 1 – Summarization (words & duration per source/model)"
printf "%-40s %-14s %10s %10s\n" "Source" "Model" "Words" "Duration"
printf "%-40s %-14s %10s %10s\n" "----------------------------------------" "--------------" "----------" "----------"

for raw in "${RAW_FILES[@]}"; do
  base="$(basename "$raw")"
  slug="${base%-raw.txt}"
  OUT_DIR="$OUTPUT_BASE/$slug"

  for f in "$OUT_DIR"/stage1-*.json; do
    [ -f "$f" ] || continue
    model=$(jq -r '.model' "$f")
    words=$(jq -r '.word_count' "$f")
    dur_ms=$(jq -r '.duration_ms' "$f")
    dur_s=$((dur_ms / 1000))
    printf "%-40s %-14s %10s %10ss\n" "$slug" "$model" "$words" "$dur_s"
  done
done

echo ""
echo "Stage 2 – JSON validity (per source/model via converter $CONVERTER_MODEL)"
printf "%-40s %-14s %10s %10s\n" "Source" "SourceModel" "JSON" "Duration"
printf "%-40s %-14s %10s %10s\n" "----------------------------------------" "--------------" "----------" "----------"

for raw in "${RAW_FILES[@]}"; do
  base="$(basename "$raw")"
  slug="${base%-raw.txt}"
  OUT_DIR="$OUTPUT_BASE/$slug"

  for f in "$OUT_DIR"/stage2-*.json; do
    [ -f "$f" ] || continue
    src=$(jq -r '.source_model' "$f")
    valid=$(jq -r '.json_valid' "$f")
    dur_ms=$(jq -r '.duration_ms' "$f")
    dur_s=$((dur_ms / 1000))
    printf "%-40s %-14s %10s %10ss\n" "$slug" "$src" "$valid" "$dur_s"
  done
done

echo ""
echo "All results written under: $OUTPUT_BASE"


