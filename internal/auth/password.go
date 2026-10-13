package auth

import (
	"errors"
	"strings"

	"github.com/ayn2op/arikawa/v3/api"
)

// MFARequired is returned by Password when the account needs a code from an
// authenticator app to finish logging in; pass it to TOTP with the code.
type MFARequired struct {
	ticket, loginInstanceID string
	// client is the one that logged in, whose cookies the code must go with.
	client *api.Client
}

func (*MFARequired) Error() string {
	return "a two-factor authentication code is required"
}

// Password logs in with an email or phone number and a password. It returns
// a *MFARequired error when the account uses two-factor authentication.
func Password(login, password string) (string, error) {
	client := newLoginClient()
	resp, err := client.Login(login, password)
	if err != nil {
		return "", friendly(err)
	}
	if resp.Token != "" {
		return resp.Token, nil
	}
	if resp.MFA {
		if !resp.TOTP {
			return "", errors.New("this account only supports SMS verification; use a QR code or token instead")
		}
		return "", &MFARequired{ticket: resp.Ticket, loginInstanceID: resp.LoginInstanceID, client: client}
	}
	return "", errors.New("Discord did not return a token")
}

// TOTP finishes a login that needs a code from an authenticator app.
func TOTP(mfa *MFARequired, code string) (string, error) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	resp, err := mfa.client.TOTP(code, mfa.ticket, mfa.loginInstanceID)
	if err != nil {
		return "", friendly(err)
	}
	if resp.Token == "" {
		return "", errors.New("Discord did not return a token")
	}
	return resp.Token, nil
}
