package gui

import "time"

// Windows can resume with exactly the same focus/minimize state. Compare wall
// time because the monotonic clock may exclude the time spent asleep.
func suspensionGap(previous, now time.Time) bool {
	return now.UnixMilli()-previous.UnixMilli() > (45 * time.Second).Milliseconds()
}

func (u *ui) startResumeWatchdog() func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		previous := time.Now()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now := time.Now()
				if suspensionGap(previous, now) {
					u.post(func() {
						if u.chat != nil {
							u.chat.refreshCurrentChat()
						}
					})
				}
				previous = now
			}
		}
	}()
	return func() { close(done) }
}
