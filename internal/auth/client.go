package auth

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ezchr/linkcord/internal/http"
)

// ErrCaptcha is returned when Discord asks to solve a captcha, which this
// client cannot show.
var ErrCaptcha = errors.New("Discord requested a captcha (the \"I'm not a robot\" check), which Linkcord cannot display")

// newLoginClient returns a client for logging in that behaves like the login
// page of a browser: it first loads the experiments, as the page does, which
// sets the cookies of Discord and gives the fingerprint that the page sends
// with every request after. Requests without them look automated, which
// makes Discord ask for a captcha more often.
func newLoginClient() *api.Client {
	headers := http.Headers()
	headers.Set("Referer", "https://discord.com/login")
	client := http.NewSessionClient("")
	client.OnRequest = append(client.OnRequest, httputil.WithHeaders(headers))

	type experiments struct {
		Fingerprint string `json:"fingerprint"`
	}
	resp, err := client.RequestJSON[experiments]("GET", api.Endpoint+"experiments?with_guild_experiments=true")
	if err != nil {
		slog.Warn("failed to load experiments before login", "err", err)
	} else if resp.Fingerprint != "" {
		headers.Set("X-Fingerprint", resp.Fingerprint)
	}
	return client
}

// friendly turns the errors of the login endpoints into messages for people.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "captcha"):
		return ErrCaptcha
	case strings.Contains(msg, "INVALID_LOGIN"), strings.Contains(msg, "Login or password is invalid"):
		return errors.New("incorrect email or password")
	case strings.Contains(msg, "Invalid two-factor code"), strings.Contains(msg, "60008"):
		return errors.New("incorrect verification code")
	case strings.Contains(msg, "status 429"):
		return errors.New("demasiados intentos; espera unos minutos y vuelve a probar")
	}
	return err
}
