package gui

import (
	"errors"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/utils/httputil"
)

// The waits stay inside their ranges, lean to the lower half, and are not
// all the same.
func TestPaceRangePick(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2)).Float64
	for _, r := range []paceRange{paceDelete, paceReact, paceForward, paceDest, paceBreak, paceRetryExtra} {
		seen := make(map[time.Duration]bool)
		low := 0
		var sum time.Duration
		const n = 2000
		for range n {
			d := r.pick(rnd)
			if d < r.min || d > r.max {
				t.Fatalf("%v outside %v–%v", d, r.min, r.max)
			}
			if d < r.min+(r.max-r.min)/2 {
				low++
			}
			seen[d] = true
			sum += d
		}
		if len(seen) < n/2 {
			t.Errorf("%v–%v: only %d different waits of %d", r.min, r.max, len(seen), n)
		}
		if low < n*6/10 || low > n*95/100 {
			t.Errorf("%v–%v: %d of %d in the lower half", r.min, r.max, low, n)
		}
		if mean := sum / n; mean < r.mean()*9/10 || mean > r.mean()*11/10 {
			t.Errorf("%v–%v: mean %v, said %v", r.min, r.max, mean, r.mean())
		}
	}
	// The ends of the range, for the least and the most the numbers give.
	if d := paceDelete.pick(func() float64 { return 0 }); d != paceDelete.min {
		t.Errorf("least %v, want %v", d, paceDelete.min)
	}
	if d := paceDelete.pick(func() float64 { return 1 }); d != paceDelete.max {
		t.Errorf("most %v, want %v", d, paceDelete.max)
	}
}

// A run waits its range between messages, with a longer pause every 8 to
// 15 of them.
func TestPacerBreaks(t *testing.T) {
	p := newPacer(paceReact, rand.New(rand.NewPCG(3, 4)).Float64)
	since, breaks := 0, 0
	for range 500 {
		d := p.next()
		since++
		switch {
		case d >= paceBreak.min && d <= paceBreak.max && since >= paceBreakMin:
			if since > paceBreakMax {
				t.Fatalf("a pause after %d messages", since)
			}
			since = 0
			breaks++
		case d < paceReact.min || d > paceReact.max:
			t.Fatalf("wait %v outside %v–%v, %d after a pause", d, paceReact.min, paceReact.max, since)
		}
		if since > paceBreakMax {
			t.Fatalf("no pause in %d messages", since)
		}
	}
	if breaks < 500/paceBreakMax-1 {
		t.Errorf("%d pauses in 500 messages", breaks)
	}
	if d := p.nextDest(); d < paceDest.min || d > paceDest.max {
		t.Errorf("wait between places %v", d)
	}
}

// A 429 is waited out with something added and slows the rest; the second
// stops the run.
func TestPacerRateLimited(t *testing.T) {
	p := newPacer(paceDelete, rand.New(rand.NewPCG(5, 6)).Float64)
	wait, stop := p.rateLimited(4 * time.Second)
	if stop {
		t.Fatal("stopped at the first 429")
	}
	if wait < 4*time.Second+paceRetryExtra.min || wait > 4*time.Second+paceRetryExtra.max {
		t.Errorf("waited %v after a 429 that asked for 4s", wait)
	}
	if p.slow < paceSlowMin || p.slow > paceSlowMax {
		t.Errorf("slowed by %v", p.slow)
	}
	for range 5 {
		d := p.next()
		if d < time.Duration(float64(paceDelete.min)*paceSlowMin) {
			t.Errorf("wait %v after a 429, faster than slowed", d)
		}
	}
	if _, stop := p.rateLimited(time.Second); !stop {
		t.Error("went on after the second 429")
	}
}

func TestRetryAfter(t *testing.T) {
	d, ok := retryAfter(&httputil.HTTPError{Status: 429, Body: []byte(`{"message":"You are being rate limited.","retry_after":2.5,"global":false}`)})
	if !ok || d != 2500*time.Millisecond {
		t.Errorf("got %v, %v", d, ok)
	}
	if _, ok := retryAfter(&httputil.HTTPError{Status: 403}); ok {
		t.Error("a 403 taken for a 429")
	}
	if _, ok := retryAfter(errors.New("no network")); ok {
		t.Error("an error taken for a 429")
	}
	if _, ok := retryAfter(nil); ok {
		t.Error("no error taken for a 429")
	}
}

func TestBulkEstimate(t *testing.T) {
	if d := bulkEstimate(paceDelete, 1, 0); d != 0 {
		t.Errorf("one message takes %v", d)
	}
	// 50 deletes: 49 waits of about 2.3 s, with some longer pauses.
	d := bulkEstimate(paceDelete, 50, 0)
	if d < 100*time.Second || d > 4*time.Minute {
		t.Errorf("50 deletes take %v", d)
	}
	if more := bulkEstimate(paceForward, 10, 2); more <= bulkEstimate(paceForward, 10, 0) {
		t.Error("more places take no longer")
	}
	for d, want := range map[time.Duration]string{
		3 * time.Second:   "a few seconds",
		38 * time.Second:  "about 40 seconds",
		70 * time.Second:  "about a minute",
		130 * time.Second: "about 2 minutes",
	} {
		if got := aboutDuration(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

// fakeRun runs steps without waiting: each step's outcome comes from errs.
func fakeRun(t *testing.T, j *bulkJob, errs map[int]error, n int, onStep func(i int)) (ran []int, failed []int, stopped string) {
	t.Helper()
	j.cancel = make(chan struct{})
	steps := make([]bulkStep, n)
	calls := make(map[int]int)
	for i := range steps {
		steps[i] = bulkStep{item: i, run: func() error {
			calls[i]++
			if calls[i] > 1 {
				// Sent again after a 429, it goes through.
				return nil
			}
			return errs[i]
		}}
	}
	p := newPacer(paceRange{}, func() float64 { return 0 })
	stopped = runSteps(j, p, 0, steps,
		func(i int) {
			ran = append(ran, i)
			if onStep != nil {
				onStep(i)
			}
		},
		func(i int, err error) {
			if err != nil {
				failed = append(failed, i)
			}
		})
	return ran, failed, stopped
}

// A run goes through every message in order, and a failure does not stop
// the others.
func TestRunStepsProgress(t *testing.T) {
	old := paceRetryExtra
	paceRetryExtra = paceRange{}
	defer func() { paceRetryExtra = old }()

	ran, failed, stopped := fakeRun(t, &bulkJob{}, map[int]error{2: errors.New("no permission")}, 5, nil)
	if len(ran) != 5 || len(failed) != 1 || failed[0] != 2 || stopped != "" {
		t.Errorf("ran %v, failed %v, stopped %q", ran, failed, stopped)
	}
	for i, n := range ran {
		if n != i {
			t.Fatalf("order %v", ran)
		}
	}
}

// Cancelling ends the run after the request on its way; what was done
// stays done.
func TestRunStepsCancel(t *testing.T) {
	j := &bulkJob{}
	ran, _, stopped := fakeRun(t, j, nil, 12, func(i int) {
		if i == 2 {
			j.stop()
		}
	})
	if len(ran) != 3 || stopped != "cancelled" {
		t.Errorf("ran %v, stopped %q", ran, stopped)
	}
	// Stopping twice does nothing more.
	j.stop()
}

// One 429 is waited out and the message sent again; the second ends the
// run.
func TestRunStepsRateLimit(t *testing.T) {
	old := paceRetryExtra
	paceRetryExtra = paceRange{}
	defer func() { paceRetryExtra = old }()

	limited := &httputil.HTTPError{Status: 429, Body: []byte(`{"retry_after":0}`)}
	ran, failed, stopped := fakeRun(t, &bulkJob{}, map[int]error{1: limited}, 4, nil)
	if len(ran) != 4 || len(failed) != 0 || stopped != "" {
		t.Errorf("one 429: ran %v, failed %v, stopped %q", ran, failed, stopped)
	}
	ran, failed, stopped = fakeRun(t, &bulkJob{}, map[int]error{1: limited, 3: limited}, 6, nil)
	if len(ran) != 4 || stopped != "rate limited" || len(failed) != 1 || failed[0] != 3 {
		t.Errorf("two 429s: ran %v, failed %v, stopped %q", ran, failed, stopped)
	}
}

// Steps left out, as those of a place that refused a message, cost no wait
// and are not run.
func TestRunStepsSkip(t *testing.T) {
	j := &bulkJob{cancel: make(chan struct{})}
	var ran []int
	steps := []bulkStep{
		{item: 0, run: func() error { return nil }},
		{item: 0, skip: func() bool { return true }, run: func() error { t.Error("ran a step left out"); return nil }},
		{item: 1, run: func() error { return nil }},
	}
	runSteps(j, newPacer(paceRange{}, func() float64 { return 0 }), 0, steps, func(i int) { ran = append(ran, i) }, func(int, error) {})
	if len(ran) != 2 || ran[0] != 0 || ran[1] != 2 {
		t.Errorf("ran %v", ran)
	}
}

func TestBulkJobProgress(t *testing.T) {
	j := &bulkJob{verb: "Deleting", at: 3, total: 12}
	if got := j.progress(); got != "Deleting 3 of 12…" {
		t.Errorf("got %q", got)
	}
}
