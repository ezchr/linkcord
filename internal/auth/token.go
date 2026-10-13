package auth

import (
	"errors"
	"strings"

	"github.com/zalando/go-keyring"
)

// The token is kept in the Windows Credential Manager under this name. Tests
// change the service so they do not touch the real one.
var keyringService = "linkcord"

// legacyKeyringService is where versions named reDiscord kept the token.
// LoadToken moves a token found there to keyringService.
var legacyKeyringService = "rediscord"

const keyringUser = "token"

// LoadToken returns the saved token, or "" if there is none.
func LoadToken() (string, error) {
	token, err := keyring.Get(keyringService, keyringUser)
	if !errors.Is(err, keyring.ErrNotFound) {
		return token, err
	}
	token, err = keyring.Get(legacyKeyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if err := SaveToken(token); err == nil {
		_ = keyring.Delete(legacyKeyringService, keyringUser)
	}
	return token, nil
}

// SaveToken remembers token for the next start.
func SaveToken(token string) error {
	return keyring.Set(keyringService, keyringUser, token)
}

// DeleteToken forgets the saved token.
func DeleteToken() error {
	err := keyring.Delete(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// CleanToken trims what people often paste around a token copied from the
// browser, such as quotes and spaces.
func CleanToken(token string) string {
	return strings.Trim(strings.TrimSpace(token), `"'`)
}
