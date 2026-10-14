package hotkey

import "testing"

func TestCombo(t *testing.T) {
	c := Combo{'M', keyShift, keyControl}.sorted()
	if got, want := c.String(), "CTRL+SHIFT+M"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	for k, want := range map[Key]string{0x05: "MOUSE4", 0x61: "NUMPAD 1", 0x7b: "F12", '7': "7"} {
		if got := k.String(); got != want {
			t.Errorf("%#x: got %q, want %q", uint16(k), got, want)
		}
	}
}
