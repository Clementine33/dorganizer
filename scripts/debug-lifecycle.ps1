param(
    [int]$PollSeconds = 1,
    [int]$TimeoutSeconds = 30,
    [string]$BackendName = "onsei-organizer-backend"
)

$ErrorActionPreference = "Stop"

function Get-BackendProcesses {
    param([string]$Name)

    $needle = "$Name.exe"
    Get-CimInstance Win32_Process |
        Where-Object { $_.Name -ieq $needle } |
        ForEach-Object {
            $parent = Get-CimInstance Win32_Process -Filter "ProcessId=$($_.ParentProcessId)" -ErrorAction SilentlyContinue
            [PSCustomObject]@{
                Timestamp       = (Get-Date).ToString("s")
                BackendPid      = $_.ProcessId
                BackendName     = $_.Name
                ParentPid       = $_.ParentProcessId
                ParentName      = if ($parent) { $parent.Name } else { "<exited>" }
                CommandLine     = $_.CommandLine
            }
        }
}

Write-Host "[lifecycle] Monitoring $BackendName.exe for $TimeoutSeconds seconds..."
Write-Host "[lifecycle] Close Flutter window now, script will report if backend remains alive."

$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
$last = @()

while ((Get-Date) -lt $deadline) {
    $rows = @(Get-BackendProcesses -Name $BackendName)
    $last = $rows

    if ($rows.Count -eq 0) {
        Write-Host "[ok] No backend process found."
        exit 0
    }

    Write-Host "[warn] Backend still running:"
    $rows | Format-Table -AutoSize Timestamp, BackendPid, ParentPid, ParentName
    Start-Sleep -Seconds $PollSeconds
}

Write-Host "[fail] Backend still alive after timeout ($TimeoutSeconds s)."
if ($last.Count -gt 0) {
    $last | Format-Table -AutoSize Timestamp, BackendPid, ParentPid, ParentName
}
exit 1
