package gui

import (
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// narratorScript speaks each line it reads with Windows' own voice, which
// Windows 7 has too, queued.
const narratorScript = `[Console]::InputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Speech
$s = New-Object System.Speech.Synthesis.SpeechSynthesizer
while (($l = [Console]::In.ReadLine()) -ne $null) { $s.SpeakAsync($l) | Out-Null }`

var narrator struct {
	mu  sync.Mutex
	in  io.WriteCloser
	cmd *exec.Cmd
}

// speak says text aloud, starting the voice the first time.
func speak(text string) {
	text = strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	if text == "" {
		return
	}
	n := &narrator
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.in == nil {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", narratorScript)
		// No window flashes up.
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
		in, err := cmd.StdinPipe()
		if err != nil {
			slog.Warn("could not start the narrator", "err", err)
			return
		}
		if err := cmd.Start(); err != nil {
			slog.Warn("could not start the narrator", "err", err)
			return
		}
		n.in, n.cmd = in, cmd
		go func() {
			cmd.Wait()
			n.mu.Lock()
			if n.cmd == cmd {
				n.in, n.cmd = nil, nil
			}
			n.mu.Unlock()
		}()
	}
	if _, err := io.WriteString(n.in, text+"\n"); err != nil {
		slog.Warn("the narrator did not speak", "err", err)
	}
}

// stopNarrator ends the voice, if it was started.
func stopNarrator() {
	n := &narrator
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.in != nil {
		n.in.Close()
		n.in, n.cmd = nil, nil
	}
}
