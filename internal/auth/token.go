package auth

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
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

// backupPath is where a copy of the token is kept, encrypted for the
// Windows user (DPAPI), for when the Credential Manager cannot be read, as
// right after Windows starts; "" keeps none. SetBackupDir sets it.
var backupPath string

// SetBackupDir keeps the copy of the token in dir.
func SetBackupDir(dir string) { backupPath = filepath.Join(dir, "session.bin") }

// LoadToken returns the saved token, or "" if there is none.
func LoadToken() (string, error) {
	token, _, err := LoadTokenFrom()
	return token, err
}

// LoadTokenFrom returns the saved token and where it was read from:
// "credentials", "backup" or "" for none.
func LoadTokenFrom() (string, string, error) {
	token, err := loadKeyring()
	if err == nil && token != "" {
		return token, "credentials", nil
	}
	// The Credential Manager failed or had nothing: the copy stands in, and
	// is put back there when it had nothing.
	if b, berr := readBackup(); berr == nil {
		if err == nil {
			if serr := keyring.Set(keyringService, keyringUser, b); serr != nil {
				slog.Warn("could not put the token back in the credentials", "err", serr)
			}
		}
		return b, "backup", nil
	}
	return token, "", err
}

func loadKeyring() (string, error) {
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

// SaveToken remembers token for the next start, in the Credential Manager
// and in the encrypted copy. It fails only if neither could keep it.
func SaveToken(token string) error {
	err := keyring.Set(keyringService, keyringUser, token)
	berr := writeBackup(token)
	if err != nil && berr != nil {
		return err
	}
	if err != nil {
		slog.Warn("could not save the token in the credentials; kept in the copy", "err", err)
	}
	return nil
}

// DeleteToken forgets the saved token, and its copy.
func DeleteToken() error {
	if backupPath != "" {
		if err := os.Remove(backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	err := keyring.Delete(keyringService, keyringUser)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

func writeBackup(token string) error {
	if backupPath == "" {
		return errors.New("no copy kept")
	}
	b, err := protect([]byte(token))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		return err
	}
	tmp := backupPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, backupPath)
}

func readBackup() (string, error) {
	if backupPath == "" {
		return "", errors.New("no copy kept")
	}
	b, err := os.ReadFile(backupPath)
	if err != nil {
		return "", err
	}
	plain, err := unprotect(b)
	if err != nil {
		return "", err
	}
	if len(plain) == 0 {
		return "", errors.New("empty copy")
	}
	return string(plain), nil
}

// CleanToken trims what people often paste around a token copied from the
// browser, such as quotes and spaces.
func CleanToken(token string) string {
	return strings.Trim(strings.TrimSpace(token), `"'`)
}

// KeepBackup writes the encrypted copy of token if there is none yet, as for
// a token saved before copies were kept.
func KeepBackup(token string) {
	if backupPath == "" || token == "" {
		return
	}
	if _, err := os.Stat(backupPath); err == nil {
		return
	}
	if err := writeBackup(token); err != nil {
		slog.Warn("could not keep a copy of the token", "err", err)
	}
}
