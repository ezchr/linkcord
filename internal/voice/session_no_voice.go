//go:build no_voice

package voice

import (
	"errors"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/state"
)

// Session joins no voice channel.
type Session struct{}

func New(*state.State, float64) *Session { return &Session{} }

func (*Session) Updates() <-chan struct{}     { return nil }
func (*Session) Status() Status               { return Status{} }
func (*Session) Speaking(discord.UserID) bool { return false }
func (*Session) Latency() time.Duration       { return 0 }
func (*Session) Leave() error                 { return nil }
func (*Session) Toggle(bool) error            { return nil }

func (*Session) SetVolume(discord.UserID, float32) {}
func (*Session) SetDevices(string, string) error   { return nil }
func (*Session) SetSensitivity(float64)            {}
func (*Session) SetInputVolume(float32)            {}
func (*Session) SetOutputVolume(float32)           {}
func (*Session) SetPushToTalk(bool)                {}
func (*Session) SetTalking(bool)                   {}
func (*Session) SetNoiseSuppression(bool)          {}
func (*Session) SetAutoGain(bool)                  {}
func (*Session) SetNoiseStrength(float32)          {}

// Effects plays nothing.
type Effects struct{}

func NewEffects() *Effects                     { return new(Effects) }
func (*Effects) Play([]int16, string, float32) {}
func (*Effects) Stop()                         {}
func (*Effects) Playing() bool                 { return false }

func (*Session) SetAutoLevel(bool)           {}
func (*Session) SetClipLength(time.Duration) {}
func (*Session) ClipLength() time.Duration   { return 0 }
func (*Session) Clip([]discord.UserID, time.Duration) (map[discord.UserID][]int16, error) {
	return nil, errNoVoice
}

// Mix adds the samples of each clip together.
func Mix(map[discord.UserID][]int16) []int16 { return nil }

// SilentLevel is the level of silence, in dB.
const SilentLevel = -100

// MicTest captures nothing.
type MicTest struct{}

func StartMicTest(string) (*MicTest, error) { return nil, errNoVoice }

func (*MicTest) SetInputVolume(float32)   {}
func (*MicTest) SetOutputVolume(float32)  {}
func (*MicTest) Level() float64           { return SilentLevel }
func (*MicTest) Echo(string, bool) error  { return errNoVoice }
func (*MicTest) Echoing() bool            { return false }
func (*MicTest) Stop()                    {}
func (*MicTest) SetNoiseSuppression(bool) {}
func (*MicTest) SetAutoGain(bool)         {}
func (*MicTest) SetNoiseStrength(float32) {}

// Devices finds no device.
func Devices() (inputs, outputs []Device, err error) { return nil, nil, errNoVoice }

var errNoVoice = errors.New("built without voice (build tag: no_voice)")

func (*Session) Join(discord.ChannelID) error { return errNoVoice }

// Player plays nothing.
type Player struct{}

func NewPlayer() *Player { return new(Player) }

func (*Player) Updates() <-chan struct{}                  { return nil }
func (*Player) Status() PlayerStatus                      { return PlayerStatus{} }
func (*Player) Toggle(discord.AttachmentID) (bool, error) { return false, nil }
func (*Player) Play(discord.AttachmentID, []byte) error   { return errNoVoice }
func (*Player) Seek(time.Duration) error                  { return nil }

func (*Session) SetMuteDeafen(bool, bool) error { return nil }

// Recorder records nothing.
type Recorder struct{}

// RecordOptions are how what is recorded is processed.
type RecordOptions struct {
	Input          string
	Gain, Strength float32
	Denoise, AGC   bool
}

func StartRecorder(RecordOptions) (*Recorder, error) { return nil, errNoVoice }
func (*Recorder) Elapsed() time.Duration             { return 0 }
func (*Recorder) Level() float32                     { return 0 }
func (*Recorder) Stop() []int16                      { return nil }

// VoiceMessage makes nothing.
func VoiceMessage([]int16) ([]byte, float64, []byte, error) { return nil, 0, nil, errNoVoice }
