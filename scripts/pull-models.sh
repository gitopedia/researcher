#!/bin/bash
# pull-models.sh
# Pulls all models listed in model-list.txt from Ollama
# This script may take a long time to complete depending on your connection speed

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODEL_LIST="${MODEL_LIST:-$SCRIPT_DIR/../model-list.txt}"
OLLAMA_HOST="${OLLAMA_HOST:-http://localhost:11434}"
DRY_RUN="${DRY_RUN:-false}"

echo "=== Gitopedia Researcher - Model Puller ==="
echo ""

# Check if Ollama is running
if ! curl -s "$OLLAMA_HOST/api/tags" > /dev/null 2>&1; then
    echo "[ERROR] Cannot connect to Ollama at $OLLAMA_HOST"
    echo "Make sure Ollama is running: ollama serve"
    exit 1
fi
echo "[OK] Ollama is running at $OLLAMA_HOST"

# Read model list
if [ ! -f "$MODEL_LIST" ]; then
    echo "[ERROR] Model list not found: $MODEL_LIST"
    exit 1
fi

# Parse models (skip empty lines and comments)
mapfile -t MODELS < <(grep -v '^#' "$MODEL_LIST" | grep -v '^$' | tr -d '\r')

echo ""
echo "Models to pull:"
for model in "${MODELS[@]}"; do
    echo "  - $model"
done
echo ""

if [ "$DRY_RUN" = "true" ]; then
    echo "[DRY RUN] Would pull ${#MODELS[@]} models"
    exit 0
fi

# Get already downloaded models
EXISTING_MODELS=$(curl -s "$OLLAMA_HOST/api/tags" | jq -r '.models[].name' 2>/dev/null || echo "")

TOTAL=${#MODELS[@]}
CURRENT=0
SKIPPED=0
PULLED=0
FAILED=0

for model in "${MODELS[@]}"; do
    CURRENT=$((CURRENT + 1))
    model=$(echo "$model" | tr -d '[:space:]')
    
    echo ""
    echo "[$CURRENT/$TOTAL] Processing: $model"
    
    # Check if already exists
    if echo "$EXISTING_MODELS" | grep -q "^${model}"; then
        echo "  [SKIP] Model already downloaded"
        SKIPPED=$((SKIPPED + 1))
        continue
    fi
    
    echo "  [PULL] Downloading $model..."
    START_TIME=$(date +%s)
    
    if ollama pull "$model"; then
        END_TIME=$(date +%s)
        DURATION=$((END_TIME - START_TIME))
        MINUTES=$((DURATION / 60))
        echo "  [OK] Downloaded in ${MINUTES} minutes"
        PULLED=$((PULLED + 1))
    else
        echo "  [FAIL] Failed to pull $model"
        FAILED=$((FAILED + 1))
    fi
done

echo ""
echo "=== Summary ==="
echo "  Total models:  $TOTAL"
echo "  Pulled:        $PULLED"
echo "  Skipped:       $SKIPPED"
echo "  Failed:        $FAILED"
echo ""

if [ $FAILED -gt 0 ]; then
    exit 1
fi




