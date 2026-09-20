<#
.SYNOPSIS
  Exports a daily time window across a date range from an NVR, as nvrclip chunks.

.DESCRIPTION
  nvrclip exports one clip per invocation. A multi-day pull is far safer as many
  short chunks than as one enormous clip: each chunk is independently verifiable,
  a failure costs one chunk instead of the whole run, and the job is resumable.

  For each date from -StartDate to -EndDate (both inclusive), the window
  -DailyFrom .. -DailyTo is exported in -ChunkMinutes pieces. Output is foldered
  by day. Re-running skips chunks whose output already exists, so an interrupted
  run resumes where it stopped.

  -Detach re-launches the script as an independent hidden process and returns
  immediately. The detached run survives the launching shell. Progress is written
  to a console log and a CSV alongside the output.

.EXAMPLE
  .\bulk-export.ps1 -Nvr nvr -Channel 1 -StartDate "2026-09-06" -EndDate "2026-09-10" `
    -DailyFrom "08:00" -DailyTo "19:00" -ChunkMinutes 180 -OutRoot "D:\nvr-export" -Detach
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Nvr,
    [Parameter(Mandatory)][string]$Channel,
    [datetime]$StartDate,
    [datetime]$EndDate,
    [string]$DailyFrom = '00:00',
    [string]$DailyTo = '24:00',
    [datetime]$From,
    [datetime]$To,
    [int]$ChunkMinutes = 180,
    [string]$OutRoot = '.\export',
    [ValidateSet('copy', 'exact')][string]$Mode = 'copy',
    [string]$Config = 'nvrclip.local.toml',
    [string]$WorkDir = '',
    [int]$Retries = 2,
    [switch]$AutoTimeOffset,
    [switch]$DryRun,
    [switch]$Detach
)

$ErrorActionPreference = 'Stop'

$exe = Join-Path $PSScriptRoot 'nvrclip.exe'
if (-not (Test-Path $exe)) {
    throw "nvrclip.exe not found at $exe. Build it with: go run ./tools/build --version 0.2.2"
}
$continuous = $PSBoundParameters.ContainsKey('From') -or $PSBoundParameters.ContainsKey('To')
if ($continuous) {
    if (-not ($PSBoundParameters.ContainsKey('From') -and $PSBoundParameters.ContainsKey('To'))) {
        throw "-From and -To must be given together."
    }
    if ($To -le $From) { throw "-To must be later than -From." }
} else {
    if (-not $StartDate -or -not $EndDate) {
        throw "Give either -From and -To, or -StartDate and -EndDate."
    }
    if ($EndDate.Date -lt $StartDate.Date) { throw "-EndDate must not be earlier than -StartDate." }
}
if ($ChunkMinutes -le 0) { throw "-ChunkMinutes must be positive." }

# "24:00" is not a valid TimeSpan; treat it as the end of the day.
function ConvertTo-DayOffset([string]$hhmm) {
    if ($hhmm -eq '24:00') { return [timespan]::FromHours(24) }
    $ts = [timespan]::Zero
    if (-not [timespan]::TryParse($hhmm, [ref]$ts)) {
        throw "Could not parse time-of-day '$hhmm'. Use HH:mm, e.g. 08:00."
    }
    return $ts
}

$fromOffset = ConvertTo-DayOffset $DailyFrom
$toOffset = ConvertTo-DayOffset $DailyTo
if ($toOffset -le $fromOffset) { throw "-DailyTo must be later than -DailyFrom." }

$null = New-Item -ItemType Directory -Force -Path $OutRoot
$OutRoot = (Resolve-Path $OutRoot).Path

# --- Detached relaunch -------------------------------------------------------
if ($Detach) {
    $stamp = Get-Date -Format 'yyyyMMdd_HHmmss'
    $consoleLog = Join-Path $OutRoot "run_$stamp.log"
    $pidFile = Join-Path $OutRoot 'run.pid'

    $childArgs = @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $PSCommandPath,
        '-Nvr', $Nvr, '-Channel', $Channel,
        '-ChunkMinutes', $ChunkMinutes,
        '-OutRoot', $OutRoot, '-Mode', $Mode,
        '-Config', (Resolve-Path $Config).Path,
        '-Retries', $Retries
    )
    if ($continuous) {
        $childArgs += @('-From', $From.ToString('yyyy-MM-dd HH:mm:ss'), '-To', $To.ToString('yyyy-MM-dd HH:mm:ss'))
    } else {
        $childArgs += @(
            '-StartDate', $StartDate.ToString('yyyy-MM-dd'),
            '-EndDate', $EndDate.ToString('yyyy-MM-dd'),
            '-DailyFrom', $DailyFrom, '-DailyTo', $DailyTo
        )
    }
    if ($AutoTimeOffset) { $childArgs += '-AutoTimeOffset' }
    if ($WorkDir) { $childArgs += @('-WorkDir', $WorkDir) }

    $proc = Start-Process -FilePath 'powershell.exe' -ArgumentList $childArgs `
        -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput $consoleLog `
        -RedirectStandardError (Join-Path $OutRoot "run_$stamp.err.log")

    Set-Content -LiteralPath $pidFile -Value $proc.Id -Encoding ascii
    Write-Host "Detached export started."
    Write-Host "  PID     : $($proc.Id)  (saved to $pidFile)"
    Write-Host "  Log     : $consoleLog"
    Write-Host "  Output  : $OutRoot"
    Write-Host ""
    Write-Host "Monitor with:"
    Write-Host "  Get-Content '$consoleLog' -Wait -Tail 20"
    Write-Host "Stop with:"
    Write-Host "  Stop-Process -Id $($proc.Id)"
    return
}

# --- Chunk planning ----------------------------------------------------------
# Mirror internal/clip.slug so expected output names can be predicted for resume.
function ConvertTo-Slug([string]$s) {
    $s = $s.Trim().ToLowerInvariant()
    $s = [regex]::Replace($s, '[^a-z0-9]+', '_')
    $s = $s.Trim('_')
    if ($s -eq '') { return 'clip' }
    return $s
}

# Mirror cmd/nvrclip outputLabel: numeric channels render as "<nvr> channel <n>".
$label = if ($Channel -match '^\d+$') { "$Nvr channel $Channel" } else { "$Nvr $Channel" }
$slug = ConvertTo-Slug $label

$chunks = @()
if ($continuous) {
    # One unbroken span. Chunks are foldered by the day they start on, so a run
    # crossing midnight files each chunk under the date it belongs to.
    $cursor = $From
    while ($cursor -lt $To) {
        $stop = $cursor.AddMinutes($ChunkMinutes)
        if ($stop -gt $To) { $stop = $To }
        $chunks += [pscustomobject]@{ Day = $cursor.Date; From = $cursor; To = $stop }
        $cursor = $stop
    }
} else {
    for ($day = $StartDate.Date; $day -le $EndDate.Date; $day = $day.AddDays(1)) {
        $windowStart = $day.Add($fromOffset)
        $windowEnd = $day.Add($toOffset)
        $cursor = $windowStart
        while ($cursor -lt $windowEnd) {
            $stop = $cursor.AddMinutes($ChunkMinutes)
            if ($stop -gt $windowEnd) { $stop = $windowEnd }
            $chunks += [pscustomobject]@{ Day = $day; From = $cursor; To = $stop }
            $cursor = $stop
        }
    }
}

$logPath = Join-Path $OutRoot ("bulk-export_{0:yyyyMMdd_HHmmss}.csv" -f (Get-Date))
'start,end,status,bytes,seconds,detail' | Set-Content -LiteralPath $logPath -Encoding utf8

Write-Host "nvrclip bulk export"
Write-Host "  nvr/channel : $Nvr / $Channel  (label '$label')"
if ($continuous) {
    Write-Host "  range       : $($From.ToString('yyyy-MM-dd HH:mm')) -> $($To.ToString('yyyy-MM-dd HH:mm'))"
} else {
    Write-Host "  dates       : $($StartDate.ToString('yyyy-MM-dd')) .. $($EndDate.ToString('yyyy-MM-dd')) inclusive"
    Write-Host "  daily window: $DailyFrom - $DailyTo"
}
Write-Host "  chunks      : $($chunks.Count) x up to $ChunkMinutes min, mode=$Mode"
Write-Host "  output      : $OutRoot"
Write-Host "  csv log     : $logPath"
Write-Host "  started     : $((Get-Date).ToString('yyyy-MM-dd HH:mm:ss'))"
Write-Host ""

$ok = 0; $skipped = 0; $failed = 0; $empty = 0
$failedChunks = @()
$runStart = Get-Date
$i = 0

foreach ($chunk in $chunks) {
    $i++
    $tag = "[{0}/{1}] {2} -> {3}" -f $i, $chunks.Count,
        $chunk.From.ToString('yyyy-MM-dd HH:mm'), $chunk.To.ToString('HH:mm')

    $dayDir = Join-Path $OutRoot $chunk.Day.ToString('yyyy-MM-dd')
    $expected = Join-Path $dayDir ("{0}_{1}-{2}.mp4" -f $slug,
        $chunk.From.ToString('yyyy-MM-dd_HHmm'), $chunk.To.ToString('HHmm'))

    if ((Test-Path -LiteralPath $expected) -and (Get-Item -LiteralPath $expected).Length -gt 0) {
        Write-Host "$tag  skip (already exported)"
        ('{0},{1},skipped,{2},0,' -f $chunk.From.ToString('s'), $chunk.To.ToString('s'),
            (Get-Item -LiteralPath $expected).Length) | Add-Content -LiteralPath $logPath
        $skipped++
        continue
    }

    $nvrArgs = @(
        'download', $Nvr,
        '--channel', $Channel,
        '--from', $chunk.From.ToString('yyyy-MM-dd HH:mm'),
        '--to', $chunk.To.ToString('yyyy-MM-dd HH:mm'),
        '--out', $dayDir,
        '--config', $Config,
        '--mode', $Mode
    )
    if ($AutoTimeOffset) { $nvrArgs += '--auto-time-offset' }
    if ($WorkDir) { $nvrArgs += @('--work-dir', $WorkDir) }

    if ($DryRun) {
        Write-Host "$tag  dry-run: nvrclip.exe $($nvrArgs -join ' ')"
        continue
    }

    $null = New-Item -ItemType Directory -Force -Path $dayDir
    Write-Host "$tag  exporting..."

    $attempt = 0
    $done = $false
    $lastErr = ''
    $chunkStart = Get-Date

    while (-not $done -and $attempt -le $Retries) {
        $attempt++
        & $exe @nvrArgs 2>&1 | ForEach-Object { Write-Host "    $_" }
        if ($LASTEXITCODE -eq 0) {
            $done = $true
        }
        else {
            $lastErr = "exit code $LASTEXITCODE"
            if ($attempt -le $Retries) {
                Write-Host "  ! $tag attempt $attempt failed ($lastErr); retrying in 10s"
                Start-Sleep -Seconds 10
            }
        }
    }

    $elapsed = [int]((Get-Date) - $chunkStart).TotalSeconds

    if ($done) {
        $size = if (Test-Path -LiteralPath $expected) { (Get-Item -LiteralPath $expected).Length } else { 0 }
        if ($size -eq 0) {
            # Exit 0 but no predicted file: usually means no recording covered this window.
            Write-Host "$tag  ok, but no output file (likely no recording in this window)"
            ('{0},{1},empty,0,{2},no output file' -f $chunk.From.ToString('s'), $chunk.To.ToString('s'), $elapsed) |
                Add-Content -LiteralPath $logPath
            $empty++
        }
        else {
            Write-Host ("$tag  done ({0:N1} MB, {1}s)" -f ($size / 1MB), $elapsed)
            ('{0},{1},ok,{2},{3},' -f $chunk.From.ToString('s'), $chunk.To.ToString('s'), $size, $elapsed) |
                Add-Content -LiteralPath $logPath
            $ok++
        }
    }
    else {
        Write-Host "$tag  FAILED after $attempt attempts ($lastErr)"
        ('{0},{1},failed,0,{2},{3}' -f $chunk.From.ToString('s'), $chunk.To.ToString('s'), $elapsed, $lastErr) |
            Add-Content -LiteralPath $logPath
        $failed++
        $failedChunks += $chunk
    }
}

$totalElapsed = (Get-Date) - $runStart
$totalBytes = (Get-ChildItem -LiteralPath $OutRoot -Recurse -Filter *.mp4 -ErrorAction SilentlyContinue |
    Measure-Object -Property Length -Sum).Sum

Write-Host ""
Write-Host "Summary: $ok exported, $skipped skipped, $empty empty, $failed failed"
Write-Host ("Total on disk: {0:N1} GB in {1} min" -f ($totalBytes / 1GB), [int]$totalElapsed.TotalMinutes)
Write-Host "Finished: $((Get-Date).ToString('yyyy-MM-dd HH:mm:ss'))"
Write-Host "CSV log: $logPath"

if ($failedChunks.Count -gt 0) {
    Write-Host ""
    Write-Host "Failed windows (re-run the same command to retry only these):"
    foreach ($c in $failedChunks) {
        Write-Host ("  {0} -> {1}" -f $c.From.ToString('yyyy-MM-dd HH:mm'), $c.To.ToString('yyyy-MM-dd HH:mm'))
    }
    Write-Host "DONE-WITH-FAILURES"
    exit 1
}

Write-Host "DONE-OK"
