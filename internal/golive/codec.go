package golive

import (
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// The H264 encoder is Cisco's OpenH264, downloaded from Cisco the first time
// a stream starts, as browsers do: Cisco then pays the H264 licence for it.
// The DLL needs nothing but kernel32, so it runs on Windows 7.
const (
	codecURL    = "http://ciscobinary.openh264.org/openh264-2.6.0-win64.dll.bz2"
	codecFile   = "openh264-2.6.0-win64.dll"
	codecSHA256 = "2076cb5675ec6c1a4c70e7a2a322552f547b6eeed649d6dfcd9e02a543b24691"
	// codecMax is more than the DLL weighs, against a server sending
	// endlessly.
	codecMax = 8 << 20
)

// CodecNotice is what Cisco's licence asks to be shown where the codec is
// used.
const CodecNotice = "OpenH264 Video Codec provided by Cisco Systems, Inc."

// codecPath returns where OpenH264 is kept in dir, downloading it there
// first if it is not.
func codecPath(ctx context.Context, dir string) (string, error) {
	path := filepath.Join(dir, codecFile)
	if b, err := os.ReadFile(path); err == nil && sum(b) == codecSHA256 {
		return path, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codecURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("golive: could not download OpenH264: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("golive: could not download OpenH264: %s", resp.Status)
	}
	dll, err := io.ReadAll(io.LimitReader(bzip2.NewReader(io.LimitReader(resp.Body, codecMax)), codecMax))
	if err != nil {
		return "", fmt.Errorf("golive: could not unpack OpenH264: %w", err)
	}
	if sum(dll) != codecSHA256 {
		return "", fmt.Errorf("golive: the OpenH264 downloaded is not the one expected")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, codecFile+".*")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(tmp, bytes.NewReader(dll))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("golive: could not save OpenH264: %w", err)
	}
	return path, nil
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
