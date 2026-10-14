// Package hub talks to Linkcord's own server, at zidpvp.xyz/linkcord: who
// uses Linkcord, for clients to mark them with its logo, and the error
// reports clients send. The server's code is in server/linkcord-api.
//
// It sends the user's Discord ID and Linkcord's version, and reports as
// they are; never tokens or messages.
package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Base is where the server is.
const Base = "https://zidpvp.xyz/linkcord"

var client = &http.Client{Timeout: 20 * time.Second}

func post(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, Base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("hub: %s: %s", path, resp.Status)
	}
	return nil
}

// Hello says that the user userID uses Linkcord version.
func Hello(ctx context.Context, userID, version string) error {
	return post(ctx, "/api/hello", map[string]string{"user_id": userID, "version": version})
}

// Users returns who used Linkcord lately, by Discord ID.
func Users(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Base+"/api/users", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub: users: %s", resp.Status)
	}
	var out struct {
		Users []string `json:"users"`
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out)
	return out.Users, err
}

// Report sends an error report of kind, such as crash or freeze.
func Report(ctx context.Context, version, os, kind, text string) error {
	return post(ctx, "/api/report", map[string]string{"version": version, "os": os, "kind": kind, "text": text})
}
