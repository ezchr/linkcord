package http

import (
	"uuid"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ezchr/linkcord/internal/tls"
)

//go:generate go run generator.go

const (
	osName    = "Windows"
	osVersion = "10"
	locale    = discord.EnglishUS
)

func BrowserUserAgent() string {
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) " + tls.BrowserName() + "/" + tls.BrowserVersion() + " Safari/537.36"
}

func browserBrands() string {
	version := tls.BrowserMajorVersion()
	return `"Chromium";v="` + version + `", "Not-A.Brand";v="24", "Google ` + tls.BrowserName() + `";v="` + version + `"`
}

// launchID is this run's client_launch_id.
var launchID = uuid.NewV4().String()

func commonProperties() gateway.IdentifyProperties {
	return gateway.IdentifyProperties{
		gateway.IdentifyDevice: "",
		"device_model_name":    "",

		gateway.IdentifyOS: osName,
		"os_version":       osVersion,

		gateway.IdentifyBrowser: tls.BrowserName(),
		"browser_version":       tls.BrowserVersion(),
		"browser_user_agent":    BrowserUserAgent(),

		"client_build_number": CurrentClientBuildNumber(),
		"client_event_source": nil,
		// One per run, in the gateway and every request alike, as the web
		// app has one per page load.
		"client_launch_id": launchID,

		"system_locale":   locale,
		"release_channel": "stable",
		"has_client_mods": false,

		"referrer":                 "",
		"referrer_current":         "",
		"referring_domain":         "",
		"referring_domain_current": "",
	}
}

func IdentifyProperties() gateway.IdentifyProperties {
	props := commonProperties()
	props["is_fast_connect"] = true
	// The web client adds the installation id to IDENTIFY when it knows it.
	if id := InstallationID(); id != "" {
		props["installation_id"] = id
	}
	return props
}

// ReidentifyProperties are those of an IDENTIFY after the first, as the web
// client's handleIdentify sends them (its first comes from its fast connect,
// IdentifyProperties): the full super properties, the app state, no fast
// connect, and why it connects, "NO_REASONS" once the cold start is past.
func ReidentifyProperties() gateway.IdentifyProperties {
	props := xSuperProperties()
	props["client_app_state"] = "focused"
	props["is_fast_connect"] = false
	props["gateway_connect_reasons"] = "NO_REASONS"
	if id := InstallationID(); id != "" {
		props["installation_id"] = id
	}
	return props
}

func xSuperProperties() gateway.IdentifyProperties {
	props := commonProperties()
	props["client_app_state"] = "focused"
	props["client_heartbeat_session_id"] = HeartbeatSessionID()
	props["launch_signature"] = launchSignature
	return props
}

// launchSignature is this launch's launch_signature. Discord's web client
// makes it once per load with libdiscore's generateLaunchSignature, which
// (disassembled, client study doc 06) is a random UUID and nothing else:
// it is held for the launch and sent in the super properties of every
// request, in every analytics event and in the hourly report.
var launchSignature = uuid.NewV4().String()

// LaunchSignature is this launch's launch_signature.
func LaunchSignature() string { return launchSignature }

// Locale is the UI locale the client reports, as in X-Discord-Locale.
func Locale() string { return string(locale) }
