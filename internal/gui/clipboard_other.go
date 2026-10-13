//go:build !windows

package gui

// clipboardContents is what the clipboard holds besides text.
type clipboardContents struct {
	files []string
	png   []byte
}

// readClipboard finds nothing but text outside Windows.
func readClipboard() (clipboardContents, error) { return clipboardContents{}, nil }
