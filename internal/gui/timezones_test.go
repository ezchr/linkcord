package gui

import (
	"testing"
	"time"
)

func TestTimeZonesLoad(t *testing.T) {
	for _, z := range timeZones {
		if _, err := time.LoadLocation(z.zone); err != nil {
			t.Errorf("%s: %v", z.zone, err)
		}
	}
}
