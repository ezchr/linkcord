//go:build !no_voice

package voice

import (
	"slices"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/tphakala/go-opus/opus"
)

// frame is how long an Opus packet of the voice channel lasts.
const frame = 20 * time.Millisecond

// clipPacket is a packet someone sent, when it arrived.
type clipPacket struct {
	at   time.Time
	opus []byte
}

// clipBuffer keeps what was said in the voice channel, as the Opus packets
// it came in, about 30 MB an hour for each who talks, for clips of the
// last minutes to be made from. What is older than keep goes.
type clipBuffer struct {
	mu    sync.Mutex
	keep  time.Duration
	users map[discord.UserID][]clipPacket
}

// record keeps a packet user sent, or the user did.
func (b *clipBuffer) record(user discord.UserID, packet []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.keep <= 0 || !user.IsValid() {
		return
	}
	if b.users == nil {
		b.users = make(map[discord.UserID][]clipPacket)
	}
	now := time.Now()
	packets := append(b.users[user], clipPacket{at: now, opus: slices.Clone(packet)})
	// What is older than the clips can be goes, a second's worth at once.
	if old := now.Add(-b.keep); packets[0].at.Before(old.Add(-time.Second)) {
		i, _ := slices.BinarySearchFunc(packets, old, func(p clipPacket, t time.Time) int { return p.at.Compare(t) })
		packets = slices.Delete(packets, 0, i)
	}
	b.users[user] = packets
}

// SetClipLength keeps what is said for clips of up to d, or keeps nothing
// if d is zero, letting go of what was kept.
func (s *Session) SetClipLength(d time.Duration) {
	b := &s.clips
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keep = d
	if d <= 0 || b.users == nil {
		b.users = make(map[discord.UserID][]clipPacket)
	}
}

// ClipLength is how long clips can be, zero when none are kept.
func (s *Session) ClipLength() time.Duration {
	s.clips.mu.Lock()
	defer s.clips.mu.Unlock()
	return s.clips.keep
}

// Clip returns what each of users said in the last d, all who talked if
// users is empty, as 48 kHz stereo samples d long, with silence where they
// did not talk.
func (s *Session) Clip(users []discord.UserID, d time.Duration) (map[discord.UserID][]int16, error) {
	b := &s.clips
	start := time.Now().Add(-d)
	b.mu.Lock()
	taken := make(map[discord.UserID][]clipPacket)
	for user, packets := range b.users {
		if len(users) > 0 && !slices.Contains(users, user) {
			continue
		}
		i, _ := slices.BinarySearchFunc(packets, start, func(p clipPacket, t time.Time) int { return p.at.Compare(t) })
		if i < len(packets) {
			taken[user] = slices.Clone(packets[i:])
		}
	}
	b.mu.Unlock()

	frames := int(d / frame)
	out := make(map[discord.UserID][]int16, len(taken))
	tmp := make([]int16, 6*frameSamples)
	for user, packets := range taken {
		decoder, err := opus.NewDecoder(sampleRate, channels)
		if err != nil {
			return nil, err
		}
		pcm := make([]int16, frames*frameSamples)
		// Each packet goes where it arrived, or right after the last when
		// it came early, as packets arrive a little unevenly.
		prev := -1
		for _, p := range packets {
			slot := int(p.at.Sub(start) / frame)
			if slot <= prev {
				slot = prev + 1
			}
			if slot >= frames {
				break
			}
			n, err := decoder.Decode(p.opus, tmp)
			if err != nil {
				continue
			}
			copy(pcm[slot*frameSamples:], tmp[:n*channels])
			prev = slot + max(n*channels/frameSamples, 1) - 1
		}
		out[user] = pcm
	}
	return out, nil
}

// Mix adds the samples of each clip together, as they would be heard.
func Mix(clips map[discord.UserID][]int16) []int16 {
	var out []int16
	for _, pcm := range clips {
		if len(pcm) > len(out) {
			out = append(out, make([]int16, len(pcm)-len(out))...)
		}
		for i, v := range pcm {
			out[i] = int16(max(-1<<15, min(1<<15-1, int32(out[i])+int32(v))))
		}
	}
	return out
}

// levelGuard lowers a voice that comes in saturated, near the loudest it
// can be, as some microphones do, and lets it back up slowly once it is
// not: gain is what it is played at, 1 when it was never too loud.
type levelGuard struct {
	gain float32
}

const (
	// saturated is how loud a voice must come in to be lowered, and
	// guardTarget what it is lowered to, of the loudest a sample can be.
	saturated   = 0.7 * 32767
	guardTarget = 0.35 * 32767
	// guardRelease is how much the gain comes back up each time it is
	// mixed, every 10ms: from half, in about three seconds.
	guardRelease = 0.0017
)

// apply is the gain for samples whose loudest is peak, played volume times
// as loud.
func (g *levelGuard) apply(peak, volume float32) float32 {
	if g.gain == 0 {
		g.gain = 1
	}
	if peak >= saturated && peak*volume*g.gain > guardTarget {
		g.gain = guardTarget / (peak * volume)
	} else if g.gain < 1 {
		g.gain = min(1, g.gain+guardRelease)
	}
	return g.gain
}
