# Orvix Build Watchdog — Windows Server
# keeps Aider running until all tasks are complete
# Usage: .\watchdog.ps1

# ============================================================
# CONFIGURE THESE
$ProjectPath  = "C:\orvix"
$ApiKey       = "YOUR_DEEPSEEK_API_KEY_HERE"
# ============================================================

$LogFile      = "$ProjectPath\watchdog.log"
$MaxRestarts  = 999
$RestartCount = 0

function Write-Log {
    param($Message)
    $ts   = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
    $line = "[$ts] $Message"
    Write-Host $line -ForegroundColor Cyan
    Add-Content -Path $LogFile -Value $line
}

function Get-UncheckedCount {
    $content = Get-Content "$ProjectPath\MVP.md" -Raw -ErrorAction SilentlyContinue
    if (-not $content) { return 999 }
    return ($content | Select-String -Pattern "\[ \]" -AllMatches).Matches.Count
}

function Get-CheckedCount {
    $content = Get-Content "$ProjectPath\MVP.md" -Raw -ErrorAction SilentlyContinue
    if (-not $content) { return 0 }
    return ($content | Select-String -Pattern "\[x\]" -AllMatches).Matches.Count
}

function Start-Agent {
    $env:DEEPSEEK_API_KEY = $ApiKey
    Write-Log "Launching Aider (restart #$RestartCount)..."

    $proc = Start-Process -FilePath "aider" -ArgumentList @(
        "--model",                 "deepseek/deepseek-chat",
        "--yes",
        "--no-suggest-shell-commands",
        "--no-auto-commits",
        "--message",               "Read AGENT_INSTRUCTIONS.md completely. Then read MVP.md completely. Then read PROGRESS.md. Find the first unchecked task [ ] in the Build Order section of MVP.md. Execute it fully. Mark it [x]. Update PROGRESS.md. Move to the next task immediately. Never stop. Never ask questions. Never wait for input. Keep going until every [ ] is [x].",
        "--file",                  "AGENT_INSTRUCTIONS.md",
        "--file",                  "MVP.md",
        "--file",                  "PROGRESS.md"
    ) -WorkingDirectory $ProjectPath -PassThru -NoNewWindow

    return $proc
}

# ── Setup ────────────────────────────────────────────────────
if (-not (Test-Path $ProjectPath)) {
    New-Item -ItemType Directory -Path $ProjectPath | Out-Null
    Write-Log "Created $ProjectPath"
}

# Copy instruction files if they exist next to the script
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
foreach ($f in @("MVP.md","AGENT_INSTRUCTIONS.md","PROGRESS.md")) {
    $src = Join-Path $scriptDir $f
    $dst = Join-Path $ProjectPath $f
    if ((Test-Path $src) -and (-not (Test-Path $dst))) {
        Copy-Item $src $dst
        Write-Log "Copied $f to project folder"
    }
}

Write-Log "========================================="
Write-Log " Orvix Watchdog Started"
Write-Log " Project : $ProjectPath"
Write-Log " Unchecked tasks : $(Get-UncheckedCount)"
Write-Log "========================================="

$current = Start-Agent

# ── Main Loop ────────────────────────────────────────────────
while ($RestartCount -lt $MaxRestarts) {
    Start-Sleep -Seconds 15

    if ($current.HasExited) {
        $exit  = $current.ExitCode
        $left  = Get-UncheckedCount
        $done  = Get-CheckedCount

        Write-Log "Agent exited (code $exit) — done: $done  remaining: $left"

        if ($left -eq 0) {
            Write-Log "========================================="
            Write-Log " ALL TASKS COMPLETE — build finished!"
            Write-Log " Check HANDOFF.md for next steps."
            Write-Log "========================================="
            break
        }

        Write-Log "$left tasks still unchecked — restarting in 5 s..."
        Start-Sleep -Seconds 5
        $RestartCount++
        $current = Start-Agent
    }

    # Progress report every 5 minutes
    if (((Get-Date).Minute % 5 -eq 0) -and ((Get-Date).Second -lt 15)) {
        $left = Get-UncheckedCount
        $done = Get-CheckedCount
        Write-Log "Progress: $done done / $left remaining"
    }
}

Write-Log "Watchdog finished. Total restarts: $RestartCount"
