# Embeds AI-Radar.ico into the Windows executables so the exe itself carries the
# app icon (Explorer, desktop shortcuts and copied exes all show it).
# Usage: powershell -ExecutionPolicy Bypass -File .\embed-icon.ps1
$ErrorActionPreference = 'Stop'

Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
public static class ResUpdater {
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    public static extern IntPtr BeginUpdateResourceW(string pFileName, bool bDeleteExistingResources);
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool UpdateResourceW(IntPtr hUpdate, IntPtr lpType, IntPtr lpName, ushort wLanguage, IntPtr lpData, int cbData);
    [DllImport("kernel32.dll", SetLastError = true)]
    public static extern bool EndUpdateResourceW(IntPtr hUpdate, bool fDiscard);
}
"@

if ($MyInvocation.MyCommand.Path) {
    $root = Split-Path -Parent $MyInvocation.MyCommand.Path
} else {
    $root = (Get-Location).Path
}
$ico = Join-Path $root 'AI-Radar-Windows\AI-Radar.ico'
if (-not (Test-Path $ico)) { throw "找不到图标文件：$ico" }
$iconBytes = [System.IO.File]::ReadAllBytes($ico)
if ($iconBytes.Length -lt 22 -or [BitConverter]::ToUInt16($iconBytes, 2) -ne 1) { throw "AI-Radar.ico 格式不正确" }
$count = [BitConverter]::ToUInt16($iconBytes, 4)

function Embed-Icon([string]$ExePath) {
    if (-not (Test-Path $ExePath)) { throw "找不到 exe：$ExePath" }
    $h = [ResUpdater]::BeginUpdateResourceW($ExePath, $false)
    if ($h -eq [IntPtr]::Zero) { throw "BeginUpdateResource 失败（$ExePath）：$([Runtime.InteropServices.Marshal]::GetLastWin32Error())" }
    try {
        $grp = New-Object System.IO.MemoryStream
        $bw = New-Object System.IO.BinaryWriter($grp)
        $bw.Write([UInt16]0)          # reserved
        $bw.Write([UInt16]1)          # type: icon
        $bw.Write([UInt16]$count)     # count
        $rtIcon = [IntPtr]3           # RT_ICON
        $rtGroupIcon = [IntPtr]14     # RT_GROUP_ICON
        for ($i = 0; $i -lt $count; $i++) {
            $o = 6 + $i * 16
            $w = $iconBytes[$o]; $h = $iconBytes[$o + 1]
            $cc = $iconBytes[$o + 2]; $res = $iconBytes[$o + 3]
            $planes = [BitConverter]::ToUInt16($iconBytes, $o + 4)
            $bpp = [BitConverter]::ToUInt16($iconBytes, $o + 6)
            $size = [BitConverter]::ToUInt32($iconBytes, $o + 8)
            $off = [BitConverter]::ToUInt32($iconBytes, $o + 12)
            $ptr = [Runtime.InteropServices.Marshal]::AllocHGlobal($size)
            try {
                [Runtime.InteropServices.Marshal]::Copy($iconBytes, [int]$off, $ptr, [int]$size)
                if (-not [ResUpdater]::UpdateResourceW($h, $rtIcon, [IntPtr]($i + 1), 0, $ptr, [int]$size)) {
                    throw "UpdateResource(ICON) 失败：$([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
                }
            } finally {
                [Runtime.InteropServices.Marshal]::FreeHGlobal($ptr)
            }
            $bw.Write([byte]$w); $bw.Write([byte]$h); $bw.Write([byte]$cc); $bw.Write([byte]$res)
            $bw.Write($planes); $bw.Write($bpp); $bw.Write($size); $bw.Write([UInt16]($i + 1))
        }
        $bw.Flush()
        $grpData = $grp.ToArray()
        $bw.Dispose(); $grp.Dispose()
        $gptr = [Runtime.InteropServices.Marshal]::AllocHGlobal($grpData.Length)
        try {
            [Runtime.InteropServices.Marshal]::Copy($grpData, 0, $gptr, $grpData.Length)
            if (-not [ResUpdater]::UpdateResourceW($h, $rtGroupIcon, [IntPtr]1, 0, $gptr, $grpData.Length)) {
                throw "UpdateResource(GROUP_ICON) 失败：$([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
            }
        } finally {
            [Runtime.InteropServices.Marshal]::FreeHGlobal($gptr)
        }
        if (-not [ResUpdater]::EndUpdateResourceW($h, $false)) {
            throw "EndUpdateResource 失败：$([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
        }
        Write-Host "已嵌入图标：$ExePath"
    } catch {
        [ResUpdater]::EndUpdateResourceW($h, $true) | Out-Null
        throw
    }
}

$exes = @(
    (Join-Path $root 'AI-Radar.exe'),
    (Join-Path $root 'artifacts\AI-Radar.exe'),
    (Join-Path $root 'AI-Radar-Windows\AI-Radar.exe')
)
foreach ($exe in $exes) { Embed-Icon $exe }
