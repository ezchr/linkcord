package gui

import "time"

// idleAfter is how long without input before the user shows as idle, as in
// Discord's apps.
const idleAfter = 10 * time.Minute

// watchIdle shows the user as idle while they are away from the computer,
// and online again once they're back. Only an online status changes: one
// the user chose (idle, Do Not Disturb, invisible) stays.
func (c *chatView) watchIdle() func() {
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			idle := systemIdle()
			away := idle >= idleAfter
			since := time.Now().Add(-idle)
			c.u.post(func() {
				if c.closed || away == c.idle {
					return
				}
				c.idle, c.idleSince = away, since
				c.pushPresence()
			})
		}
	}()
	return func() { close(stop) }
}
