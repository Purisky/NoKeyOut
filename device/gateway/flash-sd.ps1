<#
  flash-sd.ps1 - raw-write an image to a physical disk (SD card).
  Windows + Administrator only. Prefer Rufus/balenaEtcher; use this when no GUI tool.

  Usage:
    Get-Disk | Format-Table Number,FriendlyName,Size,BusType,IsSystem   # find disk number
    .\flash-sd.ps1 -DiskNumber 5 -Image D:\images\armbian.img

  Guards: refuses non-USB disks, refuses the system disk, requires typing YES.
  NOTE: kept ASCII-only on purpose - Windows PowerShell 5.1 reads BOM-less
        .ps1 as ANSI and mangles non-ASCII string literals.
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory)][int]$DiskNumber,
  [Parameter(Mandatory)][string]$Image,
  [switch]$Force
)

$ErrorActionPreference = 'Stop'

$id = [Security.Principal.WindowsIdentity]::GetCurrent()
if (-not ([Security.Principal.WindowsPrincipal]$id).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
  throw 'Administrator privileges required.'
}

if (-not (Test-Path -LiteralPath $Image)) { throw "Image not found: $Image" }
$img  = (Resolve-Path -LiteralPath $Image).Path
$disk = Get-Disk -Number $DiskNumber

if ($disk.BusType -ne 'USB') { throw "Refusing: disk $DiskNumber BusType=$($disk.BusType) is not USB." }
if ($disk.IsSystem)          { throw "Refusing: disk $DiskNumber is the system disk." }

Write-Host ("Image  : {0}" -f $img)
Write-Host ("Target : Disk {0}  {1}  {2} GB  Bus={3}" -f $DiskNumber, $disk.FriendlyName, [math]::Round($disk.Size/1GB,2), $disk.BusType)

if (-not $Force) {
  $c = Read-Host 'This will OVERWRITE the disk. Type YES to continue'
  if ($c -ne 'YES') { Write-Host 'Cancelled.'; return }
}

$size = (Get-Item -LiteralPath $img).Length
Write-Host ("Writing {0:N0} bytes ..." -f $size)

$src = $null; $dst = $null
try {
  Set-Disk -Number $DiskNumber -IsReadOnly $false -ErrorAction SilentlyContinue
  Set-Disk -Number $DiskNumber -IsOffline  $true  -ErrorAction SilentlyContinue

  $src = [System.IO.File]::OpenRead($img)
  $dst = New-Object System.IO.FileStream(
    "\\.\PhysicalDrive$DiskNumber",
    [System.IO.FileMode]::Open,
    [System.IO.FileAccess]::Write,
    [System.IO.FileShare]::None
  )

  $buf  = New-Object byte[] (4 * 1MB)
  $done = 0L
  while (($n = $src.Read($buf, 0, $buf.Length)) -gt 0) {
    $dst.Write($buf, 0, $n)
    $done += $n
    $pct = [int](100 * $done / $size)
    Write-Progress -Activity 'Writing' -Status ("{0:N0} / {1:N0} bytes" -f $done, $size) -PercentComplete $pct
  }
  $dst.Flush()
}
finally {
  if ($dst) { $dst.Close() }
  if ($src) { $src.Close() }
  Set-Disk -Number $DiskNumber -IsOffline $false -ErrorAction SilentlyContinue
}

Write-Host 'Done. Windows may prompt to format - do NOT format; just eject the reader.'
