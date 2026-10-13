package update

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var versionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)

type semanticVersion struct {
	numbers [3]uint64
	pre     []string
}

func parseVersion(raw string) (semanticVersion, error) {
	var v semanticVersion
	if len(raw) > 256 {
		return v, errors.New("invalid version")
	}
	m := versionPattern.FindStringSubmatch(raw)
	if m == nil {
		return v, errors.New("invalid version")
	}
	for i := range 3 {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return v, errors.New("invalid version")
		}
		v.numbers[i] = n
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
		for _, part := range v.pre {
			if part == "" || numeric(part) && len(part) > 1 && part[0] == '0' {
				return v, errors.New("invalid prerelease version")
			}
		}
	}
	if m[5] != "" {
		for _, part := range strings.Split(m[5], ".") {
			if part == "" {
				return v, errors.New("invalid build metadata")
			}
		}
	}
	return v, nil
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func compareVersion(a, b semanticVersion) int {
	for i := range 3 {
		if a.numbers[i] > b.numbers[i] {
			return 1
		}
		if a.numbers[i] < b.numbers[i] {
			return -1
		}
	}
	if len(a.pre) == 0 && len(b.pre) != 0 {
		return 1
	}
	if len(b.pre) == 0 && len(a.pre) != 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		x, y := a.pre[i], b.pre[i]
		if x == y {
			continue
		}
		xn, yn := numeric(x), numeric(y)
		if xn && yn && len(x) != len(y) {
			if len(x) > len(y) {
				return 1
			}
			return -1
		}
		if xn != yn {
			if xn {
				return -1
			}
			return 1
		}
		if x > y {
			return 1
		}
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	return 0
}

// Newer compares release tags numerically, including prerelease ordering.
// Unknown versions produce an error rather than risking a downgrade.
func Newer(latest, current string) (bool, error) {
	a, err := parseVersion(latest)
	if err != nil {
		return false, err
	}
	b, err := parseVersion(current)
	if err != nil {
		return false, err
	}
	return compareVersion(a, b) > 0, nil
}
