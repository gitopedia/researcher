#!/bin/bash
# run-benchmark.sh
# Runs the model summarization benchmark
# This will take a long time (potentially hours for all models)

set -e

OLLAMA_HOST="${OLLAMA_HOST:-http://localhost:11434}"
OUTPUT_DIR="${OUTPUT_DIR:-experiments/benchmark_results}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESEARCHER_DIR="$(dirname "$SCRIPT_DIR")"

START_TIME=$(date +%s)
START_DATE=$(date "+%Y-%m-%d %H:%M:%S")

echo ""
echo "╔══════════════════════════════════════════════════════════════╗"
echo "║     GITOPEDIA RESEARCHER - MODEL SUMMARIZATION BENCHMARK     ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "Started at: $START_DATE"
echo ""

# Check if Ollama is running
echo "Checking Ollama status..."
if ! curl -s "$OLLAMA_HOST/api/tags" > /dev/null 2>&1; then
    echo "[ERROR] Cannot connect to Ollama at $OLLAMA_HOST"
    echo "Make sure Ollama is running: ollama serve"
    exit 1
fi
echo "[OK] Ollama is running"

# List available models
echo ""
echo "Available models:"
curl -s "$OLLAMA_HOST/api/tags" | jq -r '.models[].name' 2>/dev/null | while read model; do
    echo "  - $model"
done

# Check GPU
echo ""
echo "Checking GPU availability..."
if command -v nvidia-smi &> /dev/null; then
    nvidia-smi --query-gpu=name,memory.total,memory.free --format=csv,noheader 2>/dev/null && echo "[OK] NVIDIA GPU detected" || echo "[WARN] nvidia-smi failed"
else
    echo "[WARN] nvidia-smi not available"
fi

# Navigate to researcher directory
cd "$RESEARCHER_DIR"

# Create output directory
mkdir -p "$OUTPUT_DIR"

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "Starting benchmark... This may take several hours!"
echo "═══════════════════════════════════════════════════════════════"
echo ""
echo "Progress will be logged below. Individual model summaries will be"
echo "saved to: $OUTPUT_DIR"
echo ""
echo "You can safely background this process with Ctrl+Z then 'bg'"
echo ""
echo "───────────────────────────────────────────────────────────────"

# Run the benchmark
LOG_FILE="$OUTPUT_DIR/benchmark_log_$(date +%Y%m%d_%H%M%S).txt"
go run experiments/model_benchmark.go 2>&1 | tee "$LOG_FILE"
EXIT_CODE=${PIPESTATUS[0]}

echo ""
echo "───────────────────────────────────────────────────────────────"

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))
HOURS=$((DURATION / 3600))
MINUTES=$(((DURATION % 3600) / 60))
SECONDS=$((DURATION % 60))

if [ $EXIT_CODE -eq 0 ]; then
    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                    BENCHMARK COMPLETED!                      ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""
    printf "Duration: %02d:%02d:%02d\n" $HOURS $MINUTES $SECONDS
    echo ""
    echo "Results saved to:"
    echo "  - $OUTPUT_DIR/BENCHMARK_REPORT.md (Summary report)"
    echo "  - $OUTPUT_DIR/results.json (Raw data)"
    echo "  - $OUTPUT_DIR/*.md (Individual model summaries)"
    echo "  - $LOG_FILE (Full log)"
else
    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║                    BENCHMARK FAILED!                         ║"
    echo "╚══════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Exit code: $EXIT_CODE"
    echo "Check the log file for details: $LOG_FILE"
    exit $EXIT_CODE
fi



