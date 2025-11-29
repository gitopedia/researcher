#!/bin/bash
# Test model summarization with GPU/CPU monitoring
# Usage: ./test_with_monitoring.sh [model_name] [raw_file]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RESEARCHER_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"
TEST_DIR="$SCRIPT_DIR/.."
OUTPUT_DIR="$TEST_DIR/test_output_monitoring"
RAW_FILE="${2:-$RESEARCHER_DIR/../gitopedia/Compendium/_debug/sources/solar-energy-technologies--earth-org-3-raw.txt}"
MODEL="${1:-qwen3:14b}"

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Build test binary
echo "Building test binary..."
cd "$RESEARCHER_DIR"
go build -o bin/test_stage1_summarize ./test/model_summarization/test_stage1_summarize/

# Check if model is available
echo "Checking if model $MODEL is available..."
if ! curl -s http://localhost:11434/api/tags | grep -q "\"name\":\"$MODEL\""; then
    echo "ERROR: Model $MODEL not found in Ollama"
    exit 1
fi

# Create monitoring output files
GPU_LOG="$OUTPUT_DIR/gpu_usage_${MODEL//:/_}.log"
CPU_LOG="$OUTPUT_DIR/cpu_usage_${MODEL//:/_}.log"
TIMING_LOG="$OUTPUT_DIR/timing_${MODEL//:/_}.log"

echo ""
echo "========================================"
echo "Testing model: $MODEL"
echo "Raw file: $RAW_FILE"
echo "Output directory: $OUTPUT_DIR"
echo "========================================"
echo ""

# Function to monitor GPU usage
monitor_gpu() {
    echo "timestamp,gpu_utilization(%),memory_used(MB),memory_total(MB),temperature(C),power_draw(W)" > "$GPU_LOG"
    while true; do
        nvidia-smi --query-gpu=timestamp,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw \
            --format=csv,noheader,nounits 2>/dev/null | \
            awk -F', ' '{print $1","$2","$3","$4","$5","$6}' >> "$GPU_LOG" || true
        sleep 0.5
    done
}

# Function to monitor CPU usage
monitor_cpu() {
    echo "timestamp,cpu_usage(%),memory_used(MB),memory_total(MB)" > "$CPU_LOG"
    while true; do
        timestamp=$(date +%s.%N)
        cpu=$(top -bn1 | grep "Cpu(s)" | sed "s/.*, *\([0-9.]*\)%* id.*/\1/" | awk '{print 100 - $1}')
        mem=$(free -m | awk 'NR==2{printf "%.1f,%.1f", $3,$2}')
        echo "$timestamp,$cpu,$mem" >> "$CPU_LOG"
        sleep 0.5
    done
}

# Start monitoring in background
echo "Starting GPU/CPU monitoring..."
monitor_gpu &
GPU_PID=$!
monitor_cpu &
CPU_PID=$!

# Cleanup function
cleanup() {
    echo ""
    echo "Stopping monitoring..."
    kill $GPU_PID 2>/dev/null || true
    kill $CPU_PID 2>/dev/null || true
    wait $GPU_PID 2>/dev/null || true
    wait $CPU_PID 2>/dev/null || true
}
trap cleanup EXIT

# Wait a moment for monitoring to start
sleep 1

# Record start time
START_TIME=$(date +%s.%N)

# Run the test
echo "Running summarization test..."
"$RESEARCHER_DIR/bin/test_stage1_summarize" \
    -raw "$RAW_FILE" \
    -topic "Solar Energy Technologies" \
    -url "https://earth.org/solar-energy/" \
    -model "$MODEL" \
    -output "$OUTPUT_DIR"

# Record end time
END_TIME=$(date +%s.%N)
DURATION=$(awk "BEGIN {printf \"%.2f\", $END_TIME - $START_TIME}")

# Stop monitoring
cleanup

# Wait a moment for logs to flush
sleep 1

# Extract results from test output
TEST_JSON="$OUTPUT_DIR/stage1-${MODEL//:/_}.json"
if [ -f "$TEST_JSON" ]; then
    WORDS=$(jq -r '.word_count' "$TEST_JSON" 2>/dev/null || echo "N/A")
    TEST_DURATION=$(jq -r '.duration_ms' "$TEST_JSON" 2>/dev/null || echo "N/A")
else
    WORDS="N/A"
    TEST_DURATION="N/A"
fi

# Calculate GPU statistics
if [ -f "$GPU_LOG" ] && [ $(wc -l < "$GPU_LOG") -gt 1 ]; then
    GPU_AVG=$(tail -n +2 "$GPU_LOG" | awk -F',' '{sum+=$2; count++} END {if(count>0) printf "%.1f", sum/count; else print "0"}')
    GPU_MAX=$(tail -n +2 "$GPU_LOG" | awk -F',' '{if($2>max) max=$2} END {print max+0}')
    MEM_AVG=$(tail -n +2 "$GPU_LOG" | awk -F',' '{sum+=$3; count++} END {if(count>0) printf "%.1f", sum/count; else print "0"}')
    MEM_MAX=$(tail -n +2 "$GPU_LOG" | awk -F',' '{if($3>max) max=$3} END {print max+0}')
    POWER_AVG=$(tail -n +2 "$GPU_LOG" | awk -F',' '{sum+=$6; count++} END {if(count>0) printf "%.1f", sum/count; else print "0"}')
else
    GPU_AVG="N/A"
    GPU_MAX="N/A"
    MEM_AVG="N/A"
    MEM_MAX="N/A"
    POWER_AVG="N/A"
fi

# Calculate CPU statistics
if [ -f "$CPU_LOG" ] && [ $(wc -l < "$CPU_LOG") -gt 1 ]; then
    CPU_AVG=$(tail -n +2 "$CPU_LOG" | awk -F',' '{sum+=$2; count++} END {if(count>0) printf "%.1f", sum/count; else print "0"}')
    CPU_MAX=$(tail -n +2 "$CPU_LOG" | awk -F',' '{if($2>max) max=$2} END {print max+0}')
else
    CPU_AVG="N/A"
    CPU_MAX="N/A"
fi

# Save timing information
cat > "$TIMING_LOG" <<EOF
Model: $MODEL
Test Duration: ${DURATION}s
Word Count: $WORDS
Test Duration (ms): $TEST_DURATION

GPU Statistics:
  Average GPU Utilization: ${GPU_AVG}%
  Maximum GPU Utilization: ${GPU_MAX}%
  Average Memory Used: ${MEM_AVG} MB
  Maximum Memory Used: ${MEM_MAX} MB
  Average Power Draw: ${POWER_AVG} W

CPU Statistics:
  Average CPU Usage: ${CPU_AVG}%
  Maximum CPU Usage: ${CPU_MAX}%
EOF

# Print summary
echo ""
echo "========================================"
echo "TEST RESULTS"
echo "========================================"
cat "$TIMING_LOG"
echo ""
echo "Detailed logs saved to:"
echo "  GPU: $GPU_LOG"
echo "  CPU: $CPU_LOG"
echo "  Timing: $TIMING_LOG"
echo ""

# Check if GPU was used
if [ "$GPU_AVG" != "N/A" ]; then
    GPU_CHECK=$(awk "BEGIN {if ($GPU_AVG > 5) print 1; else print 0}")
    if [ "$GPU_CHECK" -eq 1 ]; then
        echo "✅ GPU was actively used (avg: ${GPU_AVG}%)"
    else
        echo "⚠️  GPU usage was low or not detected (avg: ${GPU_AVG}%)"
    fi
else
    echo "⚠️  GPU monitoring data not available"
fi

echo ""

