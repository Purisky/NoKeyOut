<#
  install-tf.ps1 - resolve/download an Armbian image and write it to a TF card.

  Windows 10/11 + Administrator. This script only writes the card; the NoKeyOut
  installer runs after the board boots (see the message at the end).

  Usage:
    Get-Disk | Format-Table Number,FriendlyName,Size,BusType,IsSystem
    .\install-tf.ps1 -Board neo3plus
    .\install-tf.ps1 -Board neo3plus -DiskNumber 5
    .\install-tf.ps1 -Image D:\images\armbian.img
    .\install-tf.ps1 -Board neo3plus -NoDeploy

  With WSL2 available, the image gets the `nokeyout` user, SSH, and a one-shot
  first-boot installer before it is written. Without WSL2, -NoDeploy writes the
  Armbian image only; install NoKeyOut after the board's first login.

  Resolve only (no admin, no disk access):
    .\install-tf.ps1 -Board neo3plus -ResolveOnly

  Extract an already-downloaded image (no admin, no disk access):
    .\install-tf.ps1 -Image D:\images\armbian.img.xz -ExtractOnly

  Prepare an image without writing a disk (WSL2; no admin):
    .\install-tf.ps1 -Image D:\images\armbian.img -PrepareOnly -KeepImage

  Extraction needs one of: xz.exe, 7-Zip, Python 3, or the script can fetch
  the standalone 7zr.exe from 7-zip.org (SHA-256 pinned below).
  NOTE: ASCII-only on purpose - Windows PowerShell 5.1 reads BOM-less .ps1
        as ANSI and mangles non-ASCII string literals.
#>
[CmdletBinding()]
param(
  [string]$Board = '',
  [string]$Image = '',
  [string]$ImageUrl = '',
  [string]$Sha256 = '',
  [int]$DiskNumber = -1,
  [string]$SevenZip = '',
  [string]$WslDistro = '',
  [switch]$ResolveOnly,
  [switch]$ExtractOnly,
  [switch]$PrepareOnly,
  [switch]$NoDeploy,
  [switch]$Force,
  [switch]$KeepImage
)

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$Transcript = Join-Path ([IO.Path]::GetTempPath()) 'nokeyout-install-tf.log'
try { Start-Transcript -Path $Transcript -Force | Out-Null } catch { }
Write-Host "Log: $Transcript" 

$ToolsBase = if ($env:NOKEYOUT_TOOLS_BASE) { $env:NOKEYOUT_TOOLS_BASE.TrimEnd('/') } else { 'https://nokeyout.com/tools' }
$IndexUrl = if ($env:NOKEYOUT_IMAGE_INDEX) { $env:NOKEYOUT_IMAGE_INDEX } else { 'https://github.armbian.com/armbian-images.json' }
$SevenZipUrl = 'https://www.7-zip.org/a/7zr.exe'
$SevenZipSha = '256feca8e274e5da655e2a284fabafd9f554365eb164862089dacd4e8276d282'
$HostLang = if ($env:NOKEYOUT_LANG -eq 'zh') { 'zh' } elseif ($env:NOKEYOUT_LANG -eq 'en') { 'en' } elseif (([System.Globalization.CultureInfo]::CurrentUICulture.Name) -like 'zh*') { 'zh' } else { 'en' }

function Say([string]$msg) { Write-Host "==> $msg" }
function Fail([string]$msg) { throw $msg }

function Test-Admin {
  $id = [Security.Principal.WindowsIdentity]::GetCurrent()
  $p = New-Object Security.Principal.WindowsPrincipal($id)
  return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Get-BoardsPath([string]$Tmp) {
  $local = Join-Path $PSScriptRoot 'boards.json'
  if (Test-Path -LiteralPath $local) { return $local }
  $dst = Join-Path $Tmp 'boards.json'
  Say "Downloading boards.json"
  Invoke-WebRequest -UseBasicParsing -Uri "$ToolsBase/boards.json" -OutFile $dst
  return $dst
}

function Resolve-Image([string]$Tmp) {
  if (-not $Board) { $Board = Read-Host 'Board id (e.g. neo3plus)' }
  if (-not $Board) { Fail 'board id is required' }
  if ($Board -notmatch '^[a-z0-9][a-z0-9._-]*$') { Fail "invalid board: $Board" }

  $boardsPath = Get-BoardsPath $Tmp
  $reg = Get-Content -LiteralPath $boardsPath -Raw | ConvertFrom-Json
  $entryProp = $reg.boards.PSObject.Properties[$Board]
  if (-not $entryProp) { Fail "unknown board: $Board (see boards.json)" }
  $entry = $entryProp.Value
  $imgProp = $reg.images.PSObject.Properties[$entry.image]
  if (-not $imgProp) { Fail "unknown image key: $($entry.image)" }
  $img = $imgProp.Value

  $slug = if ($img.armbian) { $img.armbian } else { $Board }
  $wantVariant = if ($img.variant) { $img.variant } else { 'minimal' }
  $wantBranch = if ($img.branch) { $img.branch } else { 'current' }

  Say "Resolving latest Armbian image for $Board"
  $index = Invoke-RestMethod -UseBasicParsing -Uri $IndexUrl
  $assets = @($index.assets | Where-Object { $_.board_slug -eq $slug })
  if ($assets.Count -eq 0) { Fail "Armbian has no image for board_slug=$slug" }

  $ranked = foreach ($a in $assets) {
    $v = [version]'0.0.0'
    if ($a.armbian_version) {
      $clean = [regex]::Replace([string]$a.armbian_version, '[^0-9.].*$', '')
      if ($clean) { try { $v = [version]$clean } catch { $v = [version]'0.0.0' } }
    }
    [pscustomobject]@{
      Asset = $a
      R1 = if ($a.variant -eq $wantVariant) { 0 } else { 1 }
      R2 = if ($a.branch -eq $wantBranch) { 0 } else { 1 }
      R3 = if ($a.variant -eq 'minimal') { 0 } else { 1 }
      R4 = if ($a.board_support -eq 'conf') { 0 } else { 1 }
      Ver = $v
      Date = [string]$a.file_date
    }
  }
  $best = $ranked | Sort-Object R1, R2, R3, R4, @{ Expression = { $_.Ver }; Descending = $true }, @{ Expression = { $_.Date }; Descending = $true } | Select-Object -First 1
  $asset = $best.Asset
  if (-not $asset.file_url) { Fail 'resolved empty image URL' }

  $sha = $Sha256
  if (-not $sha -and $asset.file_url_sha) {
    try {
      $resp = Invoke-WebRequest -UseBasicParsing -Uri $asset.file_url_sha
      $txt = $resp.Content
      if ($txt -is [byte[]]) { $txt = [Text.Encoding]::UTF8.GetString($txt) }
      $sha = (([string]$txt).Trim() -split '\s+')[0]
    } catch { Write-Warning "cannot fetch SHA file: $($asset.file_url_sha)" }
  }

  [pscustomobject]@{
    Url = $asset.file_url
    Sha256 = $sha
    Version = $asset.armbian_version
    Variant = $asset.variant
    Support = $asset.board_support
    BoardName = $asset.board_name
  }
}

function Find-SevenZip {
  param([string]$Explicit)
  if ($Explicit) {
    if (Test-Path -LiteralPath $Explicit) { return (Resolve-Path -LiteralPath $Explicit).Path }
    Fail "7-Zip not found: $Explicit"
  }
  foreach ($name in @('7z.exe', '7za.exe', '7zr.exe')) {
    $cmd = Get-Command $name -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
  }
  foreach ($p in @(
      (Join-Path $env:ProgramFiles '7-Zip\7z.exe'),
      (Join-Path ${env:ProgramFiles(x86)} '7-Zip\7z.exe'),
      (Join-Path $PSScriptRoot '7zr.exe')
    )) {
    if ($p -and (Test-Path -LiteralPath $p)) { return $p }
  }
  return ''
}

function Find-Python {
  foreach ($name in @('python.exe', 'python3.exe')) {
    $cmd = Get-Command $name -ErrorAction SilentlyContinue
    if ($cmd) {
      & $cmd.Source -c "import sys; raise SystemExit(0 if sys.version_info[0] >= 3 else 1)" 2>$null
      if ($LASTEXITCODE -eq 0) { return @{ Exe = $cmd.Source; Args = @() } }
    }
  }
  $py = Get-Command py.exe -ErrorAction SilentlyContinue
  if ($py) {
    & $py.Source -3 -c "import sys; raise SystemExit(0 if sys.version_info[0] >= 3 else 1)" 2>$null
    if ($LASTEXITCODE -eq 0) { return @{ Exe = $py.Source; Args = @('-3') } }
  }
  return $null
}

function Get-CachedFile([string]$Url, [string]$Sha, [string]$Name) {
  $dir = if ($env:NOKEYOUT_CACHE) { $env:NOKEYOUT_CACHE } else { Join-Path $env:LOCALAPPDATA 'nokeyout\cache' }
  New-Item -ItemType Directory -Path $dir -Force | Out-Null
  $dst = Join-Path $dir $Name
  if (Test-Path -LiteralPath $dst) {
    if (-not $Sha) {
      Say "Using cached $Name (no checksum available)"
      return $dst
    }
    $got = (Get-FileHash -LiteralPath $dst -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -eq $Sha.ToLowerInvariant()) {
      Say "Using cached $Name"
      return $dst
    }
    Say "Cached $Name checksum mismatch; downloading again"
    Remove-Item -LiteralPath $dst -Force
  }

  $part = $dst + '.part'
  $curl = Join-Path $env:SystemRoot 'System32\curl.exe'
  Say "Downloading $Name (resume if a .part exists)"
  if (Test-Path -LiteralPath $curl) {
    if (Test-Path -LiteralPath $part) {
      & $curl -fL --retry 3 --retry-delay 3 -C - -o $part $Url
      if ($LASTEXITCODE -ne 0) {
        & $curl -fL --retry 3 --retry-delay 3 -o $part $Url
        if ($LASTEXITCODE -ne 0) { Fail "download failed: $Name" }
      }
    } else {
      & $curl -fL --retry 3 --retry-delay 3 -o $part $Url
      if ($LASTEXITCODE -ne 0) { Fail "download failed: $Name" }
    }
  } else {
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $part
  }
  if ($Sha) {
    $got = (Get-FileHash -LiteralPath $part -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($got -ne $Sha.ToLowerInvariant()) {
      Remove-Item -LiteralPath $part -Force
      Fail "SHA-256 mismatch: $Name"
    }
  }
  Move-Item -LiteralPath $part -Destination $dst -Force
  return $dst
}

function Test-WslLoop([string]$Exe, [string]$Distro) {
  $args = @()
  if ($Distro) { $args += @('-d', $Distro) }
  $args += @('-u', 'root', '--', 'sh', '-c', 'command -v losetup >/dev/null 2>&1 && { test -e /dev/loop-control || test -e /dev/loop0; }')
  $old = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try {
    & $Exe @args 2>$null | Out-Null
    return ($LASTEXITCODE -eq 0)
  } finally {
    $ErrorActionPreference = $old
  }
}

function Find-WslDistro([string]$Preferred) {
  $wsl = Join-Path $env:SystemRoot 'System32\wsl.exe'
  if (-not (Test-Path -LiteralPath $wsl)) { return $null }

  if ($Preferred) {
    if (Test-WslLoop $wsl $Preferred) { return [pscustomobject]@{ Exe = $wsl; Distro = $Preferred } }
    Fail "WSL distro not usable: $Preferred"
  }

  if (Test-WslLoop $wsl '') { return [pscustomobject]@{ Exe = $wsl; Distro = '' } }

  $old = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try { $names = @(& $wsl -l -q 2>$null) } finally { $ErrorActionPreference = $old }
  foreach ($raw in $names) {
    $name = ([string]$raw) -replace "`0", ''
    $name = $name.Trim()
    if (-not $name) { continue }
    if (Test-WslLoop $wsl $name) { return [pscustomobject]@{ Exe = $wsl; Distro = $name } }
  }
  return $null
}

function ConvertTo-WslPath([string]$Path) {
  $full = [IO.Path]::GetFullPath($Path)
  if ($full -match '^([A-Za-z]):\\(.*)$') {
    $drive = $matches[1].ToLowerInvariant()
    $rest = $matches[2] -replace '\\', '/'
    return "/mnt/$drive/$rest"
  }
  Fail "only local drive paths can be passed to WSL: $Path"
}

function Read-PlainPassword([string]$Prompt) {
  $sec = Read-Host $Prompt -AsSecureString
  $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec)
  try { return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr) }
  finally { [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr) }
}

function New-OfflineBundle([string]$Tmp) {
  $bundle = Join-Path $Tmp 'bundle'
  New-Item -ItemType Directory -Path $bundle -Force | Out-Null
  $manifestUrl = if ($env:NOKEYOUT_RELEASE_URL) { $env:NOKEYOUT_RELEASE_URL } else { 'https://nokeyout.com/releases/stable/manifest.json' }
  $pubUrl = if ($env:NOKEYOUT_PUBKEY_URL) { $env:NOKEYOUT_PUBKEY_URL } else { 'https://nokeyout.com/release.pub' }

  Say 'Downloading release manifest'
  Invoke-WebRequest -UseBasicParsing -Uri $manifestUrl -OutFile (Join-Path $bundle 'manifest.json')
  Invoke-WebRequest -UseBasicParsing -Uri ($manifestUrl + '.sig') -OutFile (Join-Path $bundle 'manifest.json.sig')
  Invoke-WebRequest -UseBasicParsing -Uri $pubUrl -OutFile (Join-Path $bundle 'release.pub')

  $m = Get-Content -LiteralPath (Join-Path $bundle 'manifest.json') -Raw | ConvertFrom-Json
  $asset = $m.assets.'linux-arm64'
  if (-not $asset -or -not $asset.url -or -not $asset.sha256) { Fail 'manifest has no linux-arm64 asset' }
  Say ("Downloading NoKeyOut {0}" -f $m.version)
  $tarName = Split-Path -Leaf ([uri]$asset.url).AbsolutePath
  if (-not $tarName) { $tarName = 'nokeyout-release.tar.gz' }
  $cached = Get-CachedFile $asset.url ([string]$asset.sha256) $tarName
  $tar = Join-Path $bundle 'release.tar.gz'
  Copy-Item -LiteralPath $cached -Destination $tar -Force

  $ex = Join-Path $bundle 'extract'
  New-Item -ItemType Directory -Path $ex -Force | Out-Null
  $tarExe = Join-Path $env:SystemRoot 'System32\tar.exe'
  if (-not (Test-Path -LiteralPath $tarExe)) { Fail 'tar.exe not found (Windows 10 1803+ required)' }
  & $tarExe -xzf $tar -C $ex
  if ($LASTEXITCODE -ne 0) { Fail "cannot extract release: $LASTEXITCODE" }
  $panel = Get-ChildItem -LiteralPath $ex -Recurse -File -Filter panel |
    Where-Object { $_.DirectoryName -like '*panel-go*' } | Select-Object -First 1
  $magpie = Get-ChildItem -LiteralPath $ex -Recurse -File -Filter magpie |
    Where-Object { $_.DirectoryName -like '*gateway*bin*' } | Select-Object -First 1
  if (-not $panel) { Fail 'release has no panel-go/panel' }
  if (-not $magpie) { Fail 'release has no gateway/bin/magpie; rebuild it for offline first boot' }
  Copy-Item -LiteralPath $panel.FullName -Destination (Join-Path $bundle 'panel')
  Remove-Item -LiteralPath $ex -Recurse -Force

  $installer = Join-Path $PSScriptRoot 'install-nokeyout.sh'
  if (-not (Test-Path -LiteralPath $installer)) {
    $installer = Join-Path $bundle 'install-nokeyout.sh'
    Invoke-WebRequest -UseBasicParsing -Uri "$ToolsBase/install-nokeyout.sh" -OutFile $installer
  }
  $dst = Join-Path $bundle 'install-nokeyout.sh'
  $text = [IO.File]::ReadAllText($installer, [Text.Encoding]::UTF8)
  $text = $text -replace "`r`n", "`n"
  [IO.File]::WriteAllText($dst, $text, (New-Object Text.UTF8Encoding($false)))
  return $bundle
}

function Set-ImageFirstboot([string]$Img, [string]$Tmp, [string]$Bundle) {
  if ($NoDeploy) {
    Write-Host 'Skipping firstboot injection (-NoDeploy).'
    return $false
  }

  $wslInfo = Find-WslDistro $WslDistro
  if (-not $wslInfo) {
    Fail 'WSL2 is required for offline first boot; use -NoDeploy to write the Armbian image only'
  }

  $script = Join-Path $PSScriptRoot 'flash-tf-wsl.sh'
  if (-not (Test-Path -LiteralPath $script)) {
    $script = Join-Path $Tmp 'flash-tf-wsl.sh'
    Say 'Downloading flash-tf-wsl.sh'
    Invoke-WebRequest -UseBasicParsing -Uri "$ToolsBase/flash-tf-wsl.sh" -OutFile $script
  }

  $pw1 = Read-PlainPassword 'Password for nokeyout'
  $pw2 = Read-PlainPassword 'Confirm password'
  if ($pw1 -ne $pw2) { Fail 'passwords do not match' }
  if (-not $pw1) { Fail 'empty password' }

  $wslArgs = @()
  if ($wslInfo.Distro) { $wslArgs += @('-d', $wslInfo.Distro) }
  $wslArgs += @('-u', 'root', '--')

  $old = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try { $hash = ($pw1 | & $wslInfo.Exe @wslArgs openssl passwd -6 -stdin 2>$null).Trim() } finally { $ErrorActionPreference = $old }
  $pw1 = $null
  $pw2 = $null
  if ($hash -notmatch '^\$6\$') { Fail 'cannot hash password via WSL openssl' }

  $wslScript = ConvertTo-WslPath $script
  $wslImg = ConvertTo-WslPath $Img
  Say 'Injecting user, SSH and firstboot into the image (WSL2)'
  $wslBundle = ConvertTo-WslPath $Bundle
  $run = $wslArgs + @('bash', $wslScript, $wslImg, $hash, 'nokeyout', 'nokeyout', $HostLang, $wslBundle)
  $wslErr = Join-Path $Tmp 'wsl.err'
  $old = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  try { & $wslInfo.Exe @run 2>$wslErr } finally { $ErrorActionPreference = $old }
  if ($LASTEXITCODE -ne 0) {
    if (Test-Path -LiteralPath $wslErr) { Get-Content -LiteralPath $wslErr | Write-Host }
    Fail 'WSL firstboot injection failed; use -NoDeploy to write without it'
  }
  return $true
}


function Expand-Xz([string]$Src, [string]$Tmp, [string]$SevenZipPath) {
  $seven = Find-SevenZip $SevenZipPath
  if ($seven) {
    Say "Extracting with $seven"
    & $seven x -y "-o$Tmp" $Src | Out-Null
    if ($LASTEXITCODE -ne 0) { Fail "7-Zip failed with exit code $LASTEXITCODE" }
    $img = Get-ChildItem -LiteralPath $Tmp -Filter *.img -File | Sort-Object Length -Descending | Select-Object -First 1
    if ($img) { return $img.FullName }
    Fail '7-Zip produced no .img file'
  }

  $py = Find-Python
  if ($py) {
    Say 'Extracting with Python lzma'
    $dst = Join-Path $Tmp ([IO.Path]::GetFileNameWithoutExtension($Src))
    $pyFile = Join-Path $Tmp 'unxz.py'
    @'
import lzma, shutil, sys
with lzma.open(sys.argv[1], "rb") as src, open(sys.argv[2], "wb") as dst:
    shutil.copyfileobj(src, dst, 1024 * 1024)
'@ | Set-Content -LiteralPath $pyFile -Encoding ASCII
    $pyArgs = @()
    if ($py.Args) { $pyArgs += $py.Args }
    $pyArgs += @($pyFile, $Src, $dst)
    & $py.Exe @pyArgs
    if ($LASTEXITCODE -eq 0 -and (Test-Path -LiteralPath $dst)) { return $dst }
    Fail "Python lzma failed with exit code $LASTEXITCODE"
  }

  Say 'Downloading standalone 7zr.exe'
  $seven = Join-Path $Tmp '7zr.exe'
  Invoke-WebRequest -UseBasicParsing -Uri $SevenZipUrl -OutFile $seven
  $got = (Get-FileHash -LiteralPath $seven -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($got -ne $SevenZipSha) {
    Fail "7zr.exe SHA-256 mismatch (got $got). Install 7-Zip or Python 3, or pass -SevenZip."
  }
  & $seven x -y "-o$Tmp" $Src | Out-Null
  if ($LASTEXITCODE -ne 0) { Fail "7zr failed with exit code $LASTEXITCODE" }
  $img = Get-ChildItem -LiteralPath $Tmp -Filter *.img -File | Sort-Object Length -Descending | Select-Object -First 1
  if ($img) { return $img.FullName }
  Fail '7zr produced no .img file'
}

function Expand-ImageArchive([string]$Src, [string]$Tmp, [string]$SevenZipPath) {
  $ext = [IO.Path]::GetExtension($Src).ToLowerInvariant()
  if ($ext -eq '.img') { return (Resolve-Path -LiteralPath $Src).Path }
  if ($ext -eq '.xz') { return Expand-Xz $Src $Tmp $SevenZipPath }
  if ($ext -eq '.gz') {
    Say 'Extracting gzip'
    $dst = Join-Path $Tmp ([IO.Path]::GetFileNameWithoutExtension($Src))
    $in = [IO.File]::OpenRead($Src)
    try {
      $gz = New-Object IO.Compression.GZipStream($in, [IO.Compression.CompressionMode]::Decompress)
      try {
        $out = [IO.File]::Create($dst)
        try { $gz.CopyTo($out) } finally { $out.Close() }
      } finally { $gz.Close() }
    } finally { $in.Close() }
    return $dst
  }
  if ($ext -eq '.zip') {
    Say 'Extracting zip'
    Expand-Archive -LiteralPath $Src -DestinationPath $Tmp -Force
    $img = Get-ChildItem -LiteralPath $Tmp -Filter *.img -File -Recurse | Sort-Object Length -Descending | Select-Object -First 1
    if ($img) { return $img.FullName }
    Fail 'zip contains no .img file'
  }
  Fail "unsupported image type: $ext (use .img, .img.xz, .img.gz, or .zip)"
}

function Format-GB($Bytes) {
  if ($null -eq $Bytes) { return '-' }
  try { return ('{0:N2} GB' -f ([double]$Bytes / 1GB)) } catch { return '-' }
}

function Get-DiskDetails($Disk) {
  $vols = @()
  $parts = @(Get-Partition -DiskNumber $Disk.Number -ErrorAction SilentlyContinue)
  foreach ($p in $parts) {
    $v = $null
    if ($p.DriveLetter) { $v = Get-Volume -DriveLetter $p.DriveLetter -ErrorAction SilentlyContinue }
    if (-not $v) { $v = Get-Volume -Partition $p -ErrorAction SilentlyContinue }
    if ($v) {
      $letter = if ($p.DriveLetter) { "$($p.DriveLetter):" } else { '(no drive letter)' }
      $label = if ($v.FileSystemLabel) { $v.FileSystemLabel } else { '(no label)' }
      $fs = if ($v.FileSystemType) { $v.FileSystemType } else { '-' }
      $vols += ("{0}  {1}  {2}  free {3} / total {4}" -f $letter, $fs, $label, (Format-GB $v.SizeRemaining), (Format-GB $v.Size))
    }
  }
  if ($vols.Count -eq 0) { $vols = @('(no mounted Windows volume)') }
  [pscustomobject]@{
    Number = $Disk.Number
    Model = if ($Disk.FriendlyName) { $Disk.FriendlyName } else { '(unknown model)' }
    Bus = $Disk.BusType
    Total = Format-GB $Disk.Size
    Serial = if ($Disk.SerialNumber) { $Disk.SerialNumber } else { '-' }
    PartStyle = $Disk.PartitionStyle
    Health = $Disk.HealthStatus
    Volumes = $vols
  }
}

function Show-DiskDetails($Disk, [string]$Prefix) {
  $d = Get-DiskDetails $Disk
  Write-Host ("{0}Disk {1}  {2}  {3} total  Bus={4}  Serial={5}  {6}/{7}" -f $Prefix, $d.Number, $d.Model, $d.Total, $d.Bus, $d.Serial, $d.PartStyle, $d.Health)
  foreach ($v in $d.Volumes) { Write-Host ("{0}    {1}" -f $Prefix, $v) }
}

function Select-TargetDisk {
  if ($DiskNumber -ge 0) {
    $disk = Get-Disk -Number $DiskNumber
  } else {
    $disks = @(Get-Disk | Where-Object {
        $_.BusType -in @('USB', 'SD', 'MMC') -and -not $_.IsSystem -and -not $_.IsBoot
      })
    if ($disks.Count -eq 0) { Fail 'no removable disk found; insert the TF card reader first' }
    Write-Host 'Select the target TF card (all data will be erased):'
    for ($i = 0; $i -lt $disks.Count; $i++) {
      Show-DiskDetails $disks[$i] ("  [{0}] " -f ($i + 1))
    }
    $pick = Read-Host ("Select target disk [1-{0}]" -f $disks.Count)
    if ($pick -notmatch '^\d+$') { Fail 'invalid selection' }
    $idx = [int]$pick - 1
    if ($idx -lt 0 -or $idx -ge $disks.Count) { Fail "invalid selection: $pick" }
    $disk = $disks[$idx]
  }

  if ($disk.IsSystem) { Fail "refusing: disk $($disk.Number) is the system disk" }
  if ($disk.IsBoot) { Fail "refusing: disk $($disk.Number) is the boot disk" }
  if ($disk.BusType -notin @('USB', 'SD', 'MMC')) {
    Fail "refusing: disk $($disk.Number) BusType=$($disk.BusType) is not removable media"
  }
  Show-DiskDetails $disk 'Selected: '
  return $disk
}

function Write-RawImage([string]$Img, $Disk, [switch]$Confirmed) {
  $size = (Get-Item -LiteralPath $Img).Length
  if ($size -gt $Disk.Size) { Fail ("image is larger than the target disk ({0:N0} > {1:N0} bytes)" -f $size, $Disk.Size) }

  if (-not $Confirmed) {
    Write-Host ("Image  : {0} ({1:N0} bytes)" -f $Img, $size)
    Write-Host ("Target : Disk {0}  {1}  {2} GB  Bus={3}" -f $Disk.Number, $Disk.FriendlyName, [math]::Round($Disk.Size / 1GB, 2), $Disk.BusType)
    if (-not $Force) {
      $answer = Read-Host 'This will OVERWRITE the disk. Type YES to continue'
      if ($answer -ne 'YES') { Write-Host 'Cancelled.'; return $false }
    }
  }

  try { Set-Disk -Number $Disk.Number -IsReadOnly $false -ErrorAction SilentlyContinue } catch { }
  try {
    Set-Disk -Number $Disk.Number -IsOffline $true -ErrorAction Stop
  } catch {
    Write-Warning "could not take disk offline: $($_.Exception.Message)"
    Get-Partition -DiskNumber $Disk.Number -ErrorAction SilentlyContinue | ForEach-Object {
      if ($_.DriveLetter) {
        Remove-PartitionAccessPath -DiskNumber $Disk.Number -PartitionNumber $_.PartitionNumber -AccessPath ($_.DriveLetter + ':\') -ErrorAction SilentlyContinue
      }
    }
  }

  $src = $null
  $dst = $null
  try {
    $src = [IO.File]::OpenRead($Img)
    $dst = New-Object IO.FileStream(
      "\\.\PhysicalDrive$($Disk.Number)",
      [IO.FileMode]::Open,
      [IO.FileAccess]::Write,
      [IO.FileShare]::None
    )

    $buf = New-Object byte[] (4 * 1MB)
    $done = 0L
    $next = 0
    while (($n = $src.Read($buf, 0, $buf.Length)) -gt 0) {
      $dst.Write($buf, 0, $n)
      $done += $n
      $pct = [int](100 * $done / $size)
      if ($pct -ge $next) {
        Write-Progress -Activity 'Writing image' -Status ("{0:N0} / {1:N0} bytes" -f $done, $size) -PercentComplete $pct
        $next = $pct + 1
      }
    }
    $dst.Flush()
  } finally {
    if ($dst) { $dst.Close() }
    if ($src) { $src.Close() }
    Write-Progress -Activity 'Writing image' -Completed
    Set-Disk -Number $Disk.Number -IsOffline $false -ErrorAction SilentlyContinue
  }
  return $true
}

# ---------------------------------------------------------------------------

$tmp = Join-Path $env:TEMP ('nokeyout-install-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null

if (-not $ResolveOnly -and -not $ExtractOnly -and -not $PrepareOnly -and -not (Test-Admin)) {
  $self = if ($PSCommandPath) { $PSCommandPath } else { 'install-tf.ps1' }
  Fail ("Administrator privileges required to write a disk.`n" +
        "Open Windows Terminal / PowerShell as Administrator, or run:`n" +
        "  Start-Process powershell -Verb RunAs -ArgumentList '-NoProfile -ExecutionPolicy Bypass -File `"$self`" -Board $Board'")
}

try {
  if ($Image) {
    if (-not (Test-Path -LiteralPath $Image)) { Fail "image not found: $Image" }
    $source = (Resolve-Path -LiteralPath $Image).Path
  } else {
    $resolved = Resolve-Image $tmp
    Say ("board:   {0} ({1})" -f $Board, $resolved.BoardName)
    Say ("image:   {0}" -f $resolved.Url)
    Say ("version: {0} - {1} - support={2}" -f $resolved.Version, $resolved.Variant, $resolved.Support)
    if ($resolved.Sha256) { Say ("sha256:  {0}" -f $resolved.Sha256) }

    if ($ResolveOnly) { return }

    $fileName = Split-Path -Leaf ([uri]$resolved.Url).AbsolutePath
    $source = Get-CachedFile $resolved.Url ([string]$resolved.Sha256) $fileName
  }

  if ($ResolveOnly) { return }
  if ($ExtractOnly) {
    $img = Expand-ImageArchive $source $tmp $SevenZip
    $dest = Join-Path (Get-Location).Path (Split-Path -Leaf $img)
    if (Test-Path -LiteralPath $dest) { Fail "output already exists: $dest" }
    Move-Item -LiteralPath $img -Destination $dest
    Write-Host "Extracted: $dest"
    return
  }

  $img = Expand-ImageArchive $source $tmp $SevenZip
  if ($PrepareOnly) {
    $bundle = $null
    if (-not $NoDeploy) {
      if (-not (Find-WslDistro $WslDistro)) {
        Fail 'WSL2 is required for offline first boot; use -NoDeploy to write the Armbian image only'
      }
      $bundle = New-OfflineBundle $tmp
    }
    $null = Set-ImageFirstboot $img $tmp $bundle
    Write-Host "Prepared: $img"
    return
  }

  $disk = Select-TargetDisk
  $size = (Get-Item -LiteralPath $img).Length
  Write-Host ("Image  : {0} ({1:N0} bytes)" -f $img, $size)
  Show-DiskDetails $disk 'Target : '
  if (-not $Force) {
    $answer = Read-Host 'This will OVERWRITE the disk. Type YES to continue'
    if ($answer -ne 'YES') { Write-Host 'Cancelled.'; return }
  }

  $bundle = $null
  if (-not $NoDeploy) {
    if (-not (Find-WslDistro $WslDistro)) {
      Fail 'WSL2 is required for offline first boot; use -NoDeploy to write the Armbian image only'
    }
    $bundle = New-OfflineBundle $tmp
  }
  $deployed = Set-ImageFirstboot $img $tmp $bundle
  $ok = Write-RawImage $img $disk -Confirmed
  if (-not $ok) { return }

  Write-Host ''
  Write-Host 'Done. Remove the card reader, insert the card into the board, and power on.'
  if ($deployed) {
    Write-Host 'First boot will create the nokeyout user and install NoKeyOut'
    Write-Host '(the board needs internet access).'
  } else {
    Write-Host 'Firstboot was not injected. After the Armbian first login, run:'
    Write-Host '  curl -fsSL https://nokeyout.com/tools/install-nokeyout.sh | sudo sh'
  }
} finally {
  if ($KeepImage -or $PrepareOnly) {
    if ($source) { Write-Host "Kept image: $source" }
  } else {
    Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
  }
  try { Stop-Transcript | Out-Null } catch { }
}
