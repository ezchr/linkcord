package rpc

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"golang.org/x/sys/windows"
)

const (
	pipeAccessDuplex          = 0x00000003
	fileFlagFirstPipeInstance = 0x00080000
	pipeUnlimitedInstances    = 255
	pipeBuffer                = 64 << 10
)

// Start serves the RPC on the first of \\.\pipe\discord-ipc-0 to -9 that no
// other program, such as Discord's own app, serves, and returns its name.
// Programs try them in order, so with Discord's app open they talk to it.
func Start(h Handler) (string, error) {
	for i := range 10 {
		name := fmt.Sprintf(`\\.\pipe\discord-ipc-%d`, i)
		first, err := newPipe(name, true)
		if err != nil {
			continue
		}
		go accept(name, first, h)
		return name, nil
	}
	return "", errors.New("rpc: every discord-ipc pipe is taken")
}

// newPipe makes an instance of the pipe name; the first claims the name,
// failing if another program has it.
func newPipe(name string, first bool) (windows.Handle, error) {
	mode := uint32(pipeAccessDuplex)
	if first {
		mode |= fileFlagFirstPipeInstance
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	// PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT are all 0.
	return windows.CreateNamedPipe(p, mode, 0, pipeUnlimitedInstances, pipeBuffer, pipeBuffer, 0, nil)
}

// accept waits for programs on the pipe name, each on an instance of its
// own, and serves them, until the process ends.
func accept(name string, h windows.Handle, handler Handler) {
	for {
		err := windows.ConnectNamedPipe(h, nil)
		if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			slog.Warn("rpc pipe failed", "pipe", name, "err", err)
			windows.CloseHandle(h)
			return
		}
		go serve(os.NewFile(uintptr(h), name), handler)
		if h, err = newPipe(name, false); err != nil {
			slog.Warn("rpc pipe failed", "pipe", name, "err", err)
			return
		}
	}
}
