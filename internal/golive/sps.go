package golive

import "errors"

// spsSize reads the picture size from an H264 sequence parameter set, the
// NAL unit of type 7 without its header byte, cropping applied.
func spsSize(sps []byte) (w, h int, err error) {
	r := &bitReader{data: unescape(sps)}
	profile := r.bits(8)
	r.bits(16) // constraint flags, level
	r.ue()     // seq_parameter_set_id
	chroma := 1
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chroma = r.ue()
		if chroma == 3 {
			r.bits(1) // separate_colour_plane_flag
		}
		r.ue()    // bit_depth_luma
		r.ue()    // bit_depth_chroma
		r.bits(1) // qpprime_y_zero_transform_bypass
		if r.bits(1) == 1 { // seq_scaling_matrix_present
			lists := 8
			if chroma == 3 {
				lists = 12
			}
			for i := 0; i < lists; i++ {
				if r.bits(1) == 1 {
					size := 16
					if i >= 6 {
						size = 64
					}
					last, next := 8, 8
					for j := 0; j < size; j++ {
						if next != 0 {
							next = (last + r.se() + 256) % 256
						}
						if next != 0 {
							last = next
						}
					}
				}
			}
		}
	}
	r.ue() // log2_max_frame_num
	switch r.ue() { // pic_order_cnt_type
	case 0:
		r.ue()
	case 1:
		r.bits(1)
		r.se()
		r.se()
		for n := r.ue(); n > 0 && !r.bad; n-- {
			r.se()
		}
	}
	r.ue()    // max_num_ref_frames
	r.bits(1) // gaps_in_frame_num_allowed
	mbW := r.ue() + 1
	mbH := r.ue() + 1
	frameMbsOnly := r.bits(1)
	if frameMbsOnly == 0 {
		r.bits(1) // mb_adaptive_frame_field
	}
	r.bits(1) // direct_8x8_inference
	w, h = mbW*16, mbH*16*(2-frameMbsOnly)
	if r.bits(1) == 1 { // frame_cropping
		left, right, top, bottom := r.ue(), r.ue(), r.ue(), r.ue()
		cx, cy := 1, 2-frameMbsOnly
		if chroma == 1 || chroma == 2 {
			cx = 2
		}
		if chroma == 1 {
			cy *= 2
		}
		w -= (left + right) * cx
		h -= (top + bottom) * cy
	}
	if r.bad || w <= 0 || h <= 0 || w > 8192 || h > 8192 {
		return 0, 0, errors.New("golive: unreadable SPS")
	}
	return w, h, nil
}

// unescape removes the emulation prevention bytes of a NAL unit.
func unescape(b []byte) []byte {
	out := make([]byte, 0, len(b))
	zeros := 0
	for _, c := range b {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, c)
	}
	return out
}

type bitReader struct {
	data []byte
	pos  int
	bad  bool
}

func (r *bitReader) bits(n int) int {
	v := 0
	for ; n > 0; n-- {
		if r.pos >= len(r.data)*8 {
			r.bad = true
			return 0
		}
		v = v<<1 | int(r.data[r.pos/8]>>(7-r.pos%8)&1)
		r.pos++
	}
	return v
}

// ue reads an unsigned Exp-Golomb number.
func (r *bitReader) ue() int {
	zeros := 0
	for r.bits(1) == 0 && !r.bad {
		if zeros++; zeros > 31 {
			r.bad = true
			return 0
		}
	}
	return 1<<zeros - 1 + r.bits(zeros)
}

// se reads a signed Exp-Golomb number.
func (r *bitReader) se() int {
	v := r.ue()
	if v%2 == 1 {
		return (v + 1) / 2
	}
	return -v / 2
}
