//go:build !windows

package gui

// systemAccessibilityFeatures is not read outside Windows; no bits are set.
func systemAccessibilityFeatures() int { return 0 }

// cpuSampler gives no CPU use outside Windows.
type cpuSampler struct{}

func (c *cpuSampler) sample() (float64, bool) { return 0, false }
