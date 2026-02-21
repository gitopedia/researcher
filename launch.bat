@echo off
REM Gitopedia Researcher - Launch Script (Windows Batch)
REM Starts both the API server and the dashboard

setlocal

set REPO_PATH=C:\Solus\Gitopedia\gitopedia
set BINARY_PATH=bin\researcher.exe

REM Check if repo path exists
if not exist "%REPO_PATH%" (
    echo ❌ Error: Repository path not found: %REPO_PATH%
    echo    Please update the REPO_PATH variable in launch.bat
    exit /b 1
)

REM Check if binary exists, build if not
if not exist "%BINARY_PATH%" (
    echo ⚠️  Binary not found. Building...
    go build -o %BINARY_PATH% main.go
    if errorlevel 1 (
        echo ❌ Build failed!
        exit /b 1
    )
    echo ✅ Build successful!
)

REM Check if dashboard dependencies exist
if not exist "dashboard\node_modules" (
    echo ⚠️  Dashboard dependencies not found. Installing...
    cd dashboard
    call npm install
    if errorlevel 1 (
        echo ❌ npm install failed!
        cd ..
        exit /b 1
    )
    cd ..
    echo ✅ Dependencies installed!
)

echo.
echo 🚀 Launching Gitopedia Researcher...
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo.
echo 📡 Starting API Server (port 3001)...
start "Gitopedia API Server" cmd /c "%BINARY_PATH% --server --repo-path %REPO_PATH%"

timeout /t 2 /nobreak >nul

echo ✅ API Server started
echo    API available at: http://127.0.0.1:3001
echo.
echo 🎨 Starting Dashboard (port 3000)...
cd dashboard
start "Gitopedia Dashboard" cmd /c "npm run dev"
cd ..

timeout /t 3 /nobreak >nul

echo ✅ Dashboard started
echo    Dashboard available at: http://localhost:3000
echo.
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo ✨ Both services are running!
echo.
echo 📊 Dashboard: http://localhost:3000
echo 🔌 API Server: http://127.0.0.1:3001
echo.
echo 💡 Close the command windows to stop the services
echo.

pause
