$ScraperDir = $PSScriptRoot
$IncomingDir = Join-Path $ScraperDir "Incoming"

if (!(Test-Path $IncomingDir)) {
    New-Item -ItemType Directory -Path $IncomingDir | Out-Null
}

Write-Host "Watching: $IncomingDir"
Write-Host "Press Ctrl+C to stop."
Write-Host ""

while ($true) {

    Get-ChildItem -Path $IncomingDir -Filter "*.url" -File | ForEach-Object {

        $File = $_.FullName

        try {

            # Read URL from .url file
            $Url = Get-Content $File |
                   Where-Object { $_ -match '^URL=' } |
                   ForEach-Object { $_.Substring(4) } |
                   Select-Object -First 1

            if ($Url) {
                & (Join-Path $ScraperDir "SGo-Scraper-master.exe") $Url
                
                if ($LASTEXITCODE -eq 0) {
                    Remove-Item $File -Force
                } else {
                    Write-Warning "Scraper failed with exit code $LASTEXITCODE for $File. Keeping URL in queue."
                    if ($LASTEXITCODE -eq 429) {
                        Write-Warning "Rate limit (429) hit. Pausing watcher for 5 minutes before retrying..."
                        Start-Sleep -Seconds 300
                    } else {
                        Write-Warning "Pausing watcher for 30 seconds before retrying..."
                        Start-Sleep -Seconds 30
                    }
                    break
                }
            }

        }
        catch {
            Write-Warning "Failed to process: $File"
            Write-Warning $_.Exception.Message
        }
    }

    Start-Sleep -Seconds 2
}
