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
$Headers = @{
    'User-Agent' = 'Linker Windows Setup'
}

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
        # Older shells can ignore this.
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

function Invoke-DownloadText {
    param(
        [Parameter(Mandatory = $true)][string]$Uri
    )

    return (Invoke-RestMethod -Headers $Headers -Uri $Uri -Method Get)
}

function Invoke-DownloadFile {
    param(
        [Parameter(Mandatory = $true)][string]$Uri,
        [Parameter(Mandatory = $true)][string]$Path
    )

    Invoke-WebRequest -Headers $Headers -UseBasicParsing -Uri $Uri -OutFile $Path
}

function Get-ReleaseVersion {
    if ($Version) {
        return $Version
    }

    $latestUrl = "https://api.github.com/repos/$Owner/$Repo/releases/latest"
    $release = Invoke-DownloadText -Uri $latestUrl
    if (-not $release.tag_name) {
        Fail 'I could not read the latest release from GitHub.'
    }

    return $release.tag_name
}

function Get-ChecksumForAsset {
    param(
        [Parameter(Mandatory = $true)][string]$ChecksumsPath,
        [Parameter(Mandatory = $true)][string]$AssetName
    )

    $pattern = ' ' + [regex]::Escape($AssetName) + '$'
    $match = Select-String -Path $ChecksumsPath -Pattern $pattern | Select-Object -First 1
    if (-not $match) {
        Fail "GitHub's release file does not include $AssetName."
    }

    return (($match.Line -split '\s+')[0]).Trim()
}

function Add-ToUserPath {
    param(
        [Parameter(Mandatory = $true)][string]$Directory
    )

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

function Install-Linker {
    Enable-Tls12

    Write-Info 'Linker setup for Windows'
    Write-Info 'This will download Linker, put it on your PATH if needed, and start the first setup.'

    $arch = Get-WindowsArch
    $versionToUse = Get-ReleaseVersion
    $assetName = "linker_windows_${arch}.zip"
    $checksumsUrl = "https://github.com/$Owner/$Repo/releases/download/$versionToUse/checksums.txt"
    $assetUrl = "https://github.com/$Owner/$Repo/releases/download/$versionToUse/$assetName"
    $tempRoot = New-TempWorkDir

    try {
        $checksumsPath = Join-Path $tempRoot 'checksums.txt'
        $archivePath = Join-Path $tempRoot $assetName
        $extractPath = Join-Path $tempRoot 'out'
        $installRoot = Split-Path -Parent $InstallBin
        $linkerExe = Join-Path $InstallBin 'linker.exe'

        Write-Info "Downloading Linker $versionToUse..."
        Invoke-DownloadFile -Uri $checksumsUrl -Path $checksumsPath
        if (-not (Test-Path $checksumsPath)) {
            Fail 'I could not download the release file from GitHub.'
        }

        $expectedHash = Get-ChecksumForAsset -ChecksumsPath $checksumsPath -AssetName $assetName
        Invoke-DownloadFile -Uri $assetUrl -Path $archivePath

        $actualHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archivePath).Hash.ToLowerInvariant()
        if ($actualHash -ne $expectedHash.ToLowerInvariant()) {
            Fail 'The downloaded file did not match GitHub''s checksum.'
        }

        Write-Info 'Unpacking Linker...'
        New-Item -ItemType Directory -Path $extractPath -Force | Out-Null
        New-Item -ItemType Directory -Path $installRoot -Force | Out-Null
        New-Item -ItemType Directory -Path $InstallBin -Force | Out-Null
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath -Force

        $foundExe = Get-ChildItem -Path $extractPath -Recurse -File -Filter 'linker.exe' | Select-Object -First 1
        if (-not $foundExe) {
            Fail 'The download did not include linker.exe.'
        }

        Copy-Item -LiteralPath $foundExe.FullName -Destination $linkerExe -Force
        Add-ToUserPath -Directory $InstallBin

        Write-Info 'Checking the installed command...'
        & linker version

        Write-Info 'Starting the first setup...'
        & linker onboard

        Write-Info ''
        Write-Info 'Linker is ready. You can open a new PowerShell window any time and use linker right away.'
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
