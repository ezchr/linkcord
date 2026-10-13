package gui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestDownloadVideo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html>watch page</html>"))
		case "/large":
			w.Header().Set("Content-Length", "268435457")
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.Header().Set("Content-Type", "video/mp4")
			w.Write([]byte("test-video"))
		}
	}))
	defer server.Close()
	path, err := downloadVideo(context.Background(), server.Client(), server.URL+"/clip")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "test-video" {
		t.Fatalf("downloaded content = %q, error = %v", data, err)
	}
	for _, raw := range []string{
		server.URL + "/page", server.URL + "/large", server.URL + "/missing",
		"file:///C:/private.mp4", "https://user:password@example.org/video.mp4",
	} {
		if path, err := downloadVideo(context.Background(), server.Client(), raw); err == nil {
			os.Remove(path)
			t.Fatalf("unexpectedly accepted %s", raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if path, err := downloadVideo(ctx, server.Client(), server.URL+"/clip"); err == nil {
		os.Remove(path)
		t.Fatal("cancelled download succeeded")
	}
}
