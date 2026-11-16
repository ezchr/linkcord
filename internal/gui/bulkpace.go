package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	stdhttp "net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

// Doing the same thing to many messages: forwarding, reacting to or deleting
// each of those selected. Discord has no request that does it for a user's
// account, so each message gets the request of doing it by hand, one after
// the other, never two at once.
//
// The waits between them are those of a person clicking through the
// messages, not of a machine: Discord limits and flags accounts that send a
// run of requests at a steady, fast pace. They lower that risk; nothing
// here can promise how Discord judges an account. The numbers are all
// here, to be tuned.

// paceRange is a wait between a least and a most.
type paceRange struct{ min, max time.Duration }

var (
	// paceDelete is the wait between two deletes: Discord's own app asks to
	// confirm each one unless Shift is held.
	paceDelete = paceRange{1500 * time.Millisecond, 4 * time.Second}
	// paceReact is the wait between two reactions.
	paceReact = paceRange{800 * time.Millisecond, 2500 * time.Millisecond}
	// paceForward is the wait between two messages forwarded.
	paceForward = paceRange{2 * time.Second, 5 * time.Second}
	// paceDest is the wait between the places one message is forwarded to,
	// as Discord's form sends them one after another.
	paceDest = paceRange{500 * time.Millisecond, 1500 * time.Millisecond}
	// paceBreak is a longer pause, taken after every paceBreakMin to
	// paceBreakMax messages.
	paceBreak = paceRange{5 * time.Second, 12 * time.Second}
	// paceRetryExtra is added to the wait Discord asks for with a 429.
	paceRetryExtra = paceRange{1 * time.Second, 3 * time.Second}
)

const (
	paceBreakMin, paceBreakMax = 8, 15
	// After a 429 the waits of the rest of the run are this much longer.
	paceSlowMin, paceSlowMax = 1.5, 2.0
	// maxRateLimits is how many 429s stop a run.
	maxRateLimits = 2
	// bulkLimit is the most messages one run takes.
	bulkLimit = 50
	// demoPace is how much shorter the waits are in the demo, which sends
	// nothing.
	demoPace = 0.1
)

// skew is where in a range a wait falls, from 0 to 1, for two random numbers
// from 0 to 1: most often in the lower half, now and then near the top.
func skew(u, v float64) float64 {
	return 0.15*v + 0.85*math.Pow(u, 2.2)
}

// skewMean is the mean of skew.
const skewMean = 0.15/2 + 0.85/3.2

// pick is a wait of the range, skewed to its lower half.
func (r paceRange) pick(rnd func() float64) time.Duration {
	return r.min + time.Duration(skew(rnd(), rnd())*float64(r.max-r.min))
}

// mean is the wait the range gives on average.
func (r paceRange) mean() time.Duration {
	return r.min + time.Duration(skewMean*float64(r.max-r.min))
}

// pacer gives the waits of one run.
type pacer struct {
	item paceRange
	rnd  func() float64
	// slow is how much longer the waits are since a 429, and hits how many
	// 429s there were.
	slow float64
	hits int
	// sinceBreak counts the messages since the last longer pause, taken
	// after breakAt of them.
	sinceBreak, breakAt int
}

// newPacer is a pacer for messages item apart; rnd gives random numbers from
// 0 to 1, rand.Float64 if nil.
func newPacer(item paceRange, rnd func() float64) *pacer {
	if rnd == nil {
		rnd = rand.Float64
	}
	p := &pacer{item: item, rnd: rnd, slow: 1}
	p.breakAt = p.nextBreak()
	return p
}

func (p *pacer) nextBreak() int {
	return paceBreakMin + int(p.rnd()*float64(paceBreakMax-paceBreakMin+1))
}

// next is the wait before the next message: now and then a longer pause.
func (p *pacer) next() time.Duration {
	p.sinceBreak++
	if p.sinceBreak >= p.breakAt {
		p.sinceBreak, p.breakAt = 0, p.nextBreak()
		return p.slowed(paceBreak.pick(p.rnd))
	}
	return p.slowed(p.item.pick(p.rnd))
}

// nextDest is the wait before the same message goes to another place.
func (p *pacer) nextDest() time.Duration {
	return p.slowed(paceDest.pick(p.rnd))
}

func (p *pacer) slowed(d time.Duration) time.Duration {
	return time.Duration(float64(d) * p.slow)
}

// rateLimited notes a 429 that asked to wait retryAfter: it returns how long
// to wait before going on, and whether the run stops instead. What follows
// goes slower.
func (p *pacer) rateLimited(retryAfter time.Duration) (wait time.Duration, stop bool) {
	p.hits++
	if p.hits >= maxRateLimits {
		return 0, true
	}
	p.slow *= paceSlowMin + p.rnd()*(paceSlowMax-paceSlowMin)
	return max(retryAfter, 0) + paceRetryExtra.pick(p.rnd), false
}

// bulkEstimate is about how long a run of n messages item apart takes, each
// with gaps more waits between places.
func bulkEstimate(item paceRange, n, gaps int) time.Duration {
	if n <= 0 {
		return 0
	}
	waits := n - 1
	d := time.Duration(waits)*item.mean() + time.Duration(n*gaps)*paceDest.mean()
	// A longer pause in place of a wait, every dozen messages or so.
	breaks := waits * 2 / (paceBreakMin + paceBreakMax)
	return d + time.Duration(breaks)*(paceBreak.mean()-item.mean())
}

// aboutDuration says d as people do: "a few seconds", "about 40 seconds",
// "about 2 minutes".
func aboutDuration(d time.Duration) string {
	switch {
	case d < 8*time.Second:
		return tr("a few seconds")
	case d < 50*time.Second:
		return fmt.Sprintf(tr("about %d seconds"), int(d.Round(10*time.Second)/time.Second))
	case d < 90*time.Second:
		return tr("about a minute")
	}
	return fmt.Sprintf(tr("about %d minutes"), int(d.Round(time.Minute)/time.Minute))
}

// rateLimitHits counts the answers with status 429 of every request.
// Arikawa waits what Discord asks and sends the request again by itself, so
// a run only learns of them here.
var rateLimitHits atomic.Uint64

// noteRateLimit counts a response with status 429.
func noteRateLimit(_ httpdriver.Request, resp httpdriver.Response) error {
	if resp != nil && resp.GetStatus() == stdhttp.StatusTooManyRequests {
		rateLimitHits.Add(1)
	}
	return nil
}

// retryAfter is how long a 429 that reached the caller asks to wait, and
// whether err is one.
func retryAfter(err error) (time.Duration, bool) {
	var herr *httputil.HTTPError
	if !errors.As(err, &herr) || herr.Status != stdhttp.StatusTooManyRequests {
		return 0, false
	}
	var body struct {
		RetryAfter float64 `json:"retry_after"`
	}
	json.Unmarshal(herr.Body, &body)
	return time.Duration(body.RetryAfter * float64(time.Second)), true
}

// bulkStep is one request of a run.
type bulkStep struct {
	// item is the message of the step, counted from 0. Steps of the same
	// message, which send it to several places, follow each other after a
	// shorter wait.
	item int
	// skip, if set, reports whether the step is left out; it is asked on
	// the run's goroutine.
	skip func() bool
	// run sends the request and waits for its answer, on the run's
	// goroutine. It is nil in the demo.
	run func() error
	// done takes the outcome, on the window's goroutine.
	done func(err error)
}

// bulkJob is a run under way, as the window shows it.
type bulkJob struct {
	// verb is what it does, as "Deleting".
	verb string
	// at is the step being done, of total; left is about how long the rest
	// takes.
	at, total int
	left      time.Duration
	// failed are the steps that failed, and stopped why the run ended
	// before its end, if it did.
	failed   int
	stopped  string
	finished bool

	cancel     chan struct{}
	cancelOnce sync.Once
}

// stop ends the run after the request on its way.
func (j *bulkJob) stop() {
	j.cancelOnce.Do(func() { close(j.cancel) })
}

func (j *bulkJob) cancelled() bool {
	select {
	case <-j.cancel:
		return true
	default:
		return false
	}
}

// progress is what the run says while it goes: "Deleting 3 of 12…".
func (j *bulkJob) progress() string {
	return fmt.Sprintf(tr("%s %d of %d…"), tr(j.verb), min(j.at, j.total), j.total)
}

// sleep waits d, or less if the run is cancelled, which it reports.
func (j *bulkJob) sleep(d time.Duration) bool {
	if d <= 0 {
		return !j.cancelled()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-j.cancel:
		return false
	}
}

// runSteps does the steps in order with the pacer's waits between them,
// scaled by scale, and reports why it stopped early, "" if it did not.
// before and after are told of each step done, off the window's goroutine.
// It honors 429s: one that Arikawa waited out slows the rest down, one that
// reaches here is waited out and the step sent once more, and the second
// ends the run.
func runSteps(j *bulkJob, p *pacer, scale float64, steps []bulkStep, before func(i int), after func(i int, err error)) string {
	scaled := func(d time.Duration) time.Duration { return time.Duration(float64(d) * scale) }
	last := -1
	var extra time.Duration
	for i, s := range steps {
		if j.cancelled() {
			return "cancelled"
		}
		if s.skip != nil && s.skip() {
			continue
		}
		if last >= 0 {
			wait := p.next
			if s.item == last {
				wait = p.nextDest
			}
			if !j.sleep(scaled(wait()) + extra) {
				return "cancelled"
			}
			extra = 0
		}
		last = s.item
		before(i)
		hits := rateLimitHits.Load()
		var err error
		if s.run != nil {
			err = s.run()
		}
		if ra, limited := retryAfter(err); limited {
			wait, stop := p.rateLimited(ra)
			if stop {
				after(i, err)
				return "rate limited"
			}
			if !j.sleep(wait) {
				after(i, err)
				return "cancelled"
			}
			err = s.run()
			if _, again := retryAfter(err); again {
				after(i, err)
				return "rate limited"
			}
		} else if rateLimitHits.Load() != hits {
			// Arikawa already waited what Discord asked.
			wait, stop := p.rateLimited(0)
			if stop {
				after(i, err)
				return "rate limited"
			}
			extra = wait
		}
		after(i, err)
	}
	return ""
}

// startBulk runs the steps of a job on a goroutine of its own, and calls
// finish on the window's goroutine when it ends.
func (c *chatView) startBulk(j *bulkJob, item paceRange, steps []bulkStep, finish func()) {
	j.cancel = make(chan struct{})
	j.total = 0
	gaps, last := 0, -1
	for _, s := range steps {
		if s.item == last {
			gaps++
		} else {
			j.total++
		}
		last = s.item
	}
	scale := 1.0
	if c.demo {
		scale = demoPace
	}
	estimate := func(left int) time.Duration {
		per := 0
		if j.total > 0 {
			per = gaps / j.total
		}
		return time.Duration(float64(bulkEstimate(item, left, per)) * scale)
	}
	j.left = estimate(j.total)
	p := newPacer(item, nil)
	go func() {
		at, last := 0, -1
		stopped := runSteps(j, p, scale, steps,
			func(i int) {
				if steps[i].item != last {
					at++
				}
				last = steps[i].item
				n := at
				c.u.post(func() { j.at, j.left = n, estimate(j.total-n+1) })
			},
			func(i int, err error) {
				c.u.post(func() {
					if err != nil {
						j.failed++
					}
					if steps[i].done != nil {
						steps[i].done(err)
					}
				})
			})
		c.u.post(func() {
			j.stopped, j.finished = stopped, true
			finish()
		})
	}()
}
