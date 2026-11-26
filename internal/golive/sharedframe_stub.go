//go:build !windows || no_voice

package golive

import "image"

// SharedFrame is a picture kept on the graphics card; there is none here.
type SharedFrame struct{}

func (*SharedFrame) Size() image.Point                                 { return image.Point{} }
func (*SharedFrame) UseSharedTexture(fn func(handle uintptr, v uint64)) { fn(0, 0) }
func (*SharedFrame) SharedTextureFailed(error)                         {}

// SharedFrame is never given here: pictures come through Frame.
func (*Watch) SharedFrame() *SharedFrame { return nil }
