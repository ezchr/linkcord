package gui

import "testing"

func TestSystemLanguage(t *testing.T) {
	l := systemLanguage()
	if l != "es" && l != "en" {
		t.Fatalf("systemLanguage() = %q", l)
	}
	t.Logf("Windows is in %q", l)
}
