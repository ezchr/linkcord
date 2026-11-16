//go:build !no_voice

package voice

import (
	"encoding/binary"
	"math"
	"math/rand"
	"testing"
)

// madeUpVoice is seconds of made up speech, 48 kHz mono: syllables of a buzz
// with a moving pitch through three resonances, or of hiss, with pauses
// between words, as loud as speech. Each seed is another speaker.
func madeUpVoice(seed int64, seconds float64) []int16 {
	r := rand.New(rand.NewSource(seed))
	out := make([]int16, int(seconds*sampleRate))
	pitch := 90 + 120*r.Float64()
	var phase float64
	var y1, y2 [3]float64
	for at := 0; at < len(out); {
		// A pause after some syllables.
		if r.Float64() < 0.3 {
			at += int((0.1 + 0.6*r.Float64()) * sampleRate)
		}
		n := int((0.08 + 0.2*r.Float64()) * sampleRate)
		hiss := r.Float64() < 0.2
		f0 := pitch * (0.8 + 0.4*r.Float64())
		formants := [3]float64{300 + 600*r.Float64(), 900 + 1500*r.Float64(), 2500 + 1000*r.Float64()}
		loud := 0.4 + 0.6*r.Float64()
		for i := 0; i < n && at < len(out); i, at = i+1, at+1 {
			var x float64
			if hiss {
				x = (r.Float64()*2 - 1) * 0.3
			} else {
				// A pulse every period of the pitch.
				phase += f0 * (1 + 0.1*float64(i)/float64(n)) / sampleRate
				if phase >= 1 {
					phase--
					x = 1
				}
			}
			var v float64
			for k, f := range formants {
				c := 2 * math.Exp(-math.Pi*120/sampleRate) * math.Cos(2*math.Pi*f/sampleRate)
				d := -math.Exp(-2 * math.Pi * 120 / sampleRate)
				y := x + c*y1[k] + d*y2[k]
				y1[k], y2[k] = y, y1[k]
				v += y / float64(k+1)
			}
			env := math.Min(1, math.Min(float64(i), float64(n-i))/480)
			out[at] = int16(math.Max(-30000, math.Min(30000, v*env*loud*900)))
		}
	}
	return out
}

// room is how what the speakers play arrives at the microphone.
type room struct {
	// delay is how late it arrives, in samples: the delay of both devices
	// and the way from the speakers to the microphone.
	delay int
	// gain is how loud it arrives.
	gain float64
	// drift is how much faster the clock of the microphone runs than that
	// of the speakers, 0.0001 being 100 parts in a million.
	drift float64
}

// echoOf is what the microphone captures of far in the room: late, quieter
// and with the reflections of the walls, which fade over 100 ms.
func (rm room) echoOf(far []int16) []float64 {
	r := rand.New(rand.NewSource(7))
	type reflection struct {
		at   int
		gain float64
	}
	walls := []reflection{{0, 1}}
	for range 40 {
		at := 48 + r.Intn(4800)
		walls = append(walls, reflection{at, (r.Float64()*2 - 1) * 0.5 * math.Exp(-float64(at)/1000)})
	}
	direct := make([]float64, len(far))
	for i := range far {
		var v float64
		for _, w := range walls {
			if j := i - rm.delay - w.at; j >= 0 {
				v += float64(far[j]) * w.gain
			}
		}
		direct[i] = v * rm.gain
	}
	if rm.drift == 0 {
		return direct
	}
	drifted := make([]float64, len(far))
	for i := range drifted {
		at := float64(i) * (1 + rm.drift)
		j := int(at)
		if j+1 >= len(direct) {
			break
		}
		f := at - float64(j)
		drifted[i] = direct[j]*(1-f) + direct[j+1]*f
	}
	return drifted
}

// call runs e over a call in which far is played and the microphone
// captures its echo and near, as the devices give them: 20 ms played, then
// 20 ms captured. It returns what is left of what was captured, mono, and
// whether the gate took each frame for the user. far or near may be nil.
func call(e *echo, far []int16, echo []float64, near []int16) (out []int16, user []bool) {
	n := max(len(far), len(near))
	const frame = frameSamples / channels
	output := make([]byte, frameBytes)
	pcm := make([]int16, frameSamples)
	for at := 0; at+frame <= n; at += frame {
		if far != nil {
			for i := range frame {
				binary.LittleEndian.PutUint16(output[4*i:], uint16(far[at+i]))
				binary.LittleEndian.PutUint16(output[4*i+2:], uint16(far[at+i]))
			}
			e.played(output)
		}
		for i := range frame {
			var v float64
			if echo != nil {
				v = echo[at+i]
			}
			if near != nil {
				v += float64(near[at+i])
			}
			s := int16(math.Max(-32768, math.Min(32767, v)))
			pcm[2*i], pcm[2*i+1] = s, s
		}
		user = append(user, e.capture(pcm))
		for i := range frame {
			out = append(out, pcm[2*i])
		}
	}
	return out, user
}

func powerOf[T int16 | float64](pcm []T) float64 {
	var sum float64
	for _, v := range pcm {
		sum += float64(v) * float64(v)
	}
	return sum / float64(max(len(pcm), 1))
}

// sent is whether each frame of 20 ms of pcm would be sent, as send does
// it: from a frame that the gate took for the user and that is loud enough,
// once the automatic gain control has raised it, until hangover frames
// after the last such.
func sent[T int16 | float64](pcm []T, user []bool) []bool {
	out := make([]bool, len(user))
	quiet := hangover
	for i, u := range user {
		quiet++
		if u && powerOf(pcm[i*960:(i+1)*960]) > 55*55 {
			quiet = 0
		}
		out[i] = quiet < hangover
	}
	return out
}

// count is how many of the frames in which pcm is louder than level, as
// loud as a sample, are sent, and how many there are.
func count[T int16 | float64](pcm []T, sent []bool, level float64) (n, of int) {
	for i, s := range sent {
		if powerOf(pcm[i*960:(i+1)*960]) > level*level {
			of++
			if s {
				n++
			}
		}
	}
	return n, of
}

func dB(ratio float64) float64 { return 10 * math.Log10(math.Max(ratio, 1e-12)) }

// likeness is how alike out is to want, from 0 to 1, once out is moved
// earlier by the lag at which they are most alike, up to 40 ms, and that
// lag, in samples: how late out is.
func likeness(out, want []int16) (float64, int) {
	best, lag := 0.0, 0
	for d := 0; d <= 1920; d += 8 {
		var ab, aa, bb float64
		for i := 0; i+d < len(out) && i < len(want); i += 4 {
			a, b := float64(out[i+d]), float64(want[i])
			ab, aa, bb = ab+a*b, aa+a*a, bb+b*b
		}
		if c := ab / math.Sqrt(aa*bb+1); c > best {
			best, lag = c, d
		}
	}
	return best, lag
}

// The echo of the others, alone, is removed: by the canceller for the most
// part, and what it leaves is not taken for the user by the gate.
func TestEchoRemoved(t *testing.T) {
	far := madeUpVoice(1, 30)
	for _, rm := range []struct {
		name string
		room room
		want float64
		// sent is how many frames of what is left may be sent at most.
		sent int
	}{
		{"80 ms late", room{delay: 3840, gain: 0.3}, 25, 0},
		{"250 ms late", room{delay: 12000, gain: 0.3}, 25, 0},
		{"as loud as played", room{delay: 3840, gain: 1}, 25, 0},
		{"clock 100 ppm fast", room{delay: 3840, gain: 0.3, drift: 0.0001}, 20, 0},
		// The canceller loses the room for a moment every few seconds.
		{"clock 500 ppm fast", room{delay: 3840, gain: 0.3, drift: 0.0005}, 15, 40},
	} {
		echo := rm.room.echoOf(far)
		e := newEcho()
		out, user := call(e, far, echo, nil)
		_, _, delay := e.aec.Metrics()
		e.reset()

		// After the first 5 seconds, in which the room is learned.
		const from = 5 * sampleRate
		erle := dB(powerOf(echo[from:len(out)]) / powerOf(out[from:]))
		first := dB(powerOf(echo[:sampleRate]) / powerOf(out[:sampleRate]))
		// What is sent of what the canceller left, loud enough to hear,
		// and of the first 5 seconds, whatever was left of them.
		heard, loud := count(out[from:], sent(out, user)[from/960:], 55)
		early, _ := count(echo[:from], sent(out[:from], user[:from/960]), 55)
		t.Logf("%s: echo lowered by %.1f dB (%.1f dB in the first second), delay found %d ms; of what was left, %d of %d frames loud enough to hear sent, and %d in the first 5 seconds",
			rm.name, erle, first, delay, heard, loud, early)
		if erle < rm.want {
			t.Errorf("%s: echo lowered by %.1f dB, want %.0f or more", rm.name, erle, rm.want)
		}
		if heard > rm.sent || early > 0 {
			t.Errorf("%s: %d frames of echo sent, and %d in the first 5 seconds", rm.name, heard, early)
		}
	}
}

// The user talking while the others talk is still heard, about as loud,
// and the gate takes it for the user.
func TestEchoDoubleTalk(t *testing.T) {
	far, near := madeUpVoice(1, 30), madeUpVoice(2, 30)
	// The user starts once the room is learned.
	clear(near[:5*sampleRate])
	echo := room{delay: 3840, gain: 0.3}.echoOf(far)
	e := newEcho()
	defer e.reset()
	out, user := call(e, far, echo, near)

	const from = 5 * sampleRate
	alike, lag := likeness(out[from:], near[from:])
	// How loud the user comes out, and whether it is sent, where the user
	// speaks.
	var in, left float64
	for at := from; at+960+lag <= len(out); at += 960 {
		if p := powerOf(near[at : at+960]); p > 200*200 {
			in += p
			left += powerOf(out[at+lag : at+lag+960])
		}
	}
	opened, spoken := count(near[from:], user[from/960:], 200)
	heard, _ := count(near[from:], sent(out, user)[from/960:], 200)
	t.Logf("the user comes out %.1f dB louder, %.2f alike; of %d frames spoken, %d taken for the user and %d sent; echo was %.1f dB below the user",
		dB(left/in), alike, spoken, opened, heard, dB(powerOf(near[from:])/powerOf(echo[from:])))
	if change := dB(left / in); change < -3 || change > 1 {
		t.Errorf("the user comes out %.1f dB louder, want from -3 to 1", change)
	}
	if alike < 0.9 {
		t.Errorf("what comes out is %.2f alike to what the user said, want 0.9 or more", alike)
	}
	if heard < spoken*9/10 {
		t.Errorf("%d of %d frames spoken sent", heard, spoken)
	}
}

// With nothing played, as with headphones, the user comes out as said, no
// more than a frame late, and every frame is the user's.
func TestEchoNothingPlayed(t *testing.T) {
	near := madeUpVoice(2, 10)
	silence := make([]int16, len(near))
	for name, far := range map[string][]int16{"nothing played": nil, "silence played": silence} {
		e := newEcho()
		out, user := call(e, far, nil, near)
		e.reset()
		alike, lag := likeness(out, near)
		change := dB(powerOf(out[lag:]) / powerOf(near[:len(out)-lag]))
		t.Logf("%s: %.2f dB louder, %.3f alike, %.1f ms late", name, change, alike, float64(lag)/48)
		// The canceller removes what is below speech, which is why it
		// is not exactly alike.
		if math.Abs(change) > 1 || alike < 0.95 {
			t.Errorf("%s: the user comes out %.2f dB louder and %.3f alike", name, change, alike)
		}
		if lag > frameSamples/channels {
			t.Errorf("%s: the user comes out %d samples late, more than a frame", name, lag)
		}
		for i, u := range user {
			if !u {
				t.Fatalf("%s: frame %d not taken for the user", name, i)
			}
		}
	}
}

// A microphone whose channels differ keeps them apart.
func TestEchoStereoMicrophone(t *testing.T) {
	left, right := madeUpVoice(2, 4), madeUpVoice(3, 4)
	e := newEcho()
	defer e.reset()
	var outL, outR []int16
	pcm := make([]int16, frameSamples)
	for at := 0; at+960 <= len(left); at += 960 {
		for i := range 960 {
			pcm[2*i], pcm[2*i+1] = left[at+i], right[at+i]
		}
		e.capture(pcm)
		for i := range 960 {
			outL, outR = append(outL, pcm[2*i]), append(outR, pcm[2*i+1])
		}
	}
	l, _ := likeness(outL[sampleRate:], left[sampleRate:])
	r, _ := likeness(outR[sampleRate:], right[sampleRate:])
	if l < 0.97 || r < 0.97 {
		t.Fatalf("the channels come out %.3f and %.3f alike to what went in", l, r)
	}
}

// Switched off, what is captured is left as it is, even with echo in it.
func TestEchoOff(t *testing.T) {
	far := madeUpVoice(1, 3)
	echo := room{delay: 3840, gain: 0.3}.echoOf(far)
	e := newEcho()
	e.on.Store(false)
	out, user := call(e, far, echo, nil)
	for i, v := range out {
		if want := int16(echo[i]); v != want {
			t.Fatalf("sample %d is %d, want %d", i, v, want)
		}
	}
	for i, u := range user {
		if !u {
			t.Fatalf("frame %d not taken for the user", i)
		}
	}
	if e.aec != nil {
		t.Fatal("the canceller is kept while off")
	}
}

// gateOver runs a gate alone, with no canceller before it, over a call,
// and returns whether it took each frame for the user.
func gateOver(far []int16, echo []float64, near []int16) []bool {
	var g echoGate
	var user []bool
	mic := make([]int16, 960)
	for at := 0; at+960 <= max(len(far), len(near)); at += 960 {
		if far != nil {
			g.played(meanSquare(far[at : at+480]))
			g.played(meanSquare(far[at+480 : at+960]))
		}
		for i := range mic {
			var v float64
			if echo != nil {
				v = echo[at+i]
			}
			if near != nil {
				v += float64(near[at+i])
			}
			mic[i] = int16(math.Max(-32768, math.Min(32767, v)))
		}
		user = append(user, g.open(meanSquare(mic), meanSquare(mic)))
	}
	return user
}

// The gate alone, as before the canceller has learned the room: it stays
// closed on the echo of what is played, opens for a user louder than the
// echo, and changes nothing when nothing is played.
func TestEchoGate(t *testing.T) {
	far, near := madeUpVoice(1, 30), madeUpVoice(2, 30)
	echo := room{delay: 3840, gain: 0.05}.echoOf(far)

	// What is played alone.
	heard, loud := count(echo, sent(echo, gateOver(far, echo, nil)), 55)
	t.Logf("played alone: %d of %d frames of echo sent", heard, loud)
	if heard > 0 {
		t.Errorf("played alone: %d of %d frames of echo sent", heard, loud)
	}

	// The user louder than the echo, over it.
	clear(near[:5*sampleRate])
	mic := make([]float64, len(near))
	for i := range mic {
		mic[i] = echo[i] + float64(near[i])
	}
	user := gateOver(far, echo, near)
	opened, spoken := count(near, user, 200)
	heard, _ = count(near, sent(mic, user), 200)
	t.Logf("the user over what is played: of %d frames spoken, %d taken for the user and %d sent; echo was %.1f dB below the user",
		spoken, opened, heard, dB(powerOf(near[5*sampleRate:])/powerOf(echo[5*sampleRate:])))
	if heard < spoken*9/10 {
		t.Errorf("the user over what is played: %d of %d frames spoken sent", heard, spoken)
	}

	// The user alone.
	for i, u := range gateOver(nil, nil, near) {
		if !u {
			t.Fatalf("the user alone: frame %d not taken for the user", i)
		}
	}
}

// An echo that becomes much louder than learned, as when the speakers are
// turned up, is learned again in a few seconds rather than taken for the
// user from then on.
func TestEchoGateLearnsLouderEcho(t *testing.T) {
	far := madeUpVoice(1, 40)
	echo := room{delay: 3840, gain: 0.05}.echoOf(far)
	for i := 10 * sampleRate; i < len(echo); i++ {
		echo[i] *= 4
	}
	heard := sent(echo, gateOver(far, echo, nil))
	before, _ := count(echo[:10*sampleRate], heard[:10*50], 55)
	during, _ := count(echo[10*sampleRate:25*sampleRate], heard[10*50:25*50], 55)
	after, _ := count(echo[25*sampleRate:], heard[25*50:], 55)
	t.Logf("frames of echo sent: %d before it became louder, %d in the 15 seconds after, %d later", before, during, after)
	if before > 0 || after > 0 {
		t.Fatalf("%d frames of echo sent before it became louder and %d once learned again", before, after)
	}
}

// One iteration is a packet: 20 ms played and 20 ms captured, with echo
// and the user in it.
func BenchmarkEcho(b *testing.B) {
	far, near := madeUpVoice(1, 20), madeUpVoice(2, 20)
	echo := room{delay: 3840, gain: 0.3}.echoOf(far)
	e := newEcho()
	defer e.reset()
	output := make([]byte, frameBytes)
	pcm := make([]int16, frameSamples)
	at := 0
	b.ResetTimer()
	for range b.N {
		if at+960 > len(far) {
			at = 0
		}
		for i := range 960 {
			binary.LittleEndian.PutUint16(output[4*i:], uint16(far[at+i]))
			binary.LittleEndian.PutUint16(output[4*i+2:], uint16(far[at+i]))
			s := int16(echo[at+i]) + near[at+i]/2
			pcm[2*i], pcm[2*i+1] = s, s
		}
		e.played(output)
		e.capture(pcm)
		at += 960
	}
}

// What another device plays, as a stream watched, is added to what the
// canceller is told is played, once.
func TestEchoTap(t *testing.T) {
	tap := newTap()
	defer tap.close()
	output := make([]byte, 4*480)
	for i := range 480 {
		binary.LittleEndian.PutUint16(output[4*i:], uint16(int16(100+i)))
		binary.LittleEndian.PutUint16(output[4*i+2:], uint16(int16(100+i)))
	}
	tap.played(output)
	mono := make([]int16, 480)
	mono[0] = 5
	addTaps(mono)
	if mono[0] != 105 || mono[479] != 579 {
		t.Fatalf("got %d and %d, want 105 and 579", mono[0], mono[479])
	}
	clear(mono)
	addTaps(mono)
	if mono[0] != 0 {
		t.Fatalf("what the tap played was added twice")
	}
}
