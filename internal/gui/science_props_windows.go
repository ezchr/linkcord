//go:build windows

package gui

import (
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	sciUser32                = windows.NewLazySystemDLL("user32.dll")
	procSystemParametersInfo = sciUser32.NewProc("SystemParametersInfoW")
)

const (
	spiGetHighContrast        = 0x0042
	spiGetClientAreaAnimation = 0x1042
	hcfHighContrastOn         = 0x1
)

// highContrast is Windows' HIGHCONTRASTW.
type highContrast struct {
	size   uint32
	flags  uint32
	scheme *uint16
}

// systemAccessibilityFeatures reads the states a browser on Windows turns
// into the media queries the web client watches. Each read that fails, as a
// setting Windows 7 does not have, leaves its bit as a browser there would.
func systemAccessibilityFeatures() int {
	f := 0
	// prefers-color-scheme follows the apps theme; Windows 7 has none, and
	// a browser there reports light.
	light := true
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetIntegerValue("AppsUseLightTheme"); err == nil {
			light = v != 0
		}
		k.Close()
	}
	if light {
		f |= a11yPrefersLight
	} else {
		f |= a11yPrefersDark
	}
	if procSystemParametersInfo.Find() == nil {
		hc := highContrast{size: uint32(unsafe.Sizeof(highContrast{}))}
		if r, _, _ := procSystemParametersInfo.Call(spiGetHighContrast, uintptr(hc.size), uintptr(unsafe.Pointer(&hc)), 0); r != 0 && hc.flags&hcfHighContrastOn != 0 {
			// A high contrast theme is forced-colors: active, and the
			// browser also reports prefers-contrast: more.
			f |= a11yForcedColors | a11yHighContrast
		}
		var anim int32 = 1
		if r, _, _ := procSystemParametersInfo.Call(spiGetClientAreaAnimation, 0, uintptr(unsafe.Pointer(&anim)), 0); r != 0 && anim == 0 {
			f |= a11yReducedMotion
		}
	}
	// inverted-colors is not reported by browsers on Windows.
	return f
}

// cpuSampler gives the process's CPU use since the last sample, as a share
// of all cores, from GetProcessTimes.
type cpuSampler struct {
	last     time.Time
	lastBusy time.Duration
}

func filetimeDuration(ft windows.Filetime) time.Duration {
	return time.Duration(uint64(ft.HighDateTime)<<32|uint64(ft.LowDateTime)) * 100
}

func (c *cpuSampler) sample() (float64, bool) {
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exited, &kernel, &user); err != nil {
		return 0, false
	}
	now := time.Now()
	busy := filetimeDuration(kernel) + filetimeDuration(user)
	if c.last.IsZero() {
		// The first sample covers the process's whole life.
		c.last = time.Unix(0, created.Nanoseconds())
	}
	wall := now.Sub(c.last)
	used := busy - c.lastBusy
	c.last, c.lastBusy = now, busy
	if wall <= 0 {
		return 0, false
	}
	return float64(used) / float64(wall) / float64(runtime.NumCPU()) * 100, true
}
