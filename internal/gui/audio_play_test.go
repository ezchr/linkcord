package gui

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAudioAttachmentPlays plays a WAV served as Discord serves attachments,
// as the player of the chat does: audio files were left out of the formats
// the decoder could read.
func TestAudioAttachmentPlays(t *testing.T) {
	ffmpeg, err := mediaExecutable("ffmpeg")
	if err != nil {
		t.Skip(err)
	}
	pcm := testSound()
	wav := wavFile(pcm)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/x-wav")
		http.ServeContent(w, r, "clip.wav", time.Now(), bytes.NewReader(wav))
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stream, err := streamVideo(ctx, http.DefaultClient, srv.URL+"/clip.wav")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	audio := &videoAudio{queue: make(chan []byte, 1024)}
	n := 0
	done := make(chan struct{})
	go func() {
		for c := range audio.queue {
			n += len(c)
		}
		close(done)
	}()
	if err := decodeVideoAudio(ctx, ffmpeg, stream.url, 0, audio); err != nil {
		t.Fatal(err)
	}
	<-done
	if want := len(pcm) * 2; n < want-4096 || n > want+4096 {
		t.Fatalf("decoded %d bytes, want about %d", n, want)
	}
}
