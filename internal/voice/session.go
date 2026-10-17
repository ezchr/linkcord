//go:build !no_voice

package voice

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/state"
	"github.com/ayn2op/arikawa/v3/utils/ws"
	"github.com/ayn2op/arikawa/v3/voice"
	"github.com/ayn2op/arikawa/v3/voice/udp"
	"github.com/ayn2op/arikawa/v3/voice/voicegateway"
	"github.com/disgoorg/godave"
	"github.com/gen2brain/malgo"
	dave "github.com/thomas-vilte/dave-go/session"
	"github.com/tphakala/go-opus/opus"
)

const (
	timeout = 15 * time.Second
	// hangover is how many frames are still sent after the last that was loud enough, for the ends of words not to be cut off.
	hangover = 15
)

// audio is what the audio devices are opened with. It is of the first backend that works but the null one, which malgo would otherwise silently use when the system has no audio library.
var audio = sync.OnceValues(func() (*malgo.AllocatedContext, error) {
	return malgo.InitContext([]malgo.Backend{
		malgo.BackendWasapi, malgo.BackendDsound, malgo.BackendWinmm,
		malgo.BackendCoreaudio,
		malgo.BackendSndio, malgo.BackendAudio4, malgo.BackendOss,
		malgo.BackendPulseaudio, malgo.BackendAlsa, malgo.BackendJack,
	}, malgo.ContextConfig{}, nil)
})

// devicePointers are the IDs of the devices chosen, copied once to C memory, where malgo needs them, and kept for the next time the device is chosen.
var (
	devicePointersMu sync.Mutex
	devicePointers   = map[malgo.DeviceID]unsafe.Pointer{}
)

func devicePointer(id malgo.DeviceID) unsafe.Pointer {
	devicePointersMu.Lock()
	defer devicePointersMu.Unlock()

	ptr, ok := devicePointers[id]
	if !ok {
		ptr = id.Pointer()
		devicePointers[id] = ptr
	}

	return ptr
}

// Devices returns the microphones and the speakers of the system.
func Devices() (inputs, outputs []Device, err error) {
	ctx, err := audio()
	if err != nil {
		return nil, nil, err
	}

	list := func(kind malgo.DeviceType) []Device {
		infos, err := ctx.Devices(kind)
		if err != nil {
			slog.Warn("failed to list audio devices", "err", err)
			return nil
		}

		devices := make([]Device, len(infos))
		for i, info := range infos {
			devices[i] = Device{ID: info.ID.String(), Name: info.Name(), Default: info.IsDefault != 0}
		}

		return devices
	}

	return list(malgo.Capture), list(malgo.Playback), nil
}

// startDevice starts the default playback or capture device, which calls data with what to fill or with what was captured.
func startDevice(kind malgo.DeviceType, channels int, data func(output, input []byte)) (*malgo.Device, error) {
	return startDeviceOn(kind, "", channels, data)
}

// startDeviceOn is startDevice for the device with the ID id, or the default one if id is "" or the device is no longer there.
func startDeviceOn(kind malgo.DeviceType, id string, channels int, data func(output, input []byte)) (*malgo.Device, error) {
	ctx, err := audio()
	if err != nil {
		return nil, err
	}

	config := malgo.DefaultDeviceConfig(kind)
	if id != "" {
		infos, _ := ctx.Devices(kind)
		for _, info := range infos {
			if info.ID.String() != id {
				continue
			}

			ptr := devicePointer(info.ID)
			if kind == malgo.Capture {
				config.Capture.DeviceID = ptr
			} else {
				config.Playback.DeviceID = ptr
			}
		}
	}
	config.SampleRate = sampleRate
	config.PeriodSizeInMilliseconds = 10
	config.Playback.Format, config.Playback.Channels = malgo.FormatS16, uint32(channels)
	config.Capture.Format, config.Capture.Channels = malgo.FormatS16, uint32(channels)

	device, err := malgo.InitDevice(ctx.Context, config, malgo.DeviceCallbacks{
		Data: func(output, input []byte, _ uint32) { data(output, input) },
	})
	if err != nil {
		return nil, err
	}

	if err := device.Start(); err != nil {
		device.Uninit()
		return nil, err
	}

	return device, nil
}

// Session is the voice channel that the user is in, if any.
type Session struct {
	state *state.State
	// threshold is how loud a sample of the microphone must be for what is said to be sent.
	threshold atomic.Int64
	// proc readies what the microphone captures to be sent, and volume is how many times as loud everyone is played, as the bits of a float32.
	proc   *processor
	volume atomic.Uint32
	// pushToTalk sends what the microphone captures only while talking, which a key held reports, rather than when it is loud enough.
	pushToTalk, talking atomic.Bool
	// updates is sent to when the status or who is speaking changes.
	updates chan struct{}
	// mic is sent what the microphone captures.
	mic chan []byte

	// mu is held while joining, leaving and muting, which are not done while another is.
	mu sync.Mutex
	// cancelJoin stops the join going on, if any.
	cancelJoin atomic.Pointer[context.CancelFunc]
	// joinedChannel is the channel last joined.
	joinedChannel discord.ChannelID
	// session is nil until the first voice channel is joined.
	session atomic.Pointer[voice.Session]
	// playback plays while a voice channel is joined, and capture captures while the user is not muted in it.
	playback, capture *malgo.Device

	// input and output are the IDs of the microphone and the speakers chosen, "" for the default ones.
	input, output string

	stateMu  sync.Mutex
	status   Status
	speaking map[discord.UserID]bool
	speakers map[uint32]*speaker
	// volumes are how loud the users not played as they are sent are played, 1 being as they are sent.
	volumes map[discord.UserID]float32
	// clips keeps what is said for clips, and autoLevel lowers voices that
	// come in saturated.
	clips     clipBuffer
	autoLevel atomic.Bool
	// fake is what the user pretends to the others, if anything.
	fake atomic.Pointer[fakeState]

	// e2ee encrypts end to end in the voice channel joined, made anew by
	// each voice server; it is kept for the log.
	e2ee atomic.Pointer[dave.Session]
	// sent counts the voice packets sent since the last report, and failed
	// those that could not be; failedAt is when one last failed to be logged.
	sent, failed atomic.Uint64
	// received counts the packets heard and decrypted.
	received atomic.Uint64
	// leftAt is when this end last left a voice channel itself, in Unix
	// nanoseconds.
	leftAt atomic.Int64
	// linkcordOnly makes what the user says heard by Linkcord only.
	linkcordOnly atomic.Bool
	// healedDAVE is when the channel was last joined again for keys that
	// stayed broken, in Unix nanoseconds.
	healedDAVE atomic.Int64
	failedAt     time.Time
}

// New returns a Session that sends what the microphone captures louder than sensitivity, in dB.
func New(state *state.State, sensitivity float64) *Session {
	s := &Session{
		state:    state,
		proc:     newProcessor(),
		updates:  make(chan struct{}, 1),
		mic:      make(chan []byte, 4),
		speaking: make(map[discord.UserID]bool),
		speakers: make(map[uint32]*speaker),
		volumes:  make(map[discord.UserID]float32),
	}
	s.SetSensitivity(sensitivity)
	s.SetInputVolume(1)
	s.SetOutputVolume(1)
	state.AddHandler(s.follow)

	return s
}

// SetSensitivity sends what the microphone captures louder than sensitivity, in dB.
func (s *Session) SetSensitivity(sensitivity float64) {
	s.threshold.Store(int64(math.Pow(10, sensitivity/20) * math.MaxInt16))
}

// SetInputVolume sends what the microphone captures gain times as loud: 0 silences it, and 2 is twice as loud.
func (s *Session) SetInputVolume(gain float32) {
	s.proc.gain.Store(math.Float32bits(gain))
}

// SetNoiseSuppression removes the noise around what is said, such as of fans and keyboards, if on.
func (s *Session) SetNoiseSuppression(on bool) {
	s.proc.denoise.Store(on)
}

// SetNoiseStrength removes strength of the noise, from 0 to 1, when the noise suppression is on.
func (s *Session) SetNoiseStrength(strength float32) {
	s.proc.strength.Store(math.Float32bits(strength))
}

// SetAutoGain evens out how loud what is said is sent, if on. Whether on or not, it is kept from clipping.
func (s *Session) SetAutoGain(on bool) {
	s.proc.autoGain.Store(on)
}

// SetOutputVolume plays everyone volume times as loud, on top of how loud each is played.
func (s *Session) SetOutputVolume(volume float32) {
	s.volume.Store(math.Float32bits(volume))
}

// SetPushToTalk sends what the microphone captures only while SetTalking says the user talks, if on, or whenever it is loud enough otherwise.
func (s *Session) SetPushToTalk(on bool) {
	s.pushToTalk.Store(on)
}

// SetTalking says whether the key to talk is held, with push to talk.
func (s *Session) SetTalking(talking bool) {
	s.talking.Store(talking)
}

// follow keeps up with the user being moved to another voice channel or disconnected, by someone else or by connecting from another client.
func (s *Session) follow(event *gateway.VoiceStateUpdateEvent) {
	me, _ := s.state.Cabinet.Me()
	if me == nil || event.UserID != me.ID {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	status, own := s.Status(), event.SessionID == s.state.Ready().SessionID
	// Leaving, then joining, as joining again does, has the news of the leave
	// come once the join is done, as it waits for the join: it is not news.
	if own && !event.ChannelID.IsValid() && status.Joined && time.Since(time.Unix(0, s.leftAt.Load())) < 10*time.Second {
		return
	}
	switch {
	case !status.Joined || own && event.ChannelID == status.ChannelID:
	case own && event.ChannelID.IsValid():
		s.setStatus(Status{ChannelID: event.ChannelID, Joined: true})
	default:
		s.left()
	}
}

// Updates is sent to when the status or who is speaking changes.
func (s *Session) Updates() <-chan struct{} {
	return s.updates
}

func (s *Session) Status() Status {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	return s.status
}

// Speaking reports whether the user is speaking in the voice channel joined.
func (s *Session) Speaking(userID discord.UserID) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	return s.speaking[userID]
}

// SetVolume plays the user volume times as loud as they are sent: 0 silences them, and 2 is twice as loud.
func (s *Session) SetVolume(userID discord.UserID, volume float32) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	if volume == 1 {
		delete(s.volumes, userID)
	} else {
		s.volumes[userID] = volume
	}
}

// SetDevices captures what the user says with the microphone with the ID input and plays the voice channel on the speakers with the ID output, "" being the default ones. A device in use is changed at once.
func (s *Session) SetDevices(input, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	if input != s.input {
		s.input = input
		if s.capture != nil {
			s.capture.Uninit()
			s.capture = nil
			errs = append(errs, setDevice(&s.capture, true, malgo.Capture, input, s.captured))
		}
	}

	if output != s.output {
		s.output = output
		if s.playback != nil {
			s.playback.Uninit()
			s.playback = nil
			errs = append(errs, setDevice(&s.playback, true, malgo.Playback, output, s.mix))
		}
	}

	return errors.Join(errs...)
}

// Latency returns how long the voice server of the voice channel joined takes to answer, or 0 if it is not known.
func (s *Session) Latency() time.Duration {
	if session := s.session.Load(); session != nil {
		return session.Latency()
	}

	return 0
}

// update changes the state with f and tells Updates if f reports that it did.
func (s *Session) update(f func() bool) {
	s.stateMu.Lock()
	changed := f()
	s.stateMu.Unlock()

	if changed {
		select {
		case s.updates <- struct{}{}:
		default:
		}
	}
}

// setStatus changes the status, which whoever spoke and was heard does not outlast.
func (s *Session) setStatus(status Status) {
	s.update(func() bool {
		s.status = status
		clear(s.speaking)
		clear(s.speakers)
		return true
	})
}

func (s *Session) setSpeaking(userID discord.UserID, speaking bool) {
	s.update(func() bool {
		changed := s.speaking[userID] != speaking
		s.speaking[userID] = speaking
		return changed
	})
}

// setDevice starts device, which then calls data, if on, and stops it otherwise.
func setDevice(device **malgo.Device, on bool, kind malgo.DeviceType, id string, data func(output, input []byte)) (err error) {
	switch {
	case on && *device == nil:
		*device, err = startDeviceOn(kind, id, channels, data)
	case !on && *device != nil:
		(*device).Uninit()
		*device = nil
	}

	return err
}

// setDevices captures what the user says unless mute, and plays what is said unless deafen.
func (s *Session) setDevices(mute, deafen bool) error {
	err := errors.Join(
		setDevice(&s.capture, !mute, malgo.Capture, s.input, s.captured),
		setDevice(&s.playback, !deafen, malgo.Playback, s.output, s.mix),
	)
	if mute {
		// Silence makes what is being said end.
		s.queue(make([]byte, 2*hangover*frameBytes))
	}

	return err
}

// left stops the devices, in no voice channel.
func (s *Session) left() {
	s.setDevices(true, true)
	s.setStatus(Status{})
	s.joinedChannel = 0
}

// Join joins the voice channel, muted, leaving the one that the user is in.
func (s *Session) Join(channelID discord.ChannelID) (err error) {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()

	s.setStatus(Status{ChannelID: channelID})

	defer func() {
		// Discord may have the user in the voice channel regardless, and joining it again would then do nothing.
		if err != nil {
			s.leave()
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	// Leaving while joining stops the join.
	s.cancelJoin.Store(&cancel)
	defer s.cancelJoin.Store(nil)

	session := s.session.Load()
	if session == nil {
		if session, err = voice.NewSession(s.state, s.state.Handler); err != nil {
			return err
		}

		session.DAVE = func(logger *slog.Logger, userID godave.UserID, callbacks godave.Callbacks) godave.Session {
			e2ee := dave.New(userID, callbacks, dave.WithLogger(logger))
			s.e2ee.Store(e2ee)
			return e2ee
		}
		session.AddHandler(func(event *ws.BackgroundErrorEvent) { slog.Debug("voice gateway error", "err", event.Err) })
		session.SetLinkcordOnly(s.linkcordOnly.Load())
		s.session.Store(session)

		go s.receive(session)
		go s.send(session)
		go s.report()
		go s.watchDevices()
	}

	if err := s.setDevices(true, false); err != nil {
		return err
	}

	// Moving between a server's channel and a call of a direct message or
	// group is a leave and a join: moved in place, the connection waited for
	// what Discord had already sent, and the call never connected.
	if prev := s.joinedChannel; prev.IsValid() && prev != channelID && s.crossesKinds(prev, channelID) {
		s.leftAt.Store(time.Now().UnixNano())
		if err := session.Leave(ctx); err != nil {
			slog.Warn("could not leave before moving to the call", "err", err)
		}
	}

	if err := session.JoinChannel(ctx, channelID, true, false); err != nil {
		return err
	}

	// The voice server sends nothing before it is told this.
	if err := session.Speaking(ctx, voicegateway.Microphone); err != nil {
		return err
	}

	s.setStatus(Status{ChannelID: channelID, Joined: true})
	s.joinedChannel = channelID

	return nil
}

// crossesKinds reports whether one of a and b is a server's channel and the
// other a call of a direct message or group.
func (s *Session) crossesKinds(a, b discord.ChannelID) bool {
	ca, err := s.state.Channel(a)
	if err != nil {
		return true
	}
	cb, err := s.state.Channel(b)
	if err != nil {
		return true
	}
	return ca.GuildID.IsValid() != cb.GuildID.IsValid()
}

// Leave leaves the voice channel that the user is in, if any.
func (s *Session) Leave() error {
	if !s.mu.TryLock() {
		// A join is going on: stopped, it leaves on its own.
		if c := s.cancelJoin.Load(); c != nil {
			(*c)()
		}
		return nil
	}
	defer s.mu.Unlock()

	return s.leave()
}

func (s *Session) leave() error {
	defer s.left()
	s.leftAt.Store(time.Now().UnixNano())

	session := s.session.Load()
	if session == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return session.Leave(ctx)
}

// Toggle mutes or unmutes the user in the voice channel joined, if any, or deafens or undeafens them if deafen. Deafening also mutes, and unmuting also undeafens.
func (s *Session) Toggle(deafen bool) error {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()

	status := s.Status()
	channel, err := s.state.Cabinet.Channel(status.ChannelID)
	if err != nil || !status.Joined {
		return nil
	}

	mute, deafen := deafen || s.capture != nil, deafen && s.playback != nil
	if err := s.setDevices(mute, deafen); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return s.state.SendGateway(ctx, s.voiceState(channel.GuildID, channel.ID, mute, deafen))
}

// SetMuteDeafen mutes and deafens the user, or not, as given, in the voice
// channel joined.
func (s *Session) SetMuteDeafen(mute, deafen bool) error {
	if !s.mu.TryLock() {
		return nil
	}
	defer s.mu.Unlock()

	status := s.Status()
	channel, err := s.state.Cabinet.Channel(status.ChannelID)
	if err != nil || !status.Joined {
		return nil
	}
	mute = mute || deafen
	if err := s.setDevices(mute, deafen); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return s.state.SendGateway(ctx, s.voiceState(channel.GuildID, channel.ID, mute, deafen))
}

// captured queues what the microphone captured. It is what the capture device calls.
func (s *Session) captured(_, input []byte) {
	s.queue(bytes.Clone(input))
}

// queue queues what was captured to be sent, in place of the oldest if what is queued is not being sent fast enough, for what is said not to be sent ever later.
func (s *Session) queue(pcm []byte) {
	for {
		select {
		case s.mic <- pcm:
			return
		default:
		}

		select {
		case <-s.mic:
		default:
		}
	}
}

// mix fills output with what everyone says. It is what the playback device calls.
func (s *Session) mix(output, _ []byte) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()

	master := math.Float32frombits(s.volume.Load())
	for _, speaker := range s.speakers {
		volume, ok := s.volumes[speaker.user]
		if !ok {
			volume = 1
		}

		speaker.mix(output, volume*master, s.autoLevel.Load())
	}
}

// SetAutoLevel lowers voices that come in saturated, as some microphones
// make them, until they are not, or stops.
func (s *Session) SetAutoLevel(on bool) { s.autoLevel.Store(on) }

// receive buffers what each user says in the voice channels that session joins.
func (s *Session) receive(session *voice.Session) {
	var (
		// heard is when what was said could last be decrypted, and failed how
		// many packets could not since; healed is when the voice channel was
		// last joined again for it. logged are the SSRCs whose failure was
		// logged since they were last heard.
		heard  = time.Now()
		failed int
		healed time.Time
		logged = make(map[uint32]bool)
	)
	for {
		packet, err := session.ReadPacket()
		if errors.Is(err, udp.ErrDecryptionFailed) {
			failed++
			var de *voice.DecryptError
			if errors.As(err, &de) && !logged[de.SSRC] {
				logged[de.SSRC] = true
				slog.Warn("voice packets cannot be decrypted", "ssrc", de.SSRC, "user", session.User(de.SSRC), "err", de.Err)
			}
			// Nothing anyone says can be decrypted any more, as when the keys
			// of the call changed, someone joining, without this end
			// following: joining again gets the keys anew. One person who
			// cannot be decrypted is their client's doing, which joining
			// again does not mend.
			if failed >= 100 && time.Since(heard) > 5*time.Second && time.Since(healed) > time.Minute {
				healed, failed = time.Now(), 0
				slog.Warn("nothing said in the voice channel can be decrypted; joining it again", "err", err)
				go s.rejoin()
			}
			continue
		}

		if err != nil {
			// In no voice channel, or joining one.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		// Silence is sent unencrypted, and tells nothing of whether the
		// keys work: heard only by what was said.
		if string(packet.Opus) != voice.Silence {
			heard, failed = time.Now(), 0
			delete(logged, packet.SSRC())
		}

		s.stateMu.Lock()
		speaker := s.speakers[packet.SSRC()]
		if speaker == nil {
			if speaker, err = newSpeaker(packet.Sequence()); err != nil {
				s.stateMu.Unlock()
				slog.Error("failed to create Opus decoder", "err", err)
				return
			}

			s.speakers[packet.SSRC()] = speaker
		}

		user := session.User(packet.SSRC())
		speaker.user = user
		s.stateMu.Unlock()
		s.received.Add(1)
		s.clips.record(user, packet.Opus)

		if err := speaker.write(packet.Sequence(), packet.Opus); err != nil {
			slog.Debug("failed to decode voice packet", "err", err, "ssrc", packet.SSRC())
		}

		s.setSpeaking(user, string(packet.Opus) != voice.Silence)
	}
}

// rejoin leaves the voice channel joined and joins it again, muted and
// deafened as the user was, which gets new keys from Discord.
func (s *Session) rejoin() {
	status := s.Status()
	if !status.Joined {
		return
	}
	s.mu.Lock()
	muted, deafened := s.capture == nil, s.playback == nil
	s.mu.Unlock()

	if err := s.Leave(); err != nil {
		slog.Warn("could not leave the voice channel to join it again", "err", err)
	}
	if err := s.Join(status.ChannelID); err != nil {
		slog.Warn("could not join the voice channel again", "err", err)
		return
	}
	if !muted {
		s.Toggle(false)
	}
	if deafened {
		s.Toggle(true)
	}
}

// watchDevices starts again, every two seconds, the microphone or speakers
// in use if they stopped, as one does when Windows loses it for a moment, a
// headset going to sleep or its driver resetting: everyone would otherwise
// stay silent until the voice channel is joined again.
func (s *Session) watchDevices() {
	for range time.Tick(2 * time.Second) {
		if !s.Status().Joined {
			continue
		}
		// Keys that stay broken send what is said with keys the others no
		// longer have: Discord's own clients hear nothing. Joining again
		// gets new ones.
		if e2ee := s.e2ee.Load(); e2ee != nil {
			if since := e2ee.State().DegradedSince; !since.IsZero() && time.Since(since) > 20*time.Second &&
				time.Since(time.Unix(0, s.healedDAVE.Load())) > 2*time.Minute {
				s.healedDAVE.Store(time.Now().UnixNano())
				slog.Warn("the voice channel's keys stayed broken; joining it again", "since", since)
				go s.rejoin()
			}
		}
		s.mu.Lock()
		restart := func(device **malgo.Device, kind malgo.DeviceType, id string, data func(output, input []byte), name string) {
			if *device == nil || (*device).IsStarted() {
				return
			}
			(*device).Uninit()
			*device = nil
			err := setDevice(device, true, kind, id, data)
			slog.Warn("the "+name+" stopped; started it again", "err", err)
		}
		restart(&s.playback, malgo.Playback, s.output, s.mix, "speakers")
		restart(&s.capture, malgo.Capture, s.input, s.captured, "microphone")
		s.mu.Unlock()
	}
}

// report logs, every minute in a voice channel, what was sent and how it
// was encrypted, which tells why the others would not hear the user.
func (s *Session) report() {
	for range time.Tick(time.Minute) {
		sent, failed, received := s.sent.Swap(0), s.failed.Swap(0), s.received.Swap(0)
		if !s.Status().Joined {
			continue
		}
		s.mu.Lock()
		playing := s.playback != nil && s.playback.IsStarted()
		s.mu.Unlock()
		args := []any{"sent", sent, "failed", failed, "received", received, "playing", playing}
		if e2ee := s.e2ee.Load(); e2ee != nil {
			st, stats := e2ee.State(), e2ee.Stats()
			args = append(args, "dave_ready", st.Ready, "dave_epoch", st.EpochID, "dave_version", st.ProtocolVersion,
				"degraded_since", st.DegradedSince, "passthrough", stats.PassthroughFrames, "encrypt_failures", stats.EncryptFailures,
				"decrypt_failures", stats.DecryptFailures, "recoveries", stats.RecoveryAttempts)
		}
		slog.Info("voice stats", args...)
	}
}

// send sends what the microphone captures to the voice channels that session joins, while it is loud enough.
func (s *Session) send(session *voice.Session) {
	encoder, err := opus.NewEncoder(opus.EncoderConfig{SampleRate: sampleRate, Channels: channels})
	if err != nil {
		slog.Error("failed to create Opus encoder", "err", err)
		return
	}

	me, _ := s.state.Cabinet.Me()

	var (
		pcm     []byte
		samples = make([]int16, frameSamples)
		packet  = make([]byte, 4000)
		silence = []byte(voice.Silence)
		// quiet is how many frames ago the last that was loud enough was.
		quiet = hangover
	)

	for captured := range s.mic {
		for pcm = append(pcm, captured...); len(pcm) >= frameBytes; pcm = pcm[frameBytes:] {
			quiet++
			threshold := int(s.threshold.Load())
			for i := range samples {
				samples[i] = int16(binary.LittleEndian.Uint16(pcm[2*i:]))
			}
			// Made as loud as chosen, without noise and evened out, before it
			// is told whether loud enough to send.
			s.proc.process(samples)
			for i := range samples {
				if int(samples[i]) >= threshold || -int(samples[i]) >= threshold {
					quiet = 0
				}
			}

			// With push to talk, what is said is sent while the key is held, however quiet, and not after.
			if s.pushToTalk.Load() {
				if s.talking.Load() {
					quiet = 0
				} else {
					quiet = max(quiet, hangover)
				}
			}

			var err error
			switch {
			case quiet < hangover:
				var n int
				if n, err = encoder.Encode(samples, packet); err == nil {
					_, err = session.Write(packet[:n])
					if me != nil {
						s.clips.record(me.ID, packet[:n])
					}
				}
			case quiet < hangover+5:
				// A few frames of silence are for what was said not to be interpolated with what is said next.
				_, err = session.Write(silence)
			}

			switch {
			case err != nil:
				s.failed.Add(1)
				// What stops what is said from being sent is logged, at most
				// once a minute.
				if time.Since(s.failedAt) > time.Minute {
					s.failedAt = time.Now()
					slog.Warn("failed to send voice packet", "err", err)
				}
			case quiet < hangover+5:
				s.sent.Add(1)
			}

			if me != nil {
				s.setSpeaking(me.ID, quiet < hangover)
			}
		}
	}
}

// SetLinkcordOnly makes what the user says heard by Linkcord users only, or
// by everyone: Discord's own clients cannot decrypt it.
func (s *Session) SetLinkcordOnly(on bool) {
	s.linkcordOnly.Store(on)
	if session := s.session.Load(); session != nil {
		session.SetLinkcordOnly(on)
	}
}
