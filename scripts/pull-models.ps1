# pull-models.ps1
# Pulls all models listed in model-list.txt from Ollama
# This script may take a long time to complete depending on your connection speed

param(
    [string]$ModelListPath = "$PSScriptRoot\..\model-list.txt",
    [string]$OllamaHost = "http://localhost:11434",
    [switch]$DryRun
)

Write-Host "=== Gitopedia Researcher - Model Puller ===" -ForegroundColor Cyan
Write-Host ""

# Check if Ollama is running
try {
    $response = Invoke-RestMethod -Uri "$OllamaHost/api/tags" -Method Get -ErrorAction Stop
    Write-Host "[OK] Ollama is running at $OllamaHost" -ForegroundColor Green
} catch {
    Write-Host "[ERROR] Cannot connect to Ollama at $OllamaHost" -ForegroundColor Red
    Write-Host "Make sure Ollama is running: ollama serve" -ForegroundColor Yellow
    exit 1
}

# Read model list
if (-not (Test-Path $ModelListPath)) {
    Write-Host "[ERROR] Model list not found: $ModelListPath" -ForegroundColor Red
    exit 1
}

$models = Get-Content $ModelListPath | Where-Object { 
    $_.Trim() -ne "" -and -not $_.StartsWith("#") 
}

Write-Host ""
Write-Host "Models to pull:" -ForegroundColor Yellow
$models | ForEach-Object { Write-Host "  - $_" }
Write-Host ""

if ($DryRun) {
    Write-Host "[DRY RUN] Would pull $($models.Count) models" -ForegroundColor Yellow
    exit 0
}

# Get already downloaded models
$existingModels = @()
try {
    $tags = Invoke-RestMethod -Uri "$OllamaHost/api/tags" -Method Get
    $existingModels = $tags.models | ForEach-Object { $_.name }
} catch {
    Write-Host "[WARN] Could not get existing models list" -ForegroundColor Yellow
}

$totalModels = $models.Count
$current = 0
$skipped = 0
$pulled = 0
$failed = 0

foreach ($model in $models) {
    $current++
    $model = $model.Trim()
    
    Write-Host ""
    Write-Host "[$current/$totalModels] Processing: $model" -ForegroundColor Cyan
    
    # Check if already exists
    $modelExists = $existingModels | Where-Object { $_ -eq $model -or $_ -like "$model*" }
    if ($modelExists) {
        Write-Host "  [SKIP] Model already downloaded" -ForegroundColor Yellow
        $skipped++
        continue
    }
    
    Write-Host "  [PULL] Downloading $model..." -ForegroundColor White
    $startTime = Get-Date
    
    try {
        # Use ollama CLI directly for better progress output
        $process = Start-Process -FilePath "ollama" -ArgumentList "pull", $model -NoNewWindow -Wait -PassThru
        
        if ($process.ExitCode -eq 0) {
            $duration = (Get-Date) - $startTime
            Write-Host "  [OK] Downloaded in $([math]::Round($duration.TotalMinutes, 1)) minutes" -ForegroundColor Green
            $pulled++
        } else {
            Write-Host "  [FAIL] ollama pull exited with code $($process.ExitCode)" -ForegroundColor Red
            $failed++
        }
    } catch {
        Write-Host "  [FAIL] Error: $_" -ForegroundColor Red
        $failed++
    }
}

Write-Host ""
Write-Host "=== Summary ===" -ForegroundColor Cyan
Write-Host "  Total models:  $totalModels"
Write-Host "  Pulled:        $pulled" -ForegroundColor Green
Write-Host "  Skipped:       $skipped" -ForegroundColor Yellow
Write-Host "  Failed:        $failed" -ForegroundColor $(if ($failed -gt 0) { "Red" } else { "White" })
Write-Host ""

if ($failed -gt 0) {
    exit 1
}









