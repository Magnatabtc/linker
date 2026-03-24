#requires -Version 5.0

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Owner = if ($env:LINKER_OWNER) { $env:LINKER_OWNER } else { 'Magnatabtc' }
$Repo = if ($env:LINKER_REPO) { $env:LINKER_REPO } else { 'linker' }
$Version = $env:LINKER_VERSION

$ProgramRoot = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs'
$InstallBin = if ($env:LINKER_BIN_DIR) {
    $env:LINKER_BIN_DIR
} else {
    Join-Path $ProgramRoot 'linker\bin'
}
$InstallRoot = Split-Path -Parent $InstallBin
$LinkerExe = Join-Path $InstallBin 'linker.exe'

$GoRoot = Join-Path $ProgramRoot 'Go'
$GoBin = Join-Path $GoRoot 'bin'
$GoExe = Join-Path $GoBin 'go.exe'

$UserAgent = 'Linker Windows Setup'
$Headers = @{ 'User-Agent' = $UserAgent }

function Write-Info {
    param([string]$Message)
    Write-Host $Message
}

function Fail {
    param([string]$Message)
    throw $Message
}

function Enable-Tls12 {
    try {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    } catch {
        # Older Windows builds can ignore this.
    }
}

function Normalize-PathEntry {
    param([string]$Path)

    if (-not $Path) {
        return ''
    }

    return $Path.Trim().TrimEnd('\').ToLowerInvariant()
}

function Get-WindowsArch {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) {
        $arch = $env:PROCESSOR_ARCHITECTURE
    }

    switch ($arch.ToUpperInvariant()) {
        'AMD64' { return 'amd64' }
        'ARM64' { return 'arm64' }
        default { Fail 'This installer supports 64-bit Windows only.' }
    }
}

function New-TempWorkDir {
    $root = Join-Path ([IO.Path]::GetTempPath()) ('linker-setup-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $root -Force | Out-Null
    return $root
}

function Invoke-Json {
    param([Parameter(Mandatory = $true)][string]$Uri)

    try {
        return Invoke-RestMethod -Headers $Headers -Uri $Uri -Method Get
    } catch {
        return $null
    }
}

function Invoke-DownloadFile {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$Path
    )

    Invoke-WebRequest -Headers $Headers -UseBasicParsing -Uri $Uri -OutFile $Path
}

function Invoke-DownloadFileWithRetry {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$Path,
        [int]$Attempts = 3
    )

    $lastError = $null
    for ($try = 1; $try -le $Attempts; $try++) {
        try {
            Invoke-DownloadFile -Uri $Uri -Path $Path
            return $true
        } catch {
            $lastError = $_
            if ($try -lt $Attempts) {
                Start-Sleep -Seconds ([Math]::Min(6, 2 * $try))
            }
        }
    }

    if ($lastError) {
        throw $lastError
    }
    return $false
}

function Ensure-Directory {
    param([Parameter(Mandatory = $true)][string]$Path)

    New-Item -ItemType Directory -Path $Path -Force | Out-Null
}

function Add-ToUserPath {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $normalizedDirectory = Normalize-PathEntry $Directory
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $userParts = @()

    if ($userPath) {
        $userParts = $userPath -split ';' | Where-Object { $_ -and $_.Trim() }
    }

    $normalizedUserParts = @($userParts | ForEach-Object { Normalize-PathEntry $_ })
    if ($normalizedUserParts -notcontains $normalizedDirectory) {
        $updatedUserPath = if ($userPath) { "$userPath;$Directory" } else { $Directory }
        [Environment]::SetEnvironmentVariable('Path', $updatedUserPath, 'User')
    }

    $sessionPath = $env:Path
    $sessionParts = @()
    if ($sessionPath) {
        $sessionParts = $sessionPath -split ';' | Where-Object { $_ -and $_.Trim() }
    }

    $normalizedSessionParts = @($sessionParts | ForEach-Object { Normalize-PathEntry $_ })
    if ($normalizedSessionParts -notcontains $normalizedDirectory) {
        $env:Path = if ($sessionPath) { "$Directory;$sessionPath" } else { $Directory }
    }
}

function Get-ReleaseInfo {
    if ($Version) {
        return Invoke-Json "https://api.github.com/repos/$Owner/$Repo/releases/tags/$Version"
    }

    return Invoke-Json "https://api.github.com/repos/$Owner/$Repo/releases/latest"
}

function Should-Force-SourceBuild {
    return $env:LINKER_FORCE_SOURCE -eq '1'
}

function Select-ReleaseAsset {
    param(
        [Parameter(Mandatory = $true)]$Release,
        [Parameter(Mandatory = $true)][string]$Arch
    )

    $assets = @($Release.assets)
    if (-not $assets -or $assets.Count -eq 0) {
        return $null
    }

    $archPattern = if ($Arch -eq 'arm64') { 'arm64' } else { 'amd64|x64' }

    $asset = $assets | Where-Object {
        $_.name -match '(?i)(windows|win)' -and
        $_.name -match "(?i)$archPattern" -and
        $_.name -match '(?i)\.zip$'
    } | Select-Object -First 1

    if ($asset) {
        return $asset
    }

    return $assets | Where-Object {
        $_.browser_download_url -match '(?i)(windows|win)' -and
        $_.browser_download_url -match "(?i)$archPattern" -and
        $_.browser_download_url -match '(?i)\.zip$'
    } | Select-Object -First 1
}

function Select-ChecksumsAsset {
    param([Parameter(Mandatory = $true)]$Release)

    $assets = @($Release.assets)
    if (-not $assets -or $assets.Count -eq 0) {
        return $null
    }

    return $assets | Where-Object {
        $_.name -match '(?i)checksums?\.txt$'
    } | Select-Object -First 1
}

function Get-ChecksumForAsset {
    param(
        [Parameter(Mandatory = $true)][string]$ChecksumsPath,
        [Parameter(Mandatory = $true)][string]$AssetName
    )

    $pattern = ' ' + [regex]::Escape($AssetName) + '$'
    $match = Select-String -Path $ChecksumsPath -Pattern $pattern | Select-Object -First 1
    if (-not $match) {
        return $null
    }

    return (($match.Line -split '\s+')[0]).Trim()
}

function Resolve-GoExe {
    $cmd = Get-Command go -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd -and $cmd.Source) {
        return $cmd.Source
    }

    $candidatePaths = @(
        (Join-Path $GoBin 'go.exe'),
        (Join-Path $env:ProgramFiles 'Go\bin\go.exe'),
        (Join-Path ([Environment]::GetFolderPath('ProgramFilesX86')) 'Go\bin\go.exe'),
        (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs\Go\bin\go.exe')
    )

    foreach ($path in $candidatePaths) {
        if ($path -and (Test-Path $path)) {
            return $path
        }
    }

    return $null
}

function Refresh-GoEnvironment {
    param([Parameter(Mandatory = $true)][string]$GoExePath)

    $goBinDir = Split-Path -Parent $GoExePath
    $goRootDir = Split-Path -Parent $goBinDir
    $env:GOROOT = $goRootDir
    Add-ToUserPath -Directory $goBinDir
}

function Install-GoWithWinget {
    if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
        return $false
    }

    Write-Info 'Go is missing. I am trying to install it with winget...'
    try {
        & winget install --exact --id GoLang.Go --silent --accept-package-agreements --accept-source-agreements | Out-Host
    } catch {
        return $false
    }

    return [bool](Resolve-GoExe)
}

function Install-GoWithChoco {
    if (-not (Get-Command choco -ErrorAction SilentlyContinue)) {
        return $false
    }

    Write-Info 'Go is missing. I am trying to install it with Chocolatey...'
    try {
        & choco install golang -y --no-progress | Out-Host
    } catch {
        return $false
    }

    return [bool](Resolve-GoExe)
}

function Get-GoVersionFromEndpoint {
    try {
        $response = Invoke-WebRequest -Headers $Headers -UseBasicParsing -Uri 'https://go.dev/VERSION?m=text'
    } catch {
        return $null
    }

    if (-not $response -or -not $response.Content) {
        return $null
    }

    $versionLine = $response.Content -split "\r?\n" | Where-Object {
        $_ -match '^go\d+\.\d+(\.\d+)?$'
    } | Select-Object -First 1

    return $versionLine
}

function Install-GoFromGoDev {
    Write-Info 'Go is missing. I am downloading it directly from go.dev...'

    $arch = Get-WindowsArch
    $archTag = if ($arch -eq 'arm64') { 'arm64' } else { 'amd64' }
    $downloadCandidates = @()

    # Smaller catalog first (faster/reliable on PowerShell 5), then fallback to full catalog.
    $catalog = Invoke-Json 'https://go.dev/dl/?mode=json'
    if (-not $catalog) {
        $catalog = Invoke-Json 'https://go.dev/dl/?mode=json&include=all'
    }

    if ($catalog) {
        $stableEntries = @($catalog | Where-Object { $_.stable })
        if (-not $stableEntries -or $stableEntries.Count -eq 0) {
            $stableEntries = @($catalog)
        }

        foreach ($entry in ($stableEntries | Select-Object -First 6)) {
            $goFile = @($entry.files | Where-Object {
                $_.os -eq 'windows' -and
                $_.arch -eq $archTag -and
                $_.filename -match '\.zip$'
            } | Select-Object -First 1)

            if ($goFile.Count -gt 0) {
                $filename = $goFile[0].filename
                $downloadCandidates += [PSCustomObject]@{
                    Url    = ("https://go.dev/dl/" + $filename)
                    Sha256 = $goFile[0].sha256
                    Source = 'catalog'
                }
                $downloadCandidates += [PSCustomObject]@{
                    Url    = ("https://dl.google.com/go/" + $filename)
                    Sha256 = $goFile[0].sha256
                    Source = 'catalog-mirror'
                }
            }
        }
    }

    $latestVersion = Get-GoVersionFromEndpoint
    if ($latestVersion) {
        $latestFilename = "$latestVersion.windows-$archTag.zip"
        $downloadCandidates += [PSCustomObject]@{
            Url    = ("https://go.dev/dl/" + $latestFilename)
            Sha256 = $null
            Source = 'version-endpoint'
        }
        $downloadCandidates += [PSCustomObject]@{
            Url    = ("https://dl.google.com/go/" + $latestFilename)
            Sha256 = $null
            Source = 'version-endpoint-mirror'
        }
    }

    foreach ($fallbackVersion in @('go1.26.1', 'go1.26.0', 'go1.25.4', 'go1.25.3', 'go1.24.10')) {
        $fallbackFilename = "$fallbackVersion.windows-$archTag.zip"
        $downloadCandidates += [PSCustomObject]@{
            Url    = ("https://go.dev/dl/" + $fallbackFilename)
            Sha256 = $null
            Source = 'static-fallback'
        }
        $downloadCandidates += [PSCustomObject]@{
            Url    = ("https://dl.google.com/go/" + $fallbackFilename)
            Sha256 = $null
            Source = 'static-fallback-mirror'
        }
    }

    if ($downloadCandidates.Count -eq 0) {
        return $null
    }

    $tempRoot = New-TempWorkDir
    try {
        $goZip = Join-Path $tempRoot 'go-download.zip'
        $extractRoot = Join-Path $tempRoot 'go'
        $downloadWorked = $false
        $downloadSource = $null
        $downloadSha = $null

        foreach ($candidate in $downloadCandidates) {
            try {
                if (Test-Path $goZip) {
                    Remove-Item -LiteralPath $goZip -Force -ErrorAction SilentlyContinue
                }
                Invoke-DownloadFile -Uri $candidate.Url -Path $goZip
                $downloadWorked = $true
                $downloadSource = $candidate.Source
                $downloadSha = $candidate.Sha256
                break
            } catch {
                continue
            }
        }

        if (-not $downloadWorked) {
            return $null
        }

        $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $goZip).Hash.ToLowerInvariant()
        if ($downloadSha -and $actualHash -ne $downloadSha.ToLowerInvariant()) {
            Fail 'The Go download did not pass checksum validation.'
        }

        if (Test-Path $extractRoot) {
            Remove-Item -LiteralPath $extractRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
        Ensure-Directory $extractRoot
        Expand-Archive -LiteralPath $goZip -DestinationPath $extractRoot -Force

        $goSourceRoot = Join-Path $extractRoot 'go'
        if (-not (Test-Path $goSourceRoot)) {
            $goSourceRoot = Get-ChildItem -Path $extractRoot -Directory | Select-Object -First 1 | ForEach-Object { $_.FullName }
        }

        if (-not $goSourceRoot -or -not (Test-Path $goSourceRoot)) {
            return $null
        }

        Ensure-Directory $GoRoot
        Copy-Item -Path (Join-Path $goSourceRoot '*') -Destination $GoRoot -Recurse -Force
        Refresh-GoEnvironment -GoExePath (Join-Path $GoBin 'go.exe')
        Write-Info "Go is ready ($downloadSource)."
        return Resolve-GoExe
    } finally {
        if (Test-Path $tempRoot) {
            Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

function Ensure-Go {
    $goExe = Resolve-GoExe
    if ($goExe) {
        Refresh-GoEnvironment -GoExePath $goExe
        return $goExe
    }

    if (Install-GoWithWinget) {
        $goExe = Resolve-GoExe
        if ($goExe) {
            Refresh-GoEnvironment -GoExePath $goExe
            return $goExe
        }
    }

    if (Install-GoWithChoco) {
        $goExe = Resolve-GoExe
        if ($goExe) {
            Refresh-GoEnvironment -GoExePath $goExe
            return $goExe
        }
    }

    $goExe = Install-GoFromGoDev
    if ($goExe) {
        return $goExe
    }

    Fail 'Go is required, but I could not install it automatically. Please install Go and run this setup again.'
}

function Install-FromRelease {
    param(
        [Parameter(Mandatory = $true)][string]$Arch,
        [Parameter(Mandatory = $true)][string]$TempRoot
    )

    $assetName = if ($Arch -eq 'arm64') { 'linker_windows_arm64.zip' } else { 'linker_windows_amd64.zip' }
    $checksumsName = 'checksums.txt'
    $archivePath = Join-Path $TempRoot $assetName
    $checksumsPath = Join-Path $TempRoot 'checksums.txt'
    $extractPath = Join-Path $TempRoot 'release'
    $assetUrlCandidates = @()
    $checksumsUrlCandidates = @()

    if ($Version) {
        $assetUrlCandidates += "https://github.com/$Owner/$Repo/releases/download/$Version/$assetName"
        $checksumsUrlCandidates += "https://github.com/$Owner/$Repo/releases/download/$Version/$checksumsName"
    } else {
        $assetUrlCandidates += "https://github.com/$Owner/$Repo/releases/latest/download/$assetName"
        $checksumsUrlCandidates += "https://github.com/$Owner/$Repo/releases/latest/download/$checksumsName"
    }

    Ensure-Directory $extractPath
    Ensure-Directory $InstallRoot
    Ensure-Directory $InstallBin

    Write-Info 'Downloading the latest Windows package...'
    $directAssetDownloaded = $false
    $directChecksumsDownloaded = $false

    foreach ($checksumsUrl in $checksumsUrlCandidates) {
        try {
            Invoke-DownloadFileWithRetry -Uri $checksumsUrl -Path $checksumsPath -Attempts 2 | Out-Null
            $directChecksumsDownloaded = $true
            break
        } catch {
            continue
        }
    }

    foreach ($assetUrl in $assetUrlCandidates) {
        try {
            Invoke-DownloadFileWithRetry -Uri $assetUrl -Path $archivePath -Attempts 3 | Out-Null
            $directAssetDownloaded = $true
            break
        } catch {
            continue
        }
    }

    if (-not $directAssetDownloaded) {
        # Fallback: API-based discovery for non-standard asset names.
        $release = Get-ReleaseInfo
        if (-not $release) {
            Write-Info 'I could not find a GitHub release right now. I will use the source code instead.'
            return $false
        }

        $asset = Select-ReleaseAsset -Release $release -Arch $Arch
        if (-not $asset) {
            Write-Info 'I found a release, but not a Windows download that fits this PC. I will use the source code instead.'
            return $false
        }

        $archivePath = Join-Path $TempRoot $asset.name
        $checksumsAsset = Select-ChecksumsAsset -Release $release

        try {
            if ($checksumsAsset) {
                Invoke-DownloadFileWithRetry -Uri $checksumsAsset.browser_download_url -Path $checksumsPath -Attempts 2 | Out-Null
                $directChecksumsDownloaded = $true
            }

            Invoke-DownloadFileWithRetry -Uri $asset.browser_download_url -Path $archivePath -Attempts 3 | Out-Null
            $assetName = $asset.name
        } catch {
            Write-Info 'The release download failed. I will use the source code instead.'
            return $false
        }
    }

    if ($directChecksumsDownloaded -and (Test-Path $checksumsPath)) {
        $expectedHash = Get-ChecksumForAsset -ChecksumsPath $checksumsPath -AssetName $assetName
        if ($expectedHash) {
            $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
            if ($actualHash -ne $expectedHash.ToLowerInvariant()) {
                Write-Info 'The release file did not pass checksum validation. I will use the source code instead.'
                return $false
            }
        } else {
            Write-Info 'GitHub did not list a checksum for this file, so I will continue without release validation.'
        }
    }

    Write-Info 'Unpacking Linker...'
    try {
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath -Force
    } catch {
        Write-Info 'I could not unpack the release file. I will use the source code instead.'
        return $false
    }

    $foundExe = Get-ChildItem -Path $extractPath -Recurse -File -Filter 'linker.exe' | Select-Object -First 1
    if (-not $foundExe) {
        Write-Info 'The release file did not include linker.exe. I will use the source code instead.'
        return $false
    }

    Copy-Item -LiteralPath $foundExe.FullName -Destination $LinkerExe -Force
    Add-ToUserPath -Directory $InstallBin
    return $true
}

function Install-FromSource {
    param(
        [Parameter(Mandatory = $true)][string]$TempRoot
    )

    $goExe = Ensure-Go
    if (-not (Test-Path $goExe)) {
        Fail 'Go could not be found after installation.'
    }

    $sourceArchive = Join-Path $TempRoot 'source.zip'
    $sourceExtract = Join-Path $TempRoot 'source'
    $sourceUrl = "https://codeload.github.com/$Owner/$Repo/zip/refs/heads/main"

    Ensure-Directory $sourceExtract
    Ensure-Directory $InstallRoot
    Ensure-Directory $InstallBin

    Write-Info 'Downloading the Linker source code...'
    try {
        Invoke-DownloadFileWithRetry -Uri $sourceUrl -Path $sourceArchive -Attempts 3 | Out-Null
    } catch {
        Fail 'I could not download the Linker source code.'
    }

    Write-Info 'Unpacking source code...'
    try {
        Expand-Archive -LiteralPath $sourceArchive -DestinationPath $sourceExtract -Force
    } catch {
        Fail 'I could not unpack the Linker source code.'
    }

    $repoRoot = Get-ChildItem -Path $sourceExtract -Directory | Select-Object -First 1
    if (-not $repoRoot) {
        Fail 'I could not find the Linker source folder.'
    }

    Write-Info 'Building Linker...'
    Push-Location $repoRoot.FullName
    try {
        & $goExe build -o $LinkerExe ./cmd/linker
    } catch {
        Fail 'I could not build Linker from source.'
    } finally {
        Pop-Location
    }

    Add-ToUserPath -Directory $InstallBin
}

function Invoke-Linker {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)

    $command = Get-Command linker -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($command -and $command.Source) {
        & $command.Source @Arguments
        return
    }

    & $LinkerExe @Arguments
}

function Install-Linker {
    Enable-Tls12

    Write-Info 'Linker setup for Windows'
    Write-Info 'I will install Linker, add it to your PATH if needed, and start the first setup.'

    $arch = Get-WindowsArch
    $tempRoot = New-TempWorkDir

    try {
        $installedFromRelease = $false
        if (-not (Should-Force-SourceBuild)) {
            $installedFromRelease = Install-FromRelease -Arch $arch -TempRoot $tempRoot
        } else {
            Write-Info 'Using the source code path for this check.'
        }

        if (-not $installedFromRelease) {
            Write-Info 'I am switching to the source code so the setup can finish.'
            Install-FromSource -TempRoot $tempRoot
        }

        Write-Info 'Checking the installed command...'
        Invoke-Linker -Arguments @('version')

        if ($env:LINKER_SKIP_ONBOARD -eq '1') {
            Write-Info 'Skipping the first setup because this is an automated check.'
        } else {
            Write-Info 'Starting the first setup...'
            Invoke-Linker -Arguments @('onboard')
        }

        Write-Info ''
        Write-Info 'Linker is ready. You can open a new PowerShell window and use linker right away.'
    } finally {
        if (Test-Path $tempRoot) {
            Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

try {
    Install-Linker
} catch {
    Write-Host ''
    Write-Host "Linker setup could not finish: $($_.Exception.Message)"
    exit 1
}
