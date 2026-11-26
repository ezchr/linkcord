//go:build windows && !no_voice

package golive

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// h264Packets packetizes the NAL units of one access unit as a sender does:
// small ones together in a STAP-A, big ones in FU-A fragments of at most
// mtu bytes, the marker on the last.
func h264Packets(nals [][]byte, mtu int) [][]byte {
	var pkts [][]byte
	var stap []byte
	flush := func() {
		if stap != nil {
			pkts = append(pkts, stap)
			stap = nil
		}
	}
	for _, nal := range nals {
		if len(nal) < 100 {
			if stap == nil {
				stap = []byte{24}
			}
			stap = binary.BigEndian.AppendUint16(stap, uint16(len(nal)))
			stap = append(stap, nal...)
			continue
		}
		flush()
		hdr, body := nal[0], nal[1:]
		for i := 0; i < len(body); i += mtu {
			end := min(i+mtu, len(body))
			fu := []byte{hdr&0xE0 | 28, hdr & 0x1F}
			if i == 0 {
				fu[1] |= 0x80
			}
			if end == len(body) {
				fu[1] |= 0x40
			}
			pkts = append(pkts, append(fu, body[i:end]...))
		}
	}
	flush()
	return pkts
}

func annexB(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, n...)
	}
	return out
}

func TestVideoReceiverLoopback(t *testing.T) {
	sps := []byte{0x67, 0x42, 0xe0, 0x1f, 0xda}
	pps := []byte{0x68, 0xce, 0x3c, 0x80}
	idr := make([]byte, 3000)
	idr[0] = 0x65
	for i := 1; i < len(idr); i++ {
		idr[i] = byte(i*7 + 1)
	}
	slice := []byte{0x41, 0x9a, 1, 2, 3}

	var mu sync.Mutex
	var pictures [][]byte
	var rtcp [][]byte
	got := make(chan struct{}, 4)
	r := NewVideoReceiver(VideoParams{
		NoDecoder: true,
		OnPicture: func(pic []byte) {
			mu.Lock()
			pictures = append(pictures, bytes.Clone(pic))
			mu.Unlock()
			got <- struct{}{}
		},
		SendRTCP: func(pkt []byte) error {
			mu.Lock()
			rtcp = append(rtcp, bytes.Clone(pkt))
			mu.Unlock()
			return nil
		},
		Ours: func() uint32 { return 1 },
	})
	defer r.Close()
	const video, rtx = 1000, 1001

	// Before the SSRCs are told, nothing is taken.
	r.Push(video, 1, 1, true, []byte{0x65, 1})
	r.SetSSRCs(video, rtx)

	first := h264Packets([][]byte{sps, pps, idr}, 1000)
	if len(first) != 4 {
		t.Fatalf("%d packets", len(first))
	}
	second := h264Packets([][]byte{slice}, 1000)
	// The first out of order, its third lost and resent, wrapping the
	// sequence numbers.
	var seq0 uint16 = 65534 // wraps
	r.Push(video, seq0, 3000, false, first[0])
	r.Push(video, seq0+3, 3000, true, first[3])
	r.Push(video, seq0+1, 3000, false, first[1])
	r.Push(video, seq0+4, 6000, true, second[0])
	time.Sleep(40 * time.Millisecond)
	resend := binary.BigEndian.AppendUint16(nil, seq0+2)
	r.Push(rtx, 7, 3000, false, append(resend, first[2]...))
	// Garbage from another SSRC, and a short resend, are ignored.
	r.Push(4242, seq0+2, 3000, true, []byte{0x65, 0, 0})
	r.Push(rtx, 8, 3000, true, []byte{1})

	for i := 0; i < 2; i++ {
		select {
		case <-got:
		case <-time.After(3 * time.Second):
			t.Fatalf("%d pictures", i)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if want := annexB(sps, pps, idr); !bytes.Equal(pictures[0], want) {
		t.Fatalf("first picture: %d bytes, want %d", len(pictures[0]), len(want))
	}
	if want := annexB(slice); !bytes.Equal(pictures[1], want) {
		t.Fatalf("second picture %x, want %x", pictures[1], want)
	}
	nack := false
	for _, p := range rtcp {
		if p[1] == 205 && binary.BigEndian.Uint32(p[8:]) == video && binary.BigEndian.Uint16(p[12:]) == seq0+2 {
			nack = true
		}
	}
	if !nack {
		t.Fatal("the lost packet was not asked for again")
	}
}

func TestVideoReceiverGarbage(t *testing.T) {
	var pics int
	var mu sync.Mutex
	r := NewVideoReceiver(VideoParams{
		NoDecoder: true,
		OnPicture: func([]byte) { mu.Lock(); pics++; mu.Unlock() },
		Decrypt:   func(au, out []byte) (int, error) { return copy(out, au), nil },
		SendRTCP:  func([]byte) error { return nil },
		Ours:      func() uint32 { return 1 },
	})
	r.SetSSRCs(5, 6)
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		p := make([]byte, rng.Intn(40))
		rng.Read(p)
		ssrc := uint32(5 + rng.Intn(2))
		r.Push(ssrc, uint16(rng.Intn(70000)), uint32(rng.Intn(5)), rng.Intn(3) == 0, p)
	}
	r.SetView(320, 180)
	if !r.Shown() || r.Pixels() != 320*180 {
		t.Fatal("view not kept")
	}
	r.Close()
	r.Close()
	r.Push(5, 1, 1, true, []byte{0x65})
	if r.Frame() != nil || r.SharedFrame() != nil {
		t.Fatal("a picture without a decoder")
	}
}
