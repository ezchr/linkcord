# Drives linkcord.exe by posting window messages straight to its window, in
# the Windows 7 mouse mode, without touching the real mouse or keyboard, and
# saves screenshots of its window with PrintWindow, which works even when other
# windows cover it.
#
# Usage, from the repository:
#   .\scripts\test-window.ps1 -Demo -Script { Start-Sleep 4; Snap "start"; MouseClick 500 632; TypeText "hello"; PressKey 0x0D; Snap "sent" }
#
# Inside -Script: MouseMove x y, MouseClick x y, RightClick x y, MouseWheel x y delta (120 per
# notch, positive is up), TypeText "text", PressKey vk (0x0D Enter, 0x1B Esc),
# Snap "name" (saves <Out>\<Tag>-<name>.png). Coordinates are in the client
# area of the window.
param(
	[string]$Exe = (Join-Path $PSScriptRoot "..\..\linkcord.exe"),
	[string]$Out = (Join-Path $PSScriptRoot "..\screenshots"),
	[string]$Tag = "test",
	# Shows made-up data instead of logging in.
	[switch]$Demo,
	# Renders without a graphics card, as many Windows 7 PCs do.
	[switch]$Warp,
	[scriptblock]$Script
)
New-Item -ItemType Directory -Force $Out | Out-Null
Add-Type -AssemblyName System.Drawing
Add-Type @"
using System;
using System.Runtime.InteropServices;
public static class P {
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L, T, R, B; }
  [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr h, out RECT r);
  [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr hdc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetProcessDPIAware();
}
"@
[P]::SetProcessDPIAware() | Out-Null
# Windows 7 mouse messages, which can be posted, unlike the pointer messages
# of Windows 8 and later.
$env:GIO_LEGACY_MOUSE = "1"
# The build tested runs, rather than the copy installed for the user.
$env:LINKCORD_PORTABLE = "1"
if ($Demo) { $env:LINKCORD_DEMO = "1" } else { Remove-Item Env:LINKCORD_DEMO -ErrorAction SilentlyContinue }
if ($Warp) { $env:GIO_D3D11_WARP = "1" } else { Remove-Item Env:GIO_D3D11_WARP -ErrorAction SilentlyContinue }

$p = Start-Process $Exe -PassThru -RedirectStandardError (Join-Path $Out "$Tag-stderr.txt")
$script:h = [IntPtr]::Zero
for ($i = 0; $i -lt 50 -and $script:h -eq [IntPtr]::Zero; $i++) { Start-Sleep -Milliseconds 200; $p.Refresh(); $script:h = $p.MainWindowHandle }
if ($script:h -eq [IntPtr]::Zero) { "The window did not open (process exited: $($p.HasExited))"; Get-Content (Join-Path $Out "$Tag-stderr.txt"); exit 1 }
Start-Sleep -Milliseconds 800

function MkLParam($x, $y) { [IntPtr](($y -shl 16) -bor ($x -band 0xffff)) }
function Post($m, $w, $l) { [P]::PostMessage($script:h, $m, [IntPtr]$w, $l) | Out-Null }
function MouseMove($x, $y) { Post 0x200 0 (MkLParam $x $y); Start-Sleep -Milliseconds 120 }
function MouseClick($x, $y) {
	MouseMove $x $y
	Post 0x201 1 (MkLParam $x $y); Start-Sleep -Milliseconds 60
	Post 0x202 0 (MkLParam $x $y); Start-Sleep -Milliseconds 250
}
function RightClick($x, $y) {
	MouseMove $x $y
	Post 0x204 2 (MkLParam $x $y); Start-Sleep -Milliseconds 60
	Post 0x205 0 (MkLParam $x $y); Start-Sleep -Milliseconds 250
}
# WM_MOUSEWHEEL takes screen coordinates; these are only close to the client
# ones, which is enough to land on the right area of a window near the corner.
function MouseWheel($x, $y, $delta) { Post 0x20A ([int64]($delta -shl 16)) (MkLParam $x $y); Start-Sleep -Milliseconds 250 }
function TypeText($text) { foreach ($c in $text.ToCharArray()) { Post 0x102 ([int][char]$c) ([IntPtr]1) }; Start-Sleep -Milliseconds 200 }
function PressKey($vk) { Post 0x100 $vk ([IntPtr]1); Post 0x101 $vk ([IntPtr]0xC0000001); Start-Sleep -Milliseconds 250 }
function Snap($name) {
	$r = New-Object P+RECT; [P]::GetClientRect($script:h, [ref]$r) | Out-Null
	$bmp = New-Object System.Drawing.Bitmap $r.R, $r.B
	$g = [System.Drawing.Graphics]::FromImage($bmp)
	$hdc = $g.GetHdc()
	# PW_CLIENTONLY | PW_RENDERFULLCONTENT captures DirectX content even when covered.
	[P]::PrintWindow($script:h, $hdc, 3) | Out-Null
	$g.ReleaseHdc($hdc)
	$file = Join-Path $Out "$Tag-$name.png"
	$bmp.Save($file); $g.Dispose(); $bmp.Dispose()
	"Screenshot: $file"
}

# The window must believe it has the focus to take keys.
Post 0x7 0 ([IntPtr]0); Start-Sleep -Milliseconds 200
if ($Script) { & $Script } else { Start-Sleep -Seconds 4; Snap "start" }
Stop-Process $p -Force -ErrorAction SilentlyContinue
