package golive

import (
	"image"
	"runtime"
	"sync"
)

// shrink averages src down into dst, which is smaller: each pixel of dst is
// the mean of the pixels of src it covers, rows split between the CPUs.
func shrink(dst, src *image.RGBA) {
	dw, dh := dst.Rect.Dx(), dst.Rect.Dy()
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	// The columns of src each column of dst covers.
	x0 := make([]int, dw+1)
	for x := range x0 {
		x0[x] = x * sw / dw
	}
	parts := min(runtime.NumCPU(), 8, dh)
	var wg sync.WaitGroup
	for p := range parts {
		wg.Add(1)
		go func(from, to int) {
			defer wg.Done()
			sums := make([]uint32, dw*4)
			for y := from; y < to; y++ {
				ya, yb := y*sh/dh, max((y+1)*sh/dh, y*sh/dh+1)
				clear(sums)
				for sy := ya; sy < yb; sy++ {
					row := src.Pix[sy*src.Stride:]
					for x := range dw {
						s := sums[x*4 : x*4+4 : x*4+4]
						for sx := x0[x]; sx < max(x0[x+1], x0[x]+1); sx++ {
							px := row[sx*4 : sx*4+4 : sx*4+4]
							s[0] += uint32(px[0])
							s[1] += uint32(px[1])
							s[2] += uint32(px[2])
							s[3] += uint32(px[3])
						}
					}
				}
				out := dst.Pix[y*dst.Stride:]
				for x := range dw {
					n := uint32((yb - ya) * (max(x0[x+1], x0[x]+1) - x0[x]))
					s := sums[x*4 : x*4+4 : x*4+4]
					o := out[x*4 : x*4+4 : x*4+4]
					o[0], o[1], o[2], o[3] = uint8((s[0]+n/2)/n), uint8((s[1]+n/2)/n), uint8((s[2]+n/2)/n), uint8((s[3]+n/2)/n)
				}
			}
		}(p*dh/parts, (p+1)*dh/parts)
	}
	wg.Wait()
}
