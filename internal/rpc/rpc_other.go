//go:build !windows

package rpc

import "errors"

// Start serves nothing outside Windows.
func Start(Handler) (string, error) {
	return "", errors.New("rpc: only on Windows")
}
