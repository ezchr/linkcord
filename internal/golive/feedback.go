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
