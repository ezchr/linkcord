//go:build !windows

package auth

import "errors"

var errNoBackup = errors.New("the token is only backed up on Windows")

func protect([]byte) ([]byte, error)   { return nil, errNoBackup }
func unprotect([]byte) ([]byte, error) { return nil, errNoBackup }
