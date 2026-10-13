package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Plan struct {
	Dir, Target, Staged, Version, Digest, FailureRecord string
	ParentPID                                           int
	Script, Ready, Healthy, Backup, Log, Result         string
}

func psLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func createPlan(p Plan) (*Plan, error) {
	p.Script = filepath.Join(p.Dir, "apply.ps1")
	p.Ready = filepath.Join(p.Dir, "ready")
	p.Healthy = filepath.Join(p.Dir, "healthy")
	p.Backup = filepath.Join(p.Dir, "previous.exe")
	p.Log = filepath.Join(p.Dir, "update.log")
	p.Result = filepath.Join(p.Dir, "result")
	script := strings.NewReplacer(
		"{{TARGET}}", psLiteral(p.Target),
		"{{STAGED}}", psLiteral(p.Staged),
		"{{VERSION}}", psLiteral(p.Version),
		"{{DIGEST}}", psLiteral(strings.ToLower(p.Digest)),
		"{{FAILURE}}", psLiteral(p.FailureRecord),
		"{{READY}}", psLiteral(p.Ready),
		"{{HEALTHY}}", psLiteral(p.Healthy),
		"{{BACKUP}}", psLiteral(p.Backup),
		"{{LOG}}", psLiteral(p.Log),
		"{{RESULT}}", psLiteral(p.Result),
		"{{PID}}", strconv.Itoa(p.ParentPID),
	).Replace(applyScript)
	if err := os.WriteFile(p.Script, []byte(script), 0o600); err != nil {
		return nil, err
	}
	return &p, nil
}

// Handoff returns only after the helper validates its input and is waiting
// for this application to exit. Until then, closing the app is unnecessary.
func (p *Plan) Handoff(ctx context.Context) error {
	return launchPlan(ctx, p)
}

// MarkHealthy acknowledges the new executable after its first GUI frame.
// A wrongly labeled release cannot acknowledge a different version.
func MarkHealthy(actualVersion string) error {
	path := os.Getenv("LINKCORD_UPDATE_HEALTH_FILE")
	expected := os.Getenv("LINKCORD_UPDATE_EXPECTED_VERSION")
	if path == "" && expected == "" {
		return nil
	}
	if filepath.Base(path) != "healthy" || !strings.HasPrefix(filepath.Base(filepath.Dir(path)), ".linkcord-update-") {
		return errors.New("invalid update health-marker path")
	}
	if strings.TrimPrefix(actualVersion, "v") != strings.TrimPrefix(expected, "v") {
		return errors.New("installed executable does not match the release version")
	}
	if err := writeCache(path, []byte(expected)); err != nil {
		return err
	}
	os.Unsetenv("LINKCORD_UPDATE_HEALTH_FILE")
	os.Unsetenv("LINKCORD_UPDATE_EXPECTED_VERSION")
	return nil
}

// This helper uses PowerShell 2-compatible syntax for Windows 7. It contains
// file paths, a version, and a digest, never GitHub or Discord credentials.
const applyScript = `
$ErrorActionPreference = 'Stop'
$target = {{TARGET}}
$staged = {{STAGED}}
$version = {{VERSION}}
$digest = {{DIGEST}}
$failure = {{FAILURE}}
$ready = {{READY}}
$healthy = {{HEALTHY}}
$backup = {{BACKUP}}
$log = {{LOG}}
$result = {{RESULT}}
$parentId = {{PID}}
$parent = $null
$child = $null
$installed = $false
$parentStopped = $false

function ClearHealthEnvironment {
    Remove-Item Env:LINKCORD_UPDATE_HEALTH_FILE -ErrorAction SilentlyContinue
    Remove-Item Env:LINKCORD_UPDATE_EXPECTED_VERSION -ErrorAction SilentlyContinue
}
function TargetIsRunning {
    $name = [IO.Path]::GetFileNameWithoutExtension($target)
    foreach ($process in [Diagnostics.Process]::GetProcessesByName($name)) {
        try {
            if (-not $process.HasExited -and [String]::Equals($process.MainModule.FileName, $target, [StringComparison]::OrdinalIgnoreCase)) {
                return $true
            }
        } catch {}
    }
    return $false
}
function WriteLog($text) {
    [IO.File]::AppendAllText($log, ([DateTime]::UtcNow.ToString('o') + ' ' + $text + [Environment]::NewLine))
}

try {
    if ($parentId -gt 0) {
        $parent = [Diagnostics.Process]::GetProcessById($parentId)
        if (-not [String]::Equals($parent.MainModule.FileName, $target, [StringComparison]::OrdinalIgnoreCase)) {
            throw 'The update target does not match the parent process.'
        }
    } else {
        $parentStopped = $true
    }
    if (-not [IO.File]::Exists($target) -or -not [IO.File]::Exists($staged)) {
        throw 'An update file is missing.'
    }
    $sha = [Security.Cryptography.SHA256]::Create()
    $stream = [IO.File]::OpenRead($staged)
    try { $actual = [BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-', '').ToLowerInvariant() }
    finally { $stream.Dispose(); $sha.Dispose() }
    if ($actual -ne $digest) { throw 'The staged executable changed after verification.' }

    [IO.File]::WriteAllText($ready, 'ready')
    if ($parent -ne $null) {
        if (-not $parent.WaitForExit(30000)) { throw 'The previous application did not exit.' }
        $parentStopped = $true
    }
    $lastError = $null
    for ($attempt = 0; $attempt -lt 20; $attempt++) {
        try {
            [IO.File]::Replace($staged, $target, $backup)
            $installed = $true
            break
        } catch {
            $lastError = $_
            Start-Sleep -Milliseconds 250
        }
    }
    if (-not $installed) { throw $lastError }
    $env:LINKCORD_UPDATE_HEALTH_FILE = $healthy
    $env:LINKCORD_UPDATE_EXPECTED_VERSION = $version
    $child = Start-Process -FilePath $target -PassThru
    $deadline = [DateTime]::UtcNow.AddSeconds(30)
    $acknowledged = $false
    while ([DateTime]::UtcNow -lt $deadline) {
        if ([IO.File]::Exists($healthy)) {
            if ([IO.File]::ReadAllText($healthy).Trim() -eq $version) { $acknowledged = $true; break }
        }
        if ($child.HasExited) { break }
        Start-Sleep -Milliseconds 100
    }
    if (-not $acknowledged) { throw 'The updated client did not acknowledge a successful startup.' }
    Remove-Item -LiteralPath $failure -ErrorAction SilentlyContinue
    [IO.File]::WriteAllText($result, 'applied')
    WriteLog ('Installed ' + $version)
    exit 0
} catch {
    WriteLog $_.Exception.Message
    if ($installed) {
        try {
            [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($failure)) | Out-Null
            [IO.File]::WriteAllText($failure, ($version + "` + "`n" + `" + $digest))
        } catch { WriteLog 'Could not record the failed release.' }
    }
    ClearHealthEnvironment
    if ($installed) {
        try {
            if ($child -ne $null -and -not $child.HasExited) { $child.Kill(); $child.WaitForExit(5000) | Out-Null }
            $failed = [IO.Path]::Combine([IO.Path]::GetDirectoryName($backup), 'failed.exe')
            [IO.File]::Replace($backup, $target, $failed)
            [IO.File]::WriteAllText($result, 'rolled-back')
            WriteLog 'Restored the previous executable.'
        } catch {
            WriteLog ('Rollback failed: ' + $_.Exception.Message)
            [IO.File]::WriteAllText($result, 'failed')
            exit 1
        }
    } else {
        [IO.File]::WriteAllText($result, 'failed')
    }
    if ($parentStopped -and -not (TargetIsRunning)) {
        try { Start-Process -FilePath $target | Out-Null }
        catch { WriteLog ('Could not restart the previous client: ' + $_.Exception.Message) }
    }
    exit 1
}
`
