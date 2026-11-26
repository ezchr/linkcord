//go:build !no_voice

package voice

import (
	"encoding/binary"
	"testing"

	"github.com/ezchr/linkcord/internal/opus"
)

// The pipeline benchmarks run what one 20 ms packet costs, with made up
// speech. CPU per second of audio is ns/op times 50.

// benchSend runs capture, echo, filters, the loudness check and the Opus
// encoder, as send does, with farLoud saying whether anything is played.
func benchSend(b *testing.B, farLoud bool) {
	far, near := madeUpVoice(1, 20), madeUpVoice(2, 20)
	echoed := room{delay: 3840, gain: 0.3}.echoOf(far)
	e, p := newEcho(), newProcessor()
	defer e.reset()
	defer p.close()
	enc, err := opus.NewEncoder(voiceEncoderConfig)
	if err != nil {
		b.Fatal(err)
	}
	output := make([]byte, frameBytes)
	pcm := make([]int16, frameSamples)
	packet := make([]byte, 4000)
	at := 0
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if at+960 > len(far) {
			at = 0
		}
		for i := range 960 {
			f, s := int16(0), near[at+i]/2
			if farLoud {
				f = far[at+i]
				s += int16(echoed[at+i])
			}
			binary.LittleEndian.PutUint16(output[4*i:], uint16(f))
			binary.LittleEndian.PutUint16(output[4*i+2:], uint16(f))
			pcm[2*i], pcm[2*i+1] = s, s
		}
		e.played(output)
		e.capture(pcm)
		p.process(pcm)
		loud := false
		for _, v := range pcm {
			if v > 300 || v < -300 {
				loud = true
				break
			}
		}
		_ = loud
		if _, err := enc.Encode(pcm, packet); err != nil {
			b.Fatal(err)
		}
		at += 960
	}
}

func BenchmarkSendFarPlaying(b *testing.B) { benchSend(b, true) }
func BenchmarkSendFarSilent(b *testing.B)  { benchSend(b, false) }

// BenchmarkReceive decodes a packet of each of three speakers and mixes
// them, as receive and mix do.
func BenchmarkReceive(b *testing.B) {
	const speakers = 3
	enc, err := opus.NewEncoder(voiceEncoderConfig)
	if err != nil {
		b.Fatal(err)
	}
	voiceOf := madeUpVoice(3, 4)
	var packets [][]byte
	pcm := make([]int16, frameSamples)
	for at := 0; at+960 <= len(voiceOf); at += 960 {
		for i := range 960 {
			pcm[2*i], pcm[2*i+1] = voiceOf[at+i], voiceOf[at+i]
		}
		buf := make([]byte, 4000)
		n, err := enc.Encode(pcm, buf)
		if err != nil {
			b.Fatal(err)
		}
		packets = append(packets, buf[:n])
	}
	var sp [speakers]*speaker
	for i := range sp {
		if sp[i], err = newSpeaker(0); err != nil {
			b.Fatal(err)
		}
	}
	output := make([]byte, frameBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for n := range b.N {
		for i, s := range sp {
			s.write(uint16(n), packets[(n+i*7)%len(packets)])
		}
		clear(output)
		for _, s := range sp {
			s.mix(output, 1, true)
		}
	}
}

// BenchmarkEchoFarSilent is the canceller with nothing played, while the
// user speaks.
func BenchmarkEchoFarSilent(b *testing.B) {
	near := madeUpVoice(2, 20)
	e := newEcho()
	defer e.reset()
	output := make([]byte, frameBytes)
	pcm := make([]int16, frameSamples)
	at := 0
	b.ResetTimer()
	for range b.N {
		if at+960 > len(near) {
			at = 0
		}
		for i := range 960 {
			pcm[2*i], pcm[2*i+1] = near[at+i]/2, near[at+i]/2
		}
		e.played(output)
		e.capture(pcm)
		at += 960
	}
}
