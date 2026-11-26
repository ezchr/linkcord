//go:build !no_voice

package opus

/*
#cgo CPPFLAGS: -I${SRCDIR} -I${SRCDIR}/../../third_party/libopus/include -I${SRCDIR}/../../third_party/libopus/celt -I${SRCDIR}/../../third_party/libopus/silk -I${SRCDIR}/../../third_party/libopus/silk/float -I${SRCDIR}/../../third_party/libopus/src -I${SRCDIR}/../../third_party/libopus
#cgo CFLAGS: -O2 -w
#cgo windows LDFLAGS: -static
#include <opus.h>
#include <stdlib.h>

// The ctls of libopus take a variable number of arguments, which cgo cannot
// call, so these call them with one.
static int linkcord_opus_encoder_set(OpusEncoder *st, int request, opus_int32 value) {
	return opus_encoder_ctl(st, request, value);
}
static int linkcord_opus_encoder_reset(OpusEncoder *st) {
	return opus_encoder_ctl(st, OPUS_RESET_STATE);
}
static int linkcord_opus_decoder_reset(OpusDecoder *st) {
	return opus_decoder_ctl(st, OPUS_RESET_STATE);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

// Application is what an encoder is tuned for.
type Application int

const (
	// VOIP is tuned for speech, as Discord's voice is.
	VOIP Application = C.OPUS_APPLICATION_VOIP
	// Audio is tuned for any sound, such as music, as a Go Live stream's is.
	Audio Application = C.OPUS_APPLICATION_AUDIO
)

var (
	ErrBadArg         = errors.New("opus: bad argument")
	ErrBufferTooSmall = errors.New("opus: buffer too small")
	ErrInvalidPacket  = errors.New("opus: invalid packet")
	ErrInternal       = errors.New("opus: internal error")
)

// errorOf returns the error a negative libopus result stands for.
func errorOf(code C.int) error {
	switch code {
	case C.OPUS_BAD_ARG:
		return ErrBadArg
	case C.OPUS_BUFFER_TOO_SMALL:
		return ErrBufferTooSmall
	case C.OPUS_INVALID_PACKET:
		return ErrInvalidPacket
	}
	return fmt.Errorf("%w: %s", ErrInternal, C.GoString(C.opus_strerror(code)))
}

// state returns zeroed C memory of size bytes for a libopus encoder or
// decoder, freed with C.free. It must not be Go memory: *C.OpusEncoder and
// *C.OpusDecoder point to C types Go knows nothing of, and the garbage
// collector does not follow such pointers, so Go memory held only by them
// was freed and reused while libopus still used it, which crashed calls.
func state(size C.int) unsafe.Pointer {
	return C.calloc(1, C.size_t(size))
}

func freeState(p unsafe.Pointer) { C.free(p) }

// EncoderConfig is how an encoder encodes.
type EncoderConfig struct {
	// SampleRate is 8000, 12000, 16000, 24000 or 48000 Hz.
	SampleRate int
	// Channels is 1, or 2 for interleaved stereo.
	Channels int
	// Application is what the encoder is tuned for. The zero value is Audio.
	Application Application
	// Bitrate is in bits per second, 0 for libopus to choose it from the
	// sample rate and the channels.
	Bitrate int
	// CBR sends every packet as large, rather than each as large as it needs.
	CBR bool
	// Complexity is 1 to 10, how hard the encoder works for the quality; 0
	// is libopus's default, 10.
	Complexity int
	// FEC puts in each packet what the decoder needs to make up the one
	// before it if lost, in the speech codec, SILK. PacketLoss is the
	// percentage of packets expected lost, which it is sized for.
	FEC        bool
	PacketLoss int
	// DTX sends almost nothing while what is encoded is silent.
	DTX bool
}

// Encoder encodes interleaved 16-bit PCM to Opus packets, one frame at a
// time, in order. It is not safe for concurrent use.
type Encoder struct {
	st       *C.OpusEncoder
	channels int
}

// NewEncoder returns an encoder for cfg.
func NewEncoder(cfg EncoderConfig) (*Encoder, error) {
	switch cfg.SampleRate {
	case 8000, 12000, 16000, 24000, 48000:
	default:
		return nil, fmt.Errorf("%w: sample rate %d", ErrBadArg, cfg.SampleRate)
	}
	if cfg.Channels != 1 && cfg.Channels != 2 {
		return nil, fmt.Errorf("%w: %d channels", ErrBadArg, cfg.Channels)
	}
	if cfg.Bitrate < 0 || cfg.Complexity < 0 || cfg.Complexity > 10 || cfg.PacketLoss < 0 || cfg.PacketLoss > 100 {
		return nil, ErrBadArg
	}
	application := cfg.Application
	if application == 0 {
		application = Audio
	}

	st := (*C.OpusEncoder)(state(C.opus_encoder_get_size(C.int(cfg.Channels))))
	if st == nil {
		return nil, ErrInternal
	}
	if code := C.opus_encoder_init(st, C.opus_int32(cfg.SampleRate), C.int(cfg.Channels), C.int(application)); code != C.OPUS_OK {
		freeState(unsafe.Pointer(st))
		return nil, errorOf(code)
	}

	bitrate := C.opus_int32(C.OPUS_AUTO)
	if cfg.Bitrate > 0 {
		bitrate = C.opus_int32(min(cfg.Bitrate, 510000))
	}
	complexity := cfg.Complexity
	if complexity == 0 {
		complexity = 10
	}
	for _, ctl := range [...]struct {
		request C.int
		value   int
	}{
		{C.OPUS_SET_BITRATE_REQUEST, int(bitrate)},
		{C.OPUS_SET_VBR_REQUEST, boolInt(!cfg.CBR)},
		{C.OPUS_SET_COMPLEXITY_REQUEST, complexity},
		{C.OPUS_SET_INBAND_FEC_REQUEST, boolInt(cfg.FEC)},
		{C.OPUS_SET_PACKET_LOSS_PERC_REQUEST, cfg.PacketLoss},
		{C.OPUS_SET_DTX_REQUEST, boolInt(cfg.DTX)},
	} {
		if code := C.linkcord_opus_encoder_set(st, ctl.request, C.opus_int32(ctl.value)); code != C.OPUS_OK {
			freeState(unsafe.Pointer(st))
			return nil, errorOf(code)
		}
	}
	e := &Encoder{st: st, channels: cfg.Channels}
	runtime.AddCleanup(e, freeState, unsafe.Pointer(st))
	return e, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Encode encodes one frame of pcm, interleaved, into buf and returns how
// long the packet is. A frame is 2.5, 5, 10, 20, 40 or 60 ms; if buf is too
// short for the bitrate, the packet is made to fit.
func (e *Encoder) Encode(pcm []int16, buf []byte) (int, error) {
	if len(pcm) == 0 || len(pcm)%e.channels != 0 || len(buf) == 0 {
		return 0, ErrBadArg
	}
	n := C.opus_encode(e.st, (*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)/e.channels),
		(*C.uchar)(unsafe.Pointer(&buf[0])), C.opus_int32(min(len(buf), 1<<30)))
	// The state is freed once e is unreachable: not before the call ends.
	runtime.KeepAlive(e)
	if n < 0 {
		return 0, errorOf(n)
	}
	return int(n), nil
}

// Reset makes the encoder start anew, as if no frame were encoded.
func (e *Encoder) Reset() {
	C.linkcord_opus_encoder_reset(e.st)
	runtime.KeepAlive(e)
}

// Decoder decodes Opus packets to interleaved 16-bit PCM, in order. It is
// not safe for concurrent use.
type Decoder struct {
	st       *C.OpusDecoder
	channels int
}

// NewDecoder returns a decoder to sampleRate, 8000, 12000, 16000, 24000 or
// 48000 Hz, and channels, 1 or 2.
func NewDecoder(sampleRate, channels int) (*Decoder, error) {
	if channels != 1 && channels != 2 {
		return nil, fmt.Errorf("%w: %d channels", ErrBadArg, channels)
	}
	st := (*C.OpusDecoder)(state(C.opus_decoder_get_size(C.int(channels))))
	if st == nil {
		return nil, ErrInternal
	}
	if code := C.opus_decoder_init(st, C.opus_int32(sampleRate), C.int(channels)); code != C.OPUS_OK {
		freeState(unsafe.Pointer(st))
		return nil, errorOf(code)
	}
	d := &Decoder{st: st, channels: channels}
	runtime.AddCleanup(d, freeState, unsafe.Pointer(st))
	return d, nil
}

// Decode decodes packet into pcm and returns how many samples of each
// channel it decoded to. pcm should hold 120 ms, the longest a packet can
// be. A nil or empty packet makes up the lost one, as long as pcm, which must
// then be a multiple of 2.5 ms.
func (d *Decoder) Decode(packet []byte, pcm []int16) (int, error) {
	return d.decode(packet, pcm, 0)
}

// DecodeFEC makes up the packet lost before packet, as long as pcm, a
// multiple of 2.5 ms, from what packet carries of it if encoded with FEC,
// and as Decode does with a nil packet otherwise. packet is then decoded
// with Decode.
func (d *Decoder) DecodeFEC(packet []byte, pcm []int16) (int, error) {
	return d.decode(packet, pcm, 1)
}

func (d *Decoder) decode(packet []byte, pcm []int16, fec C.int) (int, error) {
	if len(pcm) < d.channels {
		return 0, ErrBadArg
	}
	var data *C.uchar
	if len(packet) > 0 {
		data = (*C.uchar)(unsafe.Pointer(&packet[0]))
	}
	n := C.opus_decode(d.st, data, C.opus_int32(len(packet)), (*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)/d.channels), fec)
	runtime.KeepAlive(d)
	if n < 0 {
		return 0, errorOf(n)
	}
	return int(n), nil
}

// Reset makes the decoder start anew, as if no packet were decoded.
func (d *Decoder) Reset() {
	C.linkcord_opus_decoder_reset(d.st)
	runtime.KeepAlive(d)
}

// Version is the version of libopus.
func Version() string {
	return C.GoString(C.opus_get_version_string())
}
