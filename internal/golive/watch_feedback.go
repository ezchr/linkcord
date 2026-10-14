//go:build windows && !no_voice

package golive

import "encoding/binary"

func (w *Watch) sendNACK(source uint32, missing []uint16) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if source == 0 || source != w.videoSSRC || w.aead == nil {
		return
	}
	pkt := nackPacket(w.ourSSRC, source, missing)
	header := pkt[:8]
	binary.BigEndian.PutUint32(w.nonce[:4], binary.BigEndian.Uint32(w.nonce[:4])+1)
	sealed := w.aead.Seal(append([]byte(nil), header...), w.nonce[:w.aead.NonceSize()], pkt[8:], header)
	w.udp.Write(append(sealed, w.nonce[:4]...))
}
