package http

import "golang.org/x/sys/windows/registry"

// systemTimeZone reads the Windows time zone (TimeZoneKeyName, present since
// Vista) and maps it to its IANA name.
func systemTimeZone() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	name, _, err := k.GetStringValue("TimeZoneKeyName")
	if err != nil {
		return ""
	}
	return windowsToIANA(name)
}
