package golive

import "encoding/binary"

// nackPacket packs RTP Generic NACK feedback, including sequence wrap.
func nackPacket(ours, video uint32, missing []uint16) []byte {
	pkt := make([]byte, 12)
	pkt[0], pkt[1] = 0x81, 205
	binary.BigEndian.PutUint32(pkt[4:], ours)
	binary.BigEndian.PutUint32(pkt[8:], video)
	for len(missing) > 0 {
		pid := missing[0]
		missing = missing[1:]
		var mask uint16
		for len(missing) > 0 {
			d := missing[0] - pid
			if d == 0 {
				missing = missing[1:]
				continue
			}
			if d > 16 {
				break
			}
			mask |= 1 << (d - 1)
			missing = missing[1:]
		}
		pkt = binary.BigEndian.AppendUint16(pkt, pid)
		pkt = binary.BigEndian.AppendUint16(pkt, mask)
	}
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)/4-1))
	return pkt
}

func nackSequences(fci []byte) []uint16 {
	var seqs []uint16
	for len(fci) >= 4 && len(seqs) < 256 {
		pid, mask := binary.BigEndian.Uint16(fci), binary.BigEndian.Uint16(fci[2:])
		seqs = append(seqs, pid)
		for bit := uint16(0); bit < 16 && len(seqs) < 256; bit++ {
			if mask&(1<<bit) != 0 {
				seqs = append(seqs, pid+bit+1)
			}
		}
		fci = fci[4:]
	}
	return seqs
}

// pliPacket packs an RTCP picture loss indication, which asks the sender of
// media for a keyframe.
func pliPacket(ours, media uint32) []byte {
	pkt := make([]byte, 12)
	pkt[0], pkt[1] = 0x81, 206
	binary.BigEndian.PutUint16(pkt[2:], 2)
	binary.BigEndian.PutUint32(pkt[4:], ours)
	binary.BigEndian.PutUint32(pkt[8:], media)
	return pkt
}

// rembPacket packs a REMB (206, format 15): "REMB", one SSRC, the bitrate
// the receiver can take as exponent and 18-bit mantissa.
func rembPacket(ours, media uint32, bitrate int) []byte {
	exp, mant := 0, bitrate
	for mant >= 1<<18 {
		mant >>= 1
		exp++
	}
	remb := make([]byte, 8+16)
	remb[0], remb[1] = 0x8F, 206
	binary.BigEndian.PutUint16(remb[2:], 5)
	binary.BigEndian.PutUint32(remb[4:], ours)
	copy(remb[12:], "REMB")
	remb[16] = 1
	remb[17] = byte(exp<<2) | byte(mant>>16)
	binary.BigEndian.PutUint16(remb[18:], uint16(mant))
	binary.BigEndian.PutUint32(remb[20:], media)
	return remb
}
