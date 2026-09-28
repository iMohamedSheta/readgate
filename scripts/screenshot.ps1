# Screenshot the ReadGate desktop app while it runs.
#
# Launches the exe with an isolated profile (READGATE_HOME), waits for its
# main window, captures the window to a PNG, then kills the app.
#
# Usage (local):
#   ./scripts/screenshot.ps1 -ExePath "build/bin/ReadGate.exe" `
#     -OutFile "docs/screenshot.png"
#
# The release workflow calls the same script on a Windows runner and
# attaches screenshot.png to the GitHub Release.

param(
  [string]$ExePath = "build/bin/ReadGate.exe",
  [string]$ProfileDir = "",
  [string]$OutFile = "screenshot.png",
  [int]$TimeoutSec = 90,
  [int]$SettleSec = 4
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
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr hWnd, IntPtr hdcBlt, uint nFlags);
  [DllImport("user32.dll")] public static extern IntPtr GetDC(IntPtr hWnd);
  [DllImport("user32.dll")] public static extern int ReleaseDC(IntPtr hWnd, IntPtr hDC);
  [DllImport("gdi32.dll")] public static extern bool BitBlt(IntPtr hdcDest, int x, int y, int cx, int cy, IntPtr hdcSrc, int x1, int y1, uint rop);
  [StructLayout(LayoutKind.Sequential)]
  public struct RECT { public int Left; public int Top; public int Right; public int Bottom; }
}
"@

$exe = (Resolve-Path $ExePath).Path

if ($ProfileDir -eq '') {
  $ProfileDir = Join-Path ([System.IO.Path]::GetTempPath()) 'readgate-shot'
}
New-Item -ItemType Directory -Force $ProfileDir | Out-Null
$env:READGATE_HOME = $ProfileDir
# Software rendering: headless/CI sessions and some GPUs leave the
# WebView2 surface black with hardware acceleration on.
$env:WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS = '--disable-gpu'

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
  [WinCap]::SetForegroundWindow($hWnd) | Out-Null
  Start-Sleep -Seconds $SettleSec # let the WebView render

  $rect = New-Object WinCap+RECT
  if (-not [WinCap]::GetWindowRect($hWnd, [ref]$rect)) { throw "GetWindowRect failed." }
  $w = $rect.Right - $rect.Left
  $h = $rect.Bottom - $rect.Top
  if ($w -le 0 -or $h -le 0) { throw ("Invalid window bounds " + $w + "x" + $h + ".") }

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
    New-Item -ItemType Directory -Force (Split-Path $out) | Out-Null
    $bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Host ("Screenshot saved: " + $out + " (" + $w + "x" + $h + ")")
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
