//go:build !windows

package gui

import (
	"errors"
	"image"
)

var errNotWindows = errors.New("only on Windows")

func copyImage(image.Image) error { return errNotWindows }
func copyFile(string) error       { return errNotWindows }
func dragFile(string) error       { return errNotWindows }
