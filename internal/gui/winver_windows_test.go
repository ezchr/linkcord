package gui

import "testing"

func TestWindowsName(t *testing.T) {
	for _, tc := range []struct {
		major, minor, build uint32
		want                string
	}{
		{10, 0, 26100, "Windows 11 (26100)"},
		{10, 0, 22000, "Windows 11 (22000)"},
		{10, 0, 19045, "Windows 10 (19045)"},
		{6, 3, 9600, "Windows 8.1 (9600)"},
		{6, 1, 7601, "Windows 7 (7601)"},
	} {
		if got := windowsNameOf(tc.major, tc.minor, tc.build); got != tc.want {
			t.Errorf("%d.%d.%d: %q, want %q", tc.major, tc.minor, tc.build, got, tc.want)
		}
	}
}
