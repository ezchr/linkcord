package golive

import "time"

const (
	reorderLimit = 256
	reorderDelay = 100 * time.Millisecond
)

type receivedPacket struct {
	seq     uint16
	ts      uint32
	marker  bool
	payload []byte
	at      time.Time
}

// packetOrder repairs short gaps without letting an interrupted stream build
// an unbounded queue. Sequence comparisons use RTP's modular arithmetic.
type packetOrder struct {
	started  bool
	next     uint16
	pending  map[uint16]receivedPacket
	lastNACK time.Time
}

func (q *packetOrder) push(p receivedPacket) bool {
	if !q.started {
		q.started, q.next = true, p.seq
		q.pending = make(map[uint16]receivedPacket)
	}
	if int16(p.seq-q.next) < 0 {
		return false
	}
	if _, exists := q.pending[p.seq]; exists {
		return false
	}
	if len(q.pending) >= reorderLimit {
		return false
	}
	q.pending[p.seq] = p
	return true
}

func (q *packetOrder) pop(now time.Time) (receivedPacket, bool) {
	if p, ok := q.pending[q.next]; ok {
		delete(q.pending, q.next)
		q.next++
		return p, true
	}
	if len(q.pending) == 0 {
		return receivedPacket{}, false
	}
	var nearest receivedPacket
	distance := uint16(0xffff)
	var oldest time.Time
	for seq, p := range q.pending {
		if d := seq - q.next; d < distance {
			nearest, distance = p, d
		}
		if oldest.IsZero() || p.at.Before(oldest) {
			oldest = p.at
		}
	}
	if len(q.pending) < reorderLimit && now.Sub(oldest) < reorderDelay {
		return receivedPacket{}, false
	}
	q.next = nearest.seq
	return q.pop(now)
}

func (q *packetOrder) missing(now time.Time) []uint16 {
	if len(q.pending) == 0 || now.Sub(q.lastNACK) < 20*time.Millisecond {
		return nil
	}
	if _, ok := q.pending[q.next]; ok {
		return nil
	}
	farthest := uint16(0)
	for seq := range q.pending {
		farthest = max(farthest, seq-q.next)
	}
	var missing []uint16
	for d := uint16(0); d < farthest && len(missing) < 64; d++ {
		seq := q.next + d
		if _, ok := q.pending[seq]; !ok {
			missing = append(missing, seq)
		}
	}
	q.lastNACK = now
	return missing
}
