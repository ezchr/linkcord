//go:build !no_voice

package voice

import (
	"testing"

	"github.com/ezchr/linkcord/internal/opus"
)

// The Opus benchmarks run what encoding or decoding one 20 ms stereo frame
// of made up speech costs, as send and a speaker do.

// opusBenchFrames is made up speech, as stereo 20 ms frames.
func opusBenchFrames() [][]int16 {
	voiceOf := madeUpVoice(4, 4)
	var frames [][]int16
	for at := 0; at+960 <= len(voiceOf); at += 960 {
		pcm := make([]int16, frameSamples)
		for i := range 960 {
			pcm[2*i], pcm[2*i+1] = voiceOf[at+i], voiceOf[at+i]
		}
		frames = append(frames, pcm)
	}
	return frames
}

func benchOpusEncode(b *testing.B, app opus.Application, bitrate int) {
	frames := opusBenchFrames()
	enc, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: sampleRate, Channels: channels, Application: app, Bitrate: bitrate, Complexity: 10, FEC: app == opus.VOIP, PacketLoss: 10})
	if err != nil {
		b.Fatal(err)
	}
	packet := make([]byte, 4000)
	b.ReportAllocs()
	b.ResetTimer()
	for n := range b.N {
		if _, err := enc.Encode(frames[n%len(frames)], packet); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpusEncode64k(b *testing.B)      { benchOpusEncode(b, opus.VOIP, 64000) }
func BenchmarkOpusEncode64kAudio(b *testing.B) { benchOpusEncode(b, opus.Audio, 64000) }
func BenchmarkOpusEncode128k(b *testing.B)     { benchOpusEncode(b, opus.Audio, 128000) }

func BenchmarkOpusDecode(b *testing.B) {
	frames := opusBenchFrames()
	enc, err := opus.NewEncoder(voiceEncoderConfig)
	if err != nil {
		b.Fatal(err)
	}
	var packets [][]byte
	for _, pcm := range frames {
		buf := make([]byte, 4000)
		n, err := enc.Encode(pcm, buf)
		if err != nil {
			b.Fatal(err)
		}
		packets = append(packets, buf[:n])
	}
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		b.Fatal(err)
	}
	pcm := make([]int16, 6*frameSamples)
	b.ReportAllocs()
	b.ResetTimer()
	for n := range b.N {
		if _, err := dec.Decode(packets[n%len(packets)], pcm); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOpusDecodePLC(b *testing.B) {
	dec, err := opus.NewDecoder(sampleRate, channels)
	if err != nil {
		b.Fatal(err)
	}
	pcm := make([]int16, frameSamples)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := dec.Decode(nil, pcm); err != nil {
			b.Fatal(err)
		}
	}
}
