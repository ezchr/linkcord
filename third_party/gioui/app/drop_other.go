// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package app

// FilesDropped, if set, is called with the paths of the files dropped on a
// window. Only Windows calls it.
var FilesDropped func(paths []string)
