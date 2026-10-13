package update

import "testing"

func TestVersionOrdering(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"v1.4.0", "v1.3.0", true},
		{"v1.10.0", "v1.9.0", true},
		{"v1.3.0", "v1.4.0-dev", false},
		{"v1.4.0", "v1.4.0-dev", true},
		{"v1.4.0", "v1.4.0", false},
		{"v1.4.0+build.2", "v1.4.0+build.1", false},
		{"v1.4.0-rc.10", "v1.4.0-rc.2", true},
		{"v1.4.0-alpha.1", "v1.4.0-alpha", true},
		{"v1.4.0-beta", "v1.4.0-alpha.99", true},
		{"v1.4.0-2", "v1.4.0-alpha", false},
	} {
		got, err := Newer(tc.latest, tc.current)
		if err != nil || got != tc.want {
			t.Errorf("%s versus %s: %v, %v; want %v", tc.latest, tc.current, got, err, tc.want)
		}
	}
	for _, invalid := range []string{"unknown", "v1.2", "v01.2.3", "v1.2.3-01", "v1.2.3-alpha..1", "v1.2.3+..", "v999999999999999999999999.0.0"} {
		if _, err := Newer("v2.0.0", invalid); err == nil {
			t.Errorf("invalid current version accepted: %s", invalid)
		}
	}
}
