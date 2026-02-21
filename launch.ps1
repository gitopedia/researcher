# Gitopedia Researcher - Launch Script
# Starts both the API server and the dashboard

$ErrorActionPreference = "Stop"

# Colors for output
function Write-ColorOutput($ForegroundColor) {
    $fc = $host.UI.RawUI.ForegroundColor
    $host.UI.RawUI.ForegroundColor = $ForegroundColor
    if ($args) {
        Write-Output $args
    }
    $host.UI.RawUI.ForegroundColor = $fc
}

# Configuration
$REPO_PATH = "C:\Solus\Gitopedia\gitopedia"
$API_PORT = 3001
$DASHBOARD_PORT = 3000

# Check if repo path exists
if (-not (Test-Path $REPO_PATH)) {
    Write-ColorOutput Red "[ERROR] Repository path not found: $REPO_PATH"
    Write-ColorOutput Yellow "   Please update the REPO_PATH variable in launch.ps1"
    exit 1
}

# Check if Go binary exists
$BINARY_PATH = ".\bin\researcher.exe"
if (-not (Test-Path $BINARY_PATH)) {
    Write-ColorOutput Yellow "[WARN] Binary not found. Building..."
    go build -o $BINARY_PATH main.go
    if ($LASTEXITCODE -ne 0) {
        Write-ColorOutput Red "[ERROR] Build failed!"
        exit 1
    }
    Write-ColorOutput Green "[OK] Build successful!"
}

# Check if dashboard node_modules exists
if (-not (Test-Path ".\dashboard\node_modules")) {
    Write-ColorOutput Yellow "[WARN] Dashboard dependencies not found. Installing..."
    Push-Location dashboard
    npm install
    if ($LASTEXITCODE -ne 0) {
        Write-ColorOutput Red "[ERROR] npm install failed!"
        Pop-Location
        exit 1
    }
    Pop-Location
    Write-ColorOutput Green "[OK] Dependencies installed!"
}

Write-ColorOutput Cyan "`n[INFO] Launching Gitopedia Researcher..."
Write-ColorOutput Cyan "=========================================="

# Start API Server in a new window
Write-ColorOutput Yellow "`n[INFO] Starting API Server (port $API_PORT)..."
$apiProcess = Start-Process -FilePath $BINARY_PATH -ArgumentList "--server", "--repo-path", $REPO_PATH -PassThru -WindowStyle Normal

# Wait a moment for the server to start
Start-Sleep -Seconds 2

# Check if API server is running
if ($apiProcess.HasExited) {
    Write-ColorOutput Red "[ERROR] API Server failed to start!"
    exit 1
}

Write-ColorOutput Green "[OK] API Server started (PID: $($apiProcess.Id))"
Write-ColorOutput Gray "   API available at: http://127.0.0.1:$API_PORT"
Write-ColorOutput Gray "   Logs visible in the API Server window"

# Start Dashboard in a new window
Write-ColorOutput Yellow "`n[INFO] Starting Dashboard (port $DASHBOARD_PORT)..."
$dashboardPath = Resolve-Path "dashboard"
# Use cmd.exe to run npm (works better with Start-Process on Windows)
$dashboardProcess = Start-Process -FilePath "cmd.exe" -ArgumentList "/c", "cd /d `"$dashboardPath`" && npm run dev" -PassThru -WindowStyle Normal

# Wait a moment for the dashboard to start
Start-Sleep -Seconds 3

# Check if dashboard is running
if ($dashboardProcess.HasExited) {
    Write-ColorOutput Red "[ERROR] Dashboard failed to start!"
    Stop-Process -Id $apiProcess.Id -Force -ErrorAction SilentlyContinue
    exit 1
}

Write-ColorOutput Green "[OK] Dashboard started (PID: $($dashboardProcess.Id))"
Write-ColorOutput Gray "   Dashboard available at: http://localhost:$DASHBOARD_PORT"
Write-ColorOutput Gray "   Logs visible in the Dashboard window"

Write-ColorOutput Cyan "`n=========================================="
Write-ColorOutput Green "[OK] Both services are running!"
Write-ColorOutput Cyan "`n[INFO] Dashboard: http://localhost:$DASHBOARD_PORT"
Write-ColorOutput Cyan "[INFO] API Server: http://127.0.0.1:$API_PORT"
Write-ColorOutput Yellow "`n[INFO] Press Ctrl+C to stop both services`n"

# Function to cleanup on exit
function Cleanup {
    Write-ColorOutput Yellow "`n[INFO] Shutting down services..."
    
    if ($apiProcess -and -not $apiProcess.HasExited) {
        Write-ColorOutput Gray "   Stopping API Server..."
        Stop-Process -Id $apiProcess.Id -Force -ErrorAction SilentlyContinue
    }
    
    if ($dashboardProcess -and -not $dashboardProcess.HasExited) {
        Write-ColorOutput Gray "   Stopping Dashboard..."
        Stop-Process -Id $dashboardProcess.Id -Force -ErrorAction SilentlyContinue
    }
    
    Write-ColorOutput Green "[OK] All services stopped. Goodbye!`n"
    exit 0
}

# Register cleanup on Ctrl+C
$null = [Console]::TreatControlCAsInput = $false
Register-EngineEvent PowerShell.Exiting -Action { Cleanup } | Out-Null

# Wait for user to press Ctrl+C or for processes to exit
try {
    Write-ColorOutput Cyan "`n[INFO] Services are running. Monitoring for exit..."
    while ($true) {
        if ($apiProcess.HasExited -or $dashboardProcess.HasExited) {
            Write-ColorOutput Yellow "`n[WARNING] One of the services has exited."
            break
        }
        Start-Sleep -Seconds 1
    }
} catch {
    # Monitoring interrupted
}

Cleanup
