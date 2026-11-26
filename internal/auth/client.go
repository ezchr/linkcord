package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
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

	resp, err := http.FetchExperiments(client)
	if err != nil {
		slog.Warn("failed to load experiments before login", "err", err)
	} else if resp.Fingerprint != "" {
		headers.Set("X-Fingerprint", resp.Fingerprint)
	}
	// The installation id it may have given goes with every request after.
	if id := http.InstallationID(); id != "" {
		headers.Set("X-Installation-ID", id)
	}
	return client
}

// friendly turns the errors of the login endpoints into messages for people,
// as the login page of Discord words them.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "captcha") {
		return ErrCaptcha
	}
	var he *httputil.HTTPError
	if !errors.As(err, &he) {
		return err
	}
	// The field errors say what was wrong with the email or the password.
	fields := string(he.Errors) + " " + string(he.Body)
	switch {
	case he.Status == 429:
		var body struct {
			RetryAfter float64 `json:"retry_after"`
		}
		if json.Unmarshal(he.Body, &body) == nil && body.RetryAfter > 0 {
			return fmt.Errorf("you are being rate limited; try again in %d seconds", int(math.Ceil(body.RetryAfter)))
		}
		return errors.New("too many attempts; wait a few minutes and try again")
	case strings.Contains(fields, "suspended_user_token"):
		return errors.New("this account has been suspended by Discord")
	case he.Code == 20011 || strings.Contains(fields, "ACCOUNT_SCHEDULED_FOR_DELETION"):
		return errors.New("this account is scheduled for deletion; log in on discord.com to cancel it")
	case he.Code == 20013 || strings.Contains(fields, "ACCOUNT_DISABLED"):
		return errors.New("this account is disabled; log in on discord.com to re-enable it")
	case he.Code == 70007 || strings.Contains(fields, "ACCOUNT_LOGIN_VERIFICATION_EMAIL") || strings.Contains(fields, "verify your login"):
		return errors.New("new login location detected: check your email to verify this login, then try again")
	case he.Code == 60008 || strings.Contains(fields, "Invalid two-factor code"):
		return errors.New("incorrect verification code")
	case strings.Contains(fields, "PASSWORD_DOES_NOT_MATCH"), strings.Contains(fields, "INVALID_PASSWORD"):
		// Accounts made with a passkey or a phone may have no password.
		return errors.New("the password is not valid for this account; if it signs in with a passkey or has no password, use a QR code")
	case strings.Contains(fields, "INVALID_LOGIN"), strings.Contains(fields, "Login or password is invalid"):
		return errors.New("login or password is invalid")
	case he.Code == 50035 && he.Message != "":
		return errors.New(he.Message)
	}
	return err
}
