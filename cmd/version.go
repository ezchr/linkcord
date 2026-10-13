package cmd

import "github.com/ezchr/linkcord/internal/version"

func buildVersion() string {
	return version.String()
}
