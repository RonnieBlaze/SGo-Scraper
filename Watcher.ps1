$ScraperDir = $PSScriptRoot
$IncomingDir = Join-Path $ScraperDir "Incoming"
$FailedDir = Join-Path $IncomingDir "Failed"

# Exit codes from the scraper (see README).
$ExitSomeFailed = 3
$ExitLoginFailed = 4
$ExitBadUrl = 5
$ExitRateLimited = 429

if (!(Test-Path $IncomingDir)) {
    New-Item -ItemType Directory -Path $IncomingDir | Out-Null
}

function Move-ToFailed($Item, $Reason) {
    if (!(Test-Path $FailedDir)) {
        New-Item -ItemType Directory -Path $FailedDir | Out-Null
    }
    Move-Item $Item.FullName -Destination $FailedDir -Force
    Write-Warning "$Reason Moved $($Item.Name) to $FailedDir."
}

# Reads the URL from a .url shortcut (its URL= line) or a .txt file (its first
# line that starts with http:// or https://).
function Get-QueuedUrl($Item) {
    $Lines = Get-Content $Item.FullName
    if ($Item.Extension -eq ".url") {
        return $Lines |
               Where-Object { $_ -match '^URL=' } |
               ForEach-Object { $_.Substring(4).Trim() } |
               Select-Object -First 1
    }
    return $Lines |
           ForEach-Object { $_.Trim() } |
           Where-Object { $_ -match '^https?://' } |
           Select-Object -First 1
}

Write-Host "Watching: $IncomingDir (.url and .txt files)"
Write-Host "Press Ctrl+C to stop."
Write-Host ""

while ($true) {

    # A foreach statement (not ForEach-Object) so "break" below only stops
    # this pass; in ForEach-Object it would also end the while loop and quit
    # the watcher.
    foreach ($Item in Get-ChildItem -Path (Join-Path $IncomingDir "*") -Include "*.url", "*.txt" -File) {

        try {
            $Url = Get-QueuedUrl $Item
            if (!$Url) {
                Move-ToFailed $Item "No URL found in $($Item.Name)."
                continue
            }

            & (Join-Path $ScraperDir "SGo-Scraper-master.exe") $Url
            $Code = $LASTEXITCODE

            if ($Code -eq 0) {
                Remove-Item $Item.FullName -Force
            } elseif ($Code -eq $ExitSomeFailed) {
                # The run finished but some items failed. Park the URL so it
                # doesn't block the queue; drop it back in Incoming to retry
                # (already-downloaded items are skipped).
                Move-ToFailed $Item "Some items failed for $Url."
            } elseif ($Code -eq $ExitBadUrl) {
                Move-ToFailed $Item "The scraper doesn't handle $Url."
            } else {
                Write-Warning "Scraper failed with exit code $Code for $($Item.Name). Keeping URL in queue."
                if ($Code -eq $ExitLoginFailed) {
                    Write-Warning "Login check failed: update the cookies in .env. Pausing watcher for 5 minutes..."
                    Start-Sleep -Seconds 300
                } elseif ($Code -eq $ExitRateLimited) {
                    Write-Warning "Rate limit (429) hit. Pausing watcher for 5 minutes before retrying..."
                    Start-Sleep -Seconds 300
                } else {
                    Write-Warning "Pausing watcher for 30 seconds before retrying..."
                    Start-Sleep -Seconds 30
                }
                break
            }
        }
        catch {
            Write-Warning "Failed to process: $($Item.FullName)"
            Write-Warning $_.Exception.Message
        }
    }

    Start-Sleep -Seconds 2
}
