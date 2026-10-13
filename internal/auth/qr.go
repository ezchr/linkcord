// Package auth logs into Discord without depending on a user interface, by
// QR code, by email and password, or by token.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/discordo/internal/gateway"
	"github.com/ayn2op/discordo/internal/http"
	"github.com/gorilla/websocket"
)

const remoteAuthGatewayURL = "wss://remote-auth-gateway.discord.gg/?v=2"

// ErrQRCanceled is returned by QR when the login is canceled on the phone.
var ErrQRCanceled = errors.New("el inicio de sesión se canceló en el teléfono")

// QRHandlers receive the progress of a QR login. They are called from the
// goroutine running QR.
type QRHandlers struct {
	// Code is called with the URL to show as a QR code, which the Discord
	// mobile app scans.
	Code func(url string)
	// Scanned is called with the name of the user who scanned the code,
	// before they confirm the login on their phone.
	Scanned func(username string)
}

// QR logs in through the Remote Auth Gateway and returns the token once the
// user scans the code and confirms on their phone. It blocks until then, an
// error, or ctx is done.
func QR(ctx context.Context, h QRHandlers) (string, error) {
	headers := http.Headers()
	headers.Set("User-Agent", http.BrowserUserAgent())
	dialer := gateway.NewDialer()
	ws, _, err := dialer.DialContext(ctx, remoteAuthGatewayURL, headers)
	if err != nil {
		return "", err
	}
	// Closing the connection unblocks the read below when ctx is done.
	var closeOnce sync.Once
	closeConn := func() { closeOnce.Do(func() { ws.Close() }) }
	defer closeConn()
	stop := context.AfterFunc(ctx, closeConn)
	defer stop()

	var writeMu sync.Mutex
	write := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return ws.WriteJSON(v)
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", err
	}
	// The client that exchanges the ticket loads the login page's cookies
	// while the user scans the code.
	clientc := make(chan *api.Client, 1)
	go func() { clientc <- newLoginClient() }()
	done := make(chan struct{})
	defer close(done)

	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", err
		}

		var payload struct {
			Op                   string `json:"op"`
			HeartbeatInterval    int    `json:"heartbeat_interval"`
			EncryptedNonce       string `json:"encrypted_nonce"`
			Fingerprint          string `json:"fingerprint"`
			EncryptedUserPayload string `json:"encrypted_user_payload"`
			Ticket               string `json:"ticket"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return "", err
		}

		switch payload.Op {
		case "hello":
			go heartbeat(ws, &writeMu, time.Duration(payload.HeartbeatInterval)*time.Millisecond, done)
			spki, err := x509.MarshalPKIXPublicKey(privateKey.Public())
			if err != nil {
				return "", err
			}
			if err := write(struct {
				Op               string `json:"op"`
				EncodedPublicKey string `json:"encoded_public_key"`
			}{"init", base64.StdEncoding.EncodeToString(spki)}); err != nil {
				return "", err
			}

		case "nonce_proof":
			nonce, err := decrypt(privateKey, payload.EncryptedNonce)
			if err != nil {
				return "", err
			}
			if err := write(struct {
				Op    string `json:"op"`
				Nonce string `json:"nonce"`
			}{"nonce_proof", base64.RawURLEncoding.EncodeToString(nonce)}); err != nil {
				return "", err
			}

		case "pending_remote_init":
			if h.Code != nil {
				h.Code("https://discord.com/ra/" + payload.Fingerprint)
			}

		case "pending_ticket":
			user, err := decrypt(privateKey, payload.EncryptedUserPayload)
			if err != nil {
				return "", err
			}
			// The payload is "id:discriminator:avatar:username".
			parts := strings.Split(string(user), ":")
			if len(parts) != 4 {
				return "", errors.New("datos de usuario inválidos")
			}
			if h.Scanned != nil {
				h.Scanned(parts[3])
			}

		case "pending_login":
			var client *api.Client
			select {
			case client = <-clientc:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			token, err := exchangeTicket(client, privateKey, payload.Ticket)
			return token, friendly(err)

		case "cancel":
			return "", ErrQRCanceled
		}
	}
}

func heartbeat(ws *websocket.Conn, mu *sync.Mutex, interval time.Duration, done <-chan struct{}) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			mu.Lock()
			err := ws.WriteJSON(struct {
				Op string `json:"op"`
			}{"heartbeat"})
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func decrypt(privateKey *rsa.PrivateKey, encrypted string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, err
	}
	return rsa.DecryptOAEP(sha256.New(), nil, privateKey, decoded, nil)
}

func exchangeTicket(client *api.Client, privateKey *rsa.PrivateKey, ticket string) (string, error) {
	encryptedToken, err := client.ExchangeRemoteAuthTicket(ticket)
	if err != nil {
		return "", err
	}
	token, err := decrypt(privateKey, encryptedToken)
	if err != nil {
		return "", err
	}
	return string(token), nil
}
