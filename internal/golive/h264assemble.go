package golive

import "encoding/binary"

// h264Assembler rebuilds H264 access units in Annex B from RTP payloads in
// order (RFC 6184: single NAL units, STAP-A and FU-A), each start code
// written in 4 bytes as DAVE's ranges expect. It is shared by the stream
// viewer and the cameras of a voice channel.
type h264Assembler struct {
	au                      []byte
	ts                      uint32
	lastSeq                 uint16
	haveSeq, lost, fuActive bool
}

// maxAccessUnit bounds an access unit being rebuilt: past it, it is
// dropped as lost.
const maxAccessUnit = 16 << 20

var startCode4 = []byte{0, 0, 0, 1}

// reset forgets the access unit being rebuilt and the last sequence number,
// as when the video's source changes.
func (a *h264Assembler) reset() {
	a.au, a.lost, a.fuActive, a.haveSeq = a.au[:0], false, false, false
}

// push adds the next packet in order. picture is called with each whole
// access unit, valid until the next push, and lost each time one could not
// be rebuilt whole.
func (a *h264Assembler) push(p receivedPacket, picture func(au []byte), lost func()) {
	gap := a.haveSeq && p.seq != a.lastSeq+1
	a.lastSeq, a.haveSeq = p.seq, true
	if p.ts != a.ts {
		if len(a.au) > 0 || a.lost {
			lost()
		}
		a.au, a.ts, a.lost, a.fuActive = a.au[:0], p.ts, false, false
	}
	if gap {
		a.lost = true
	}
	payload := p.payload
	if len(payload) == 0 {
		a.lost = true
		payload = []byte{0}
	}
	switch typ := payload[0] & 0x1F; {
	case typ >= 1 && typ <= 23:
		if a.fuActive {
			a.lost = true
		}
		a.au = append(a.au, startCode4...)
		a.au = append(a.au, payload...)
	case typ == 24:
		if a.fuActive {
			a.lost = true
		}
		for rest := payload[1:]; len(rest) > 0; {
			if len(rest) < 2 {
				a.lost = true
				break
			}
			size := int(binary.BigEndian.Uint16(rest))
			if size == 0 || len(rest) < 2+size {
				a.lost = true
				break
			}
			a.au = append(a.au, startCode4...)
			a.au = append(a.au, rest[2:2+size]...)
			rest = rest[2+size:]
		}
	case typ == 28 && len(payload) >= 3:
		start, end := payload[1]&0x80 != 0, payload[1]&0x40 != 0
		if start {
			if a.fuActive || end {
				a.lost = true
			}
			a.au = append(a.au, startCode4...)
			a.au = append(a.au, payload[0]&0xE0|payload[1]&0x1F)
			a.fuActive = true
		}
		if !a.fuActive {
			a.lost = true
		} else {
			a.au = append(a.au, payload[2:]...)
		}
		if end {
			a.fuActive = false
		}
	default:
		a.lost = true
	}
	if len(a.au) > maxAccessUnit {
		a.au, a.lost = nil, true
	}
	if !p.marker {
		return
	}
	a.lost = a.lost || a.fuActive
	if !a.lost && len(a.au) > 0 {
		picture(a.au)
	} else if a.lost {
		lost()
	}
	a.au, a.lost, a.fuActive = a.au[:0], false, false
}
