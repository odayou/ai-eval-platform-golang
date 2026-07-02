Write-Host "AI Eval Platform - Starting..." -ForegroundColor Green
$ErrorActionPreference = "Stop"
Write-Host "[1/4] Building frontend..."
Set-Location web
npm install
npm run build
Set-Location ..
Write-Host "[2/4] Building backend..."
go build -o server.exe ./cmd/server/
Write-Host "[3/4] Starting server..."
.\server.exe
