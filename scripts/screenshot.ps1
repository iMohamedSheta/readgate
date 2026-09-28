# Screenshot the ReadGate desktop app while it runs — good, seeded, stable.
#
# What makes this one "good" (vs. a bare empty-profile capture):
#   1. Fresh isolated profile (READGATE_HOME) seeded with SYNTHETIC demo data
#      via `ReadGate --shot-seed` (2 clusters + 3 SQLite sources, all
#      verified read-only, example.com people) — never your live profile,
#      never a real host or customer row.
#   2. Fixed window geometry (1380x900, centered) so every release looks the
#      same and the Fleet hero card + cluster cards are fully in frame.
#   3. Foreground + generous settle so the WebView finishes rendering.
#   4. Screen BitBlt capture (PrintWindow sees WebView2 as black).
#
# Usage (local):
#   ./scripts/screenshot.ps1 -ExePath "build/bin/ReadGate.exe" `
#     -OutFile "docs/screenshot.png"
#
# The release workflow calls the same script on a Windows runner and
# attaches the PNG to the GitHub Release.

param(
  [string]$ExePath = "build/bin/ReadGate.exe",
  [string]$ProfileDir = "",
  [string]$OutFile = "screenshot.png",
  [int]$TimeoutSec = 90,
  [int]$SettleSec = 6,
  [int]$Width = 1380,
  [int]$Height = 900,
  [switch]$NoSeed
)

$ErrorActionPreference = 'Stop'

Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class WinCap {
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr hWnd, out RECT lpRect);
  [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern bool MoveWindow(IntPtr hWnd, int X, int Y, int nWidth, int nHeight, bool bRepaint);
  [DllImport("user32.dll")] public static extern IntPtr GetDC(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern int ReleaseDC(IntPtr hWnd, IntPtr hDC);
  [DllImport("user32.dll")] public static extern int GetSystemMetrics(int nIndex);
  [DllImport("gdi32.dll")] public static extern bool BitBlt(IntPtr hdcDest, int x, int y, int cx, int cy, IntPtr hdcSrc, int x1, int y1, uint rop);
  [StructLayout(LayoutKind.Sequential)]
  public struct RECT { public int Left; public int Top; public int Right; public int Bottom; }
}
"@

$exe = (Resolve-Path $ExePath).Path

if ($ProfileDir -eq '') {
  $ProfileDir = Join-Path ([System.IO.Path]::GetTempPath()) 'readgate-shot'
}
# Fresh profile every run: stale windows, sizes, or real data must never leak in.
if (Test-Path $ProfileDir) { Remove-Item -Recurse -Force $ProfileDir }
New-Item -ItemType Directory -Force $ProfileDir | Out-Null
$env:READGATE_HOME = $ProfileDir
# Software rendering: headless/CI sessions and some GPUs leave the
# WebView2 surface black with hardware acceleration on.
$env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = '--disable-gpu --disable-gpu-compositing --no-sandbox'

if (-not $NoSeed) {
  Write-Host "Seeding demo profile at $ProfileDir ..."
  & $exe --shot-seed
  if ($LASTEXITCODE -ne 0) { throw "--shot-seed failed with code $LASTEXITCODE" }
  Get-ChildItem $ProfileDir -Recurse | Format-Table Name, Length | Out-String | Write-Host
}

$p = Start-Process -FilePath $exe -PassThru
try {
  $deadline = (Get-Date).AddSeconds($TimeoutSec)
  $hWnd = [IntPtr]::Zero
  while ((Get-Date) -lt $deadline) {
    $p.Refresh()
    if ($p.HasExited) { throw "App exited early - code $($p.ExitCode)." }
    if ($p.MainWindowHandle -ne [IntPtr]::Zero) { $hWnd = $p.MainWindowHandle; break }
    Start-Sleep -Milliseconds 500
  }
  if ($hWnd -eq [IntPtr]::Zero) { throw "Main window did not appear within ${TimeoutSec}s." }

  if ([WinCap]::IsIconic($hWnd)) { [WinCap]::ShowWindow($hWnd, 9) | Out-Null } # SW_RESTORE

  # Fixed geometry: center a WxH window so the Fleet hero + cards frame nicely.
  $scrW = [WinCap]::GetSystemMetrics(0)
  $scrH = [WinCap]::GetSystemMetrics(1)
  $posX = [Math]::Max(0, [int](($scrW - $Width) / 2))
  $posY = [Math]::Max(0, [int](($scrH - $Height) / 2))
  [WinCap]::MoveWindow($hWnd, $posX, $posY, $Width, $Height, $true) | Out-Null
  [WinCap]::SetForegroundWindow($hWnd) | Out-Null
  Start-Sleep -Seconds 2
  [WinCap]::SetForegroundWindow($hWnd) | Out-Null
  Start-Sleep -Seconds $SettleSec # let the WebView + FleetCards render

  $rect = New-Object WinCap+RECT
  if (-not [WinCap]::GetWindowRect($hWnd, [ref]$rect)) { throw "GetWindowRect failed." }
  $w = $rect.Right - $rect.Left
  $h = $rect.Bottom - $rect.Top
  if ($w -le 800 -or $h -le 500) { throw ("Suspicious window bounds " + $w + "x" + $h + " — app did not size correctly.") }

  Add-Type -AssemblyName System.Drawing
  $bmp = New-Object System.Drawing.Bitmap($w, $h)
  try {
    # BitBlt from the screen: WebView2 renders on the GPU compositor, which
    # PrintWindow cannot see (black capture). The window is foreground here.
    $g = [System.Drawing.Graphics]::FromImage($bmp)
    try {
      $hdcDest = $g.GetHdc()
      try {
        $hdcSrc = [WinCap]::GetDC([IntPtr]::Zero)
        try {
          if (-not [WinCap]::BitBlt($hdcDest, 0, 0, $w, $h, $hdcSrc, $rect.Left, $rect.Top, 0x00CC0020)) {
            throw "BitBlt failed."
          }
        }
        finally { [WinCap]::ReleaseDC([IntPtr]::Zero, $hdcSrc) | Out-Null }
      }
      finally { $g.ReleaseHdc($hdcDest) }
    }
    finally { $g.Dispose() }
    $out = Join-Path (Get-Location) $OutFile
    $outDir = Split-Path $out
    if ($outDir) { New-Item -ItemType Directory -Force $outDir | Out-Null }
    $bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
    $bytes = (Get-Item $out).Length
    if ($bytes -lt 50000) { throw "Screenshot suspiciously small ($bytes bytes) — WebView likely did not render." }
    Write-Host ("Screenshot saved: " + $out + " (" + $w + "x" + $h + ", " + $bytes + " bytes)")
  }
  finally { $bmp.Dispose() }
}
finally {
  try {
    $p.Refresh()
    if (-not $p.HasExited) { $p.Kill(); $p.WaitForExit(5000) | Out-Null }
  } catch {}
  Remove-Item Env:\READGATE_HOME -ErrorAction SilentlyContinue
  Remove-Item Env:\WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS -ErrorAction SilentlyContinue
}
