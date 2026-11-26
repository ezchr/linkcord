//go:build windows && !no_voice

package golive

import "encoding/binary"

func (w *Watch) sendNACK(source uint32, missing []uint16) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if source == 0 || source != w.videoSSRC || w.ae() == nil {
		return
	}
	pkt := nackPacket(w.ours(), source, missing)
	header := pkt[:8]
	binary.BigEndian.PutUint32(w.nonce[:4], binary.BigEndian.Uint32(w.nonce[:4])+1)
	sealed := w.ae().Seal(append([]byte(nil), header...), w.nonce[:w.ae().NonceSize()], pkt[8:], header)
	w.u().Write(append(sealed, w.nonce[:4]...))
}
