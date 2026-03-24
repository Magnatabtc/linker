#requires -Version 5.0

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$Owner = if ($env:LINKER_OWNER) { $env:LINKER_OWNER } else { 'Magnatabtc' }
$Repo = if ($env:LINKER_REPO) { $env:LINKER_REPO } else { 'linker' }
$Version = $env:LINKER_VERSION
$InstallBin = if ($env:LINKER_BIN_DIR) {
    $env:LINKER_BIN_DIR
} else {
    Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs\linker\bin'
}
$UserAgent = 'Linker Windows Setup'

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

function Invoke-GitHubJson {
    param([Parameter(Mandatory = $true)][string]$Uri)

    try {
        return Invoke-RestMethod -Headers @{ 'User-Agent' = $UserAgent } -Uri $Uri -Method Get
    } catch {
        return $null
    }
}

function Invoke-DownloadFile {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$Path
    )

    Invoke-WebRequest -Headers @{ 'User-Agent' = $UserAgent } -UseBasicParsing -Uri $Uri -OutFile $Path
}

function Get-ReleaseInfo {
    if ($Version) {
        return Invoke-GitHubJson -Uri "https://api.github.com/repos/$Owner/$Repo/releases/tags/$Version"
    }

    return Invoke-GitHubJson -Uri "https://api.github.com/repos/$Owner/$Repo/releases/latest"
}

function Get-SourceArchiveUrl {
    if ($Version) {
        return "https://github.com/$Owner/$Repo/archive/refs/tags/$Version.zip"
    }

    return "https://github.com/$Owner/$Repo/archive/refs/heads/main.zip"
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

    $archRegex = if ($Arch -eq 'arm64') { 'arm64' } else { 'amd64|x64' }

    $asset = $assets | Where-Object {
        $_.name -match '(?i)(windows|win)' -and
        $_.name -match "(?i)$archRegex" -and
        $_.name -match '(?i)\.zip$'
    } | Select-Object -First 1

    if ($asset) {
        return $asset
    }

    return $assets | Where-Object {
        $_.browser_download_url -match '(?i)(windows|win)' -and
        $_.browser_download_url -match "(?i)$archRegex" -and
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

function Add-ToUserPath {
    param([Parameter(Mandatory = $true)][string]$Directory)

    $normalizedDirectory = $Directory.Trim().TrimEnd('\')
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $userParts = @()

    if ($userPath) {
        $userParts = $userPath -split ';' | Where-Object { $_ -and $_.Trim() }
    }

    $normalizedParts = @($userParts | ForEach-Object { $_.Trim().TrimEnd('\') })
    if ($normalizedParts -notcontains $normalizedDirectory) {
        $updatedUserPath = if ($userPath) { "$userPath;$Directory" } else { $Directory }
        [Environment]::SetEnvironmentVariable('Path', $updatedUserPath, 'User')
    }

    if ($env:Path) {
        $processParts = $env:Path -split ';' | Where-Object { $_ -and $_.Trim() }
        $normalizedProcessParts = @($processParts | ForEach-Object { $_.Trim().TrimEnd('\') })
        if ($normalizedProcessParts -notcontains $normalizedDirectory) {
            $env:Path = "$Directory;$env:Path"
        }
    } else {
        $env:Path = $Directory
    }
}

function Resolve-GoExe {
    $cmd = Get-Command go -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($cmd -and $cmd.Source) {
        return $cmd.Source
    }

    $paths = @(
        (Join-Path $env:ProgramFiles 'Go\bin\go.exe'),
        (Join-Path ([Environment]::GetFolderPath('ProgramFilesX86')) 'Go\bin\go.exe'),
        (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs\Go\bin\go.exe')
    )

    foreach ($path in $paths) {
        if ($path -and (Test-Path $path)) {
            return $path
        }
    }

    return $null
}

function Ensure-GoExe {
    $goExe = Resolve-GoExe
    if ($goExe) {
        return $goExe
    }

    Write-Info 'Go is not installed yet. I will try to install it now.'

    if (Get-Command winget -ErrorAction SilentlyContinue) {
        & winget install --exact --id GoLang.Go --silent --accept-package-agreements --accept-source-agreements | Out-Host
        $goExe = Resolve-GoExe
        if ($goExe) {
            return $goExe
        }
    }

    if (Get-Command choco -ErrorAction SilentlyContinue) {
        & choco install golang -y --no-progress | Out-Host
        $goExe = Resolve-GoExe
        if ($goExe) {
            return $goExe
        }
    }

    Fail 'Go could not be found or installed. Please install Go, then run this setup again.'
}

function Install-FromRelease {
    param(
        [Parameter(Mandatory = $true)][string]$Arch,
        [Parameter(Mandatory = $true)][string]$TempRoot
    )

    $release = Get-ReleaseInfo
    if (-not $release) {
        Write-Info 'I could not find a GitHub release right now. I will use a source build instead.'
        return $false
    }

    $asset = Select-ReleaseAsset -Release $release -Arch $Arch
    if (-not $asset) {
        Write-Info 'I found a release, but not a Windows download that matches this PC. I will use a source build instead.'
        return $false
    }

    $assetName = $asset.name
    $versionName = if ($release.tag_name) { $release.tag_name } else { 'the latest release' }
    $checksumsAsset = Select-ChecksumsAsset -Release $release
    $archivePath = Join-Path $TempRoot $assetName
    $extractPath = Join-Path $TempRoot 'release'
    $installRoot = Split-Path -Parent $InstallBin
    $linkerExe = Join-Path $InstallBin 'linker.exe'

    New-Item -ItemType Directory -Path $extractPath -Force | Out-Null
    New-Item -ItemType Directory -Path $installRoot -Force | Out-Null
    New-Item -ItemType Directory -Path $InstallBin -Force | Out-Null

    Write-Info "Downloading $versionName..."

    if ($checksumsAsset) {
        $checksumsPath = Join-Path $TempRoot 'checksums.txt'
        try {
            Invoke-DownloadFile -Uri $checksumsAsset.browser_download_url -Path $checksumsPath
            Invoke-DownloadFile -Uri $asset.browser_download_url -Path $archivePath
        } catch {
            Write-Info 'The release download failed. I will use a source build instead.'
            return $false
        }

        $expectedHash = Get-ChecksumForAsset -ChecksumsPath $checksumsPath -AssetName $assetName
        if (-not $expectedHash) {
            Write-Info 'GitHub did not list a checksum for this file. I will continue without release validation.'
        } else {
            $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
            if ($actualHash -ne $expectedHash.ToLowerInvariant()) {
                Write-Info 'The release file did not pass checksum validation. I will use a source build instead.'
                return $false
            }
        }
    } else {
        Write-Info 'GitHub did not publish a checksum file for this release. I will continue without release validation.'
        try {
            Invoke-DownloadFile -Uri $asset.browser_download_url -Path $archivePath
        } catch {
            Write-Info 'The release download failed. I will use a source build instead.'
            return $false
        }
    }

    Write-Info 'Unpacking Linker...'
    try {
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath -Force
    } catch {
        Write-Info 'I could not unpack the release file. I will use a source build instead.'
        return $false
    }

    $foundExe = Get-ChildItem -Path $extractPath -Recurse -File -Filter 'linker.exe' | Select-Object -First 1
    if (-not $foundExe) {
        Write-Info 'The release file did not include linker.exe. I will use a source build instead.'
        return $false
    }

    Copy-Item -LiteralPath $foundExe.FullName -Destination $linkerExe -Force
    Add-ToUserPath -Directory $InstallBin

    return $true
}

function Install-FromSource {
    param(
        [Parameter(Mandatory = $true)][string]$Arch,
        [Parameter(Mandatory = $true)][string]$TempRoot
    )

    $goExe = Ensure-GoExe
    $sourceUrl = Get-SourceArchiveUrl
    $sourceArchive = Join-Path $TempRoot 'source.zip'
    $sourceExtract = Join-Path $TempRoot 'source'
    $installRoot = Split-Path -Parent $InstallBin
    $linkerExe = Join-Path $InstallBin 'linker.exe'

    Write-Info 'Downloading the Linker source files...'
    try {
        Invoke-DownloadFile -Uri $sourceUrl -Path $sourceArchive
    } catch {
        Fail 'I could not download the Linker source files.'
    }

    New-Item -ItemType Directory -Path $sourceExtract -Force | Out-Null
    New-Item -ItemType Directory -Path $installRoot -Force | Out-Null
    New-Item -ItemType Directory -Path $InstallBin -Force | Out-Null

    Write-Info 'Unpacking source files...'
    try {
        Expand-Archive -LiteralPath $sourceArchive -DestinationPath $sourceExtract -Force
    } catch {
        Fail 'I could not unpack the Linker source files.'
    }

    $repoRoot = Get-ChildItem -Path $sourceExtract -Directory | Select-Object -First 1
    if (-not $repoRoot) {
        Fail 'I could not find the Linker source folder.'
    }

    Write-Info 'Building Linker...'
    Push-Location $repoRoot.FullName
    try {
        & $goExe build -o $linkerExe ./cmd/linker
    } catch {
        Fail 'I could not build Linker from source.'
    } finally {
        Pop-Location
    }

    Add-ToUserPath -Directory $InstallBin
}

function Validate-InstalledLinker {
    param([Parameter(Mandatory = $true)][string]$LinkerExe)

    Write-Info 'Checking the installed command...'
    try {
        & linker version
        return
    } catch {
        & $LinkerExe version
    }
}

function Start-FirstSetup {
    param([Parameter(Mandatory = $true)][string]$LinkerExe)

    Write-Info 'Starting the first setup...'
    try {
        & linker onboard
        return
    } catch {
        & $LinkerExe onboard
    }
}

function Install-Linker {
    Enable-Tls12

    Write-Info 'Linker setup for Windows'
    Write-Info 'I will install Linker, add it to your PATH if needed, and start the first setup.'

    $arch = Get-WindowsArch
    $tempRoot = New-TempWorkDir
    $linkerExe = Join-Path $InstallBin 'linker.exe'

    try {
        $installedFromRelease = Install-FromRelease -Arch $arch -TempRoot $tempRoot
        if (-not $installedFromRelease) {
            Write-Info 'I am switching to a source build so the setup can finish.'
            Install-FromSource -Arch $arch -TempRoot $tempRoot
        }

        Validate-InstalledLinker -LinkerExe $linkerExe
        Start-FirstSetup -LinkerExe $linkerExe

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
