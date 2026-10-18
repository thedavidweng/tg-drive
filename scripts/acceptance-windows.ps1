param(
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$Version = '1.0.2',
    [ValidatePattern('^\d+\.\d+\.\d+$')]
    [string]$UpgradeFrom = '1.0.1'
)

$ErrorActionPreference = 'Stop'
if (!$IsWindows -or $env:GITHUB_ACTIONS -ne 'true') {
    throw 'Run this acceptance script on a disposable Windows Actions runner.'
}

$work = Join-Path $env:RUNNER_TEMP 'td-published-windows'
$install = Join-Path $work 'installed'
$registry = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\td-gui'
$shortcuts = @(
    (Join-Path ([Environment]::GetFolderPath('Programs')) 'td.lnk'),
    (Join-Path ([Environment]::GetFolderPath('Desktop')) 'td.lnk')
)
foreach ($path in @($work, $registry) + $shortcuts) {
    if (Test-Path $path) { throw "Acceptance requires an unused path: $path" }
}
New-Item -ItemType Directory -Path $work | Out-Null
$env:TD_CONFIG = Join-Path $work 'config.toml'
$env:TD_DB = Join-Path $work 'td.db'
$env:TD_SESSION = Join-Path $work 'session.json'
$receipt = [ordered]@{
    os = [Environment]::OSVersion.VersionString
    architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    version = $Version
    upgrade_from = $UpgradeFrom
    assets = @()
    installations = @()
    signing_scope = 'Observed Authenticode status only; install success does not establish signing acceptance.'
}

function Download-VerifiedAsset([string]$Release, [string]$Name, [string]$Manifest) {
    $dir = Join-Path $work $Release
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    & gh release download "v$Release" --repo thedavidweng/tg-drive --dir $dir --pattern $Name --pattern $Manifest
    if ($LASTEXITCODE -ne 0) { throw "Release download failed: $Release/$Name" }
    $lines = @(Get-Content (Join-Path $dir $Manifest) | Where-Object {
        $_ -match ('^[a-fA-F0-9]{64}\s+\*?' + [regex]::Escape($Name) + '$')
    })
    if ($lines.Count -ne 1) { throw "Expected one checksum for $Name" }
    $path = Join-Path $dir $Name
    $hash = (Get-FileHash $path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($hash -ne ($lines[0] -split '\s+')[0].ToLowerInvariant()) {
        throw "Checksum mismatch: $Name"
    }
    $script:receipt.assets += @{ release = $Release; name = $Name; sha256 = $hash; bytes = (Get-Item $path).Length }
    return $path
}

function Signature-Observation([string]$Path) {
    $signature = Get-AuthenticodeSignature $Path
    return @{
        file = Split-Path $Path -Leaf
        sha256 = (Get-FileHash $Path -Algorithm SHA256).Hash.ToLowerInvariant()
        status = $signature.Status.ToString()
        signer = $signature.SignerCertificate.Subject
        timestamp_signer = $signature.TimeStamperCertificate.Subject
    }
}

function Install-And-Probe([string]$Release) {
    $name = "td-gui_${Release}_windows_x86_64-installer.exe"
    $installer = Download-VerifiedAsset $Release $name "$name.sha256"
    $process = Start-Process $installer -ArgumentList "/S /D=$install" -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw "Installer failed: $($process.ExitCode)" }
    $gui = Join-Path $install 'td-gui.exe'
    $reported = (& $gui --version | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $reported -ne $Release) { throw "Installed GUI version: $reported, expected $Release" }
    if ((Get-ItemProperty $registry).DisplayVersion -ne $Release) { throw 'Installed registry version differs' }
    foreach ($path in $shortcuts) {
        if (!(Test-Path $path)) { throw "Missing installed shortcut: $path" }
    }
    $guiProcess = Start-Process $gui -PassThru
    try {
        Start-Sleep -Seconds 5
        $guiProcess.Refresh()
        if ($guiProcess.HasExited) { throw "GUI exited during startup: $($guiProcess.ExitCode)" }
    } finally {
        if (!$guiProcess.HasExited) { Stop-Process -Id $guiProcess.Id }
        $guiProcess.WaitForExit()
    }
    $script:receipt.installations += @{
        version = $reported
        startup_seconds = 5
        installer = Signature-Observation $installer
        application = Signature-Observation $gui
    }
}

try {
    $zip = Download-VerifiedAsset $Version 'td_windows_x86_64.zip' 'checksums.txt'
    $cliDir = Join-Path $work 'cli'
    Expand-Archive $zip -DestinationPath $cliDir
    $cli = Join-Path $cliDir 'td.exe'
    $versionResult = (& $cli version --json | Out-String) | ConvertFrom-Json
    if ($LASTEXITCODE -ne 0 -or !$versionResult.ok -or $versionResult.data.version -ne $Version) {
        throw "CLI version differs from $Version"
    }
    $receipt.cli_version = $versionResult.data
    foreach ($shell in @('bash', 'zsh', 'fish')) {
        $completion = (& $cli completion $shell | Out-String)
        if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($completion)) {
            throw "Completion generation failed: $shell"
        }
    }
    $receipt.cli = Signature-Observation $cli
    $receipt.completions = @('bash', 'zsh', 'fish')
    Install-And-Probe $UpgradeFrom
    Install-And-Probe $Version
    $uninstaller = Join-Path $install 'uninstall.exe'
    $process = Start-Process $uninstaller -ArgumentList '/S' -Wait -PassThru
    if ($process.ExitCode -ne 0) { throw "Uninstaller failed: $($process.ExitCode)" }
    # NSIS may run the uninstaller from a temporary copy after its parent exits.
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    do {
        $remaining = @(@($install, $registry) + $shortcuts | Where-Object { Test-Path $_ })
        if ($remaining.Count -eq 0) { break }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $deadline)
    if ($remaining.Count -ne 0) { throw "Uninstall left paths: $remaining" }
    $receipt.uninstalled = $true
} catch {
    $receipt.error = $_.Exception.Message
    throw
} finally {
    $receipt | ConvertTo-Json -Depth 8 | Set-Content (Join-Path $work 'receipt.json')
}
