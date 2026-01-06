# run-benchmark.ps1
# Runs the model summarization benchmark
# This will take a long time (potentially hours for all models)

param(
    [string]$OllamaHost = "http://localhost:11434",
    [switch]$Verbose,
    [string]$OutputDir = "experiments/benchmark_results"
)

$ErrorActionPreference = "Stop"
$startTime = Get-Date

Write-Host ""
Write-Host "╔══════════════════════════════════════════════════════════════╗" -ForegroundColor Cyan
Write-Host "║     GITOPEDIA RESEARCHER - MODEL SUMMARIZATION BENCHMARK     ║" -ForegroundColor Cyan
Write-Host "╚══════════════════════════════════════════════════════════════╝" -ForegroundColor Cyan
Write-Host ""
Write-Host "Started at: $startTime"
Write-Host ""

# Check if Ollama is running
Write-Host "Checking Ollama status..." -ForegroundColor Yellow
try {
    $ollamaStatus = Invoke-RestMethod -Uri "$OllamaHost/api/tags" -Method Get -ErrorAction Stop
    Write-Host "[OK] Ollama is running with $($ollamaStatus.models.Count) models loaded" -ForegroundColor Green
} catch {
    Write-Host "[ERROR] Cannot connect to Ollama at $OllamaHost" -ForegroundColor Red
    Write-Host "Make sure Ollama is running: ollama serve" -ForegroundColor Yellow
    exit 1
}

# List available models
Write-Host ""
Write-Host "Available models:" -ForegroundColor Yellow
$ollamaStatus.models | ForEach-Object { Write-Host "  - $($_.name)" }

# Check GPU status
Write-Host ""
Write-Host "Checking GPU availability..." -ForegroundColor Yellow
try {
    $nvidia = & nvidia-smi --query-gpu=name,memory.total,memory.free --format=csv,noheader 2>$null
    if ($nvidia) {
        Write-Host "[OK] NVIDIA GPU detected:" -ForegroundColor Green
        $nvidia -split "`n" | ForEach-Object { Write-Host "  $_" }
    }
} catch {
    Write-Host "[WARN] Could not detect NVIDIA GPU (nvidia-smi not available)" -ForegroundColor Yellow
}

# Navigate to researcher directory
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$researcherDir = Split-Path -Parent $scriptDir
Push-Location $researcherDir

try {
    Write-Host ""
    Write-Host "Creating output directory..." -ForegroundColor Yellow
    New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

    Write-Host ""
    Write-Host "═══════════════════════════════════════════════════════════════" -ForegroundColor Cyan
    Write-Host "Starting benchmark... This may take several hours!" -ForegroundColor Cyan
    Write-Host "═══════════════════════════════════════════════════════════════" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "Progress will be logged below. Individual model summaries will be"
    Write-Host "saved to: $OutputDir"
    Write-Host ""
    Write-Host "You can safely minimize this window - the benchmark will continue."
    Write-Host ""
    Write-Host "───────────────────────────────────────────────────────────────" -ForegroundColor DarkGray

    # Run the benchmark
    $logFile = "$OutputDir/benchmark_log_$(Get-Date -Format 'yyyyMMdd_HHmmss').txt"

    if ($Verbose) {
        go run experiments/model_benchmark.go 2>&1 | Tee-Object -FilePath $logFile
    } else {
        go run experiments/model_benchmark.go 2>&1 | Tee-Object -FilePath $logFile
    }

    $exitCode = $LASTEXITCODE

    Write-Host ""
    Write-Host "───────────────────────────────────────────────────────────────" -ForegroundColor DarkGray

    $endTime = Get-Date
    $duration = $endTime - $startTime

    if ($exitCode -eq 0) {
        Write-Host ""
        Write-Host "╔══════════════════════════════════════════════════════════════╗" -ForegroundColor Green
        Write-Host "║                    BENCHMARK COMPLETED!                      ║" -ForegroundColor Green
        Write-Host "╚══════════════════════════════════════════════════════════════╝" -ForegroundColor Green
        Write-Host ""
        Write-Host "Duration: $($duration.ToString('hh\:mm\:ss'))" -ForegroundColor Green
        Write-Host ""
        Write-Host "Results saved to:" -ForegroundColor Yellow
        Write-Host "  - $OutputDir/BENCHMARK_REPORT.md (Summary report)"
        Write-Host "  - $OutputDir/results.json (Raw data)"
        Write-Host "  - $OutputDir/*.md (Individual model summaries)"
        Write-Host "  - $logFile (Full log)"
        Write-Host ""

        # Show quick preview of report if it exists
        $reportPath = "$OutputDir/BENCHMARK_REPORT.md"
        if (Test-Path $reportPath) {
            Write-Host "═══════════════════════════════════════════════════════════════" -ForegroundColor Cyan
            Write-Host "RESULTS PREVIEW:" -ForegroundColor Cyan
            Write-Host "═══════════════════════════════════════════════════════════════" -ForegroundColor Cyan
            Get-Content $reportPath | Select-Object -First 50 | ForEach-Object { Write-Host $_ }
            Write-Host ""
            Write-Host "... (see full report in $reportPath)"
        }
    } else {
        Write-Host ""
        Write-Host "╔══════════════════════════════════════════════════════════════╗" -ForegroundColor Red
        Write-Host "║                    BENCHMARK FAILED!                         ║" -ForegroundColor Red
        Write-Host "╚══════════════════════════════════════════════════════════════╝" -ForegroundColor Red
        Write-Host ""
        Write-Host "Exit code: $exitCode" -ForegroundColor Red
        Write-Host "Check the log file for details: $logFile" -ForegroundColor Yellow
    }
} finally {
    Pop-Location
}








