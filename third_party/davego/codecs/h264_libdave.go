package codecs

// Added by Linkcord: H264 frames laid out the way libdave, Discord's own DAVE
// implementation, lays them out, so that Discord clients decrypt them.

import (
	"errors"

	"github.com/thomas-vilte/dave-go/frame"
)

var longStartCode = []byte{0, 0, 0, 1}

// H264Frame returns frame, an H264 access unit in Annex B, with every start
// code written in 4 bytes, and the ranges of it to leave unencrypted, as
// libdave's ProcessFrameH264 does:
//
//   - the receiver's depacketizer rebuilds the frame with 4-byte start codes,
//     so they must be 4 bytes long for the ranges to match;
//   - the start code, the NAL header and the start of the slice header up to
//     the PPS ID of every slice stay readable, for the depacketizer;
//   - every other NAL unit (SPS, PPS, SEI…) stays readable as a whole.
func H264Frame(f []byte) ([]byte, []frame.Range, error) {
	out := make([]byte, 0, len(f)+16)
	var ranges []frame.Range
	start, size := nextStartCode(f, 0)
	if start < 0 {
		return nil, nil, errors.New("codecs: H264 frame without a start code")
	}
	for start >= 0 {
		nal := start + size
		next, nextSize := nextStartCode(f, nal+1)
		end := len(f)
		if next >= 0 {
			end = next
		}
		unit := f[nal:end]
		at := len(out)
		out = append(out, longStartCode...)
		out = append(out, unit...)
		clear := len(longStartCode) + len(unit)
		if len(unit) > 0 {
			if t := unit[0] & 0x1F; t == 1 || t == 5 {
				clear = len(longStartCode) + min(len(unit), 1+bytesCoveringH264PPS(unit[1:]))
			}
		}
		ranges = append(ranges, frame.Range{Offset: at, Length: clear})
		start, size = next, nextSize
	}
	return out, mergeRanges(ranges), nil
}

// nextStartCode returns where the next start code at or after from begins
// and how long it is, or -1.
func nextStartCode(f []byte, from int) (int, int) {
	for i := from; i+2 < len(f); i++ {
		if f[i] != 0 || f[i+1] != 0 {
			continue
		}
		if f[i+2] == 1 {
			// A zero before makes it a 4-byte start code.
			if i > from && f[i-1] == 0 {
				return i - 1, 4
			}
			return i, 3
		}
	}
	return -1, 0
}

// bytesCoveringH264PPS returns how many bytes of a slice header, after the
// NAL header, cover its first three values: first_mb_in_slice, slice_type
// and pic_parameter_set_id, written in exponential Golomb. It skips
// emulation prevention bytes, as libdave does.
func bytesCoveringH264PPS(p []byte) int {
	bit, zeros, values := 0, 0, 0
	for bit < len(p)*8 && values < 3 {
		byteIndex, bitIndex := bit/8, bit%8
		b := p[byteIndex]
		if bitIndex == 0 && byteIndex >= 2 && b == 3 && p[byteIndex-1] == 0 && p[byteIndex-2] == 0 {
			bit += 8
			continue
		}
		if b&(1<<(7-bitIndex)) == 0 {
			zeros++
			bit++
			if zeros >= 32 {
				break
			}
			continue
		}
		values++
		bit += 1 + zeros
		zeros = 0
	}
	return bit/8 + 1
}

// mergeRanges joins ranges that touch, which libdave writes as one.
func mergeRanges(ranges []frame.Range) []frame.Range {
	var out []frame.Range
	for _, r := range ranges {
		if n := len(out); n > 0 && out[n-1].Offset+out[n-1].Length == r.Offset {
			out[n-1].Length += r.Length
			continue
		}
		out = append(out, r)
	}
	return out
}

// HasStartCode reports whether b holds an H264 start code, which must not
// appear in what is encrypted or in the footer of a frame, since packetizers
// split frames at them.
func HasStartCode(b []byte) bool {
	return hasStartCodeSequence(b)
}
