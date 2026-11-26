package http

import (
	"github.com/ayn2op/arikawa/v3/api"
)

// Experiments is the part of GET /experiments the client uses.
type Experiments struct {
	Fingerprint  string `json:"fingerprint"`
	Installation string `json:"installation"`
}

// FetchExperiments loads the experiments as the web client does on start,
// before login with the login page's client and again once logged in with
// the account's client, and keeps the installation id it gives (set once).
// Linkcord shows nothing driven by experiments, so the assignments are not
// read and no experiment events follow.
func FetchExperiments(client *api.Client) (Experiments, error) {
	resp, err := client.RequestJSON[Experiments]("GET", api.Endpoint+"experiments?with_guild_experiments=true")
	if err != nil {
		return Experiments{}, err
	}
	SetInstallationID(resp.Installation)
	return resp, nil
}
