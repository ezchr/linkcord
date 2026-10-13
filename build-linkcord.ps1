# Builds linkcord.exe with the Go toolchain that still supports Windows 7
# (go-legacy-win7) and MinGW linked against MSVCRT, which every Windows 7
# install has, for the cgo audio code of voice.
param(
	[string]$Out = "..\linkcord.exe",
	# For a release, pass its exact tag, for example -Version v1.4.0.
	[string]$Version = "",
	# Keep the console window to read logs and panics while developing.
	[switch]$Console
)

$ErrorActionPreference = "Stop"
$tools = "$env:LOCALAPPDATA\Programs"
$env:PATH = "$tools\go-legacy-win7\bin;$tools\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
$env:CC = "gcc"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

$ldflags = "-s -w"
if ($Version) {
	if ($Version -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$') {
		throw "Version must be a release tag such as v1.4.0."
	}
	$ldflags += " -X github.com/ezchr/linkcord/internal/version.Version=$Version"
}
if (-not $Console) { $ldflags += " -H windowsgui" }

Push-Location $PSScriptRoot
try {
	go build -trimpath -ldflags $ldflags -o $Out ./cmd/linkcord
	if ($LASTEXITCODE -ne 0) { throw "go build failed" }
	$item = Get-Item $Out
	"{0} ({1:N1} MB)" -f $item.FullName, ($item.Length / 1MB)
} finally {
	Pop-Location
}
