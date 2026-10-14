package gui

import "golang.org/x/sys/windows"

var getUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// systemLanguage is the language Windows is shown in, as Linkcord names its
// languages: "es" for any Spanish, "en" otherwise.
func systemLanguage() string {
	lang, _, _ := getUserDefaultUILanguage.Call()
	// The low ten bits are the language, whatever the country.
	if lang&0x3ff == 0x0a {
		return "es"
	}
	return "en"
}
