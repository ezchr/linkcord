package gui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// videoStream plays a video while it downloads. The download goes to a
// temporary file through Go's HTTP client as before, and a server on the
// loopback address hands the part already written to the decoders, whose
// reads past it wait for the rest. The decoders still never see the remote
// URL.
type videoStream struct {
	url  string
	key  string // the request path
	path string
	srv  *http.Server
	stop context.CancelFunc

	// The source, for parts far ahead of the download.
	ctx    context.Context
	client *http.Client
	source string

	mu    sync.Mutex
	grew  *sync.Cond
	have  int64 // bytes written so far
	total int64 // full size, or -1 until known
	done  bool
	err   error

	handlers sync.WaitGroup
	finished chan struct{}
}

func streamVideo(ctx context.Context, client *http.Client, raw string) (*videoStream, error) {
	ctx, stop := context.WithTimeout(ctx, 3*time.Minute)
	resp, err := requestVideo(ctx, client, raw)
	if err != nil {
		stop()
		return nil, err
	}
	file, err := os.CreateTemp("", "linkcord-video-*")
	if err != nil {
		resp.Body.Close()
		stop()
		return nil, errors.New("could not create temporary video file")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		resp.Body.Close()
		file.Close()
		os.Remove(file.Name())
		stop()
		return nil, errors.New("could not start the video stream")
	}
	// The random path keeps other programs on this computer from reading it.
	secret := make([]byte, 16)
	rand.Read(secret)
	s := &videoStream{
		key:      "/" + hex.EncodeToString(secret),
		path:     file.Name(),
		stop:     stop,
		ctx:      ctx,
		client:   client,
		source:   raw,
		total:    resp.ContentLength,
		finished: make(chan struct{}),
	}
	s.url = "http://" + listener.Addr().String() + s.key
	s.grew = sync.NewCond(&s.mu)
	s.srv = &http.Server{Handler: http.HandlerFunc(s.serve), ReadHeaderTimeout: 10 * time.Second}
	go s.srv.Serve(listener)
	go s.download(ctx, resp, file)
	return s, nil
}

func (s *videoStream) download(ctx context.Context, resp *http.Response, file *os.File) {
	defer close(s.finished)
	defer resp.Body.Close()
	defer file.Close()
	buf := make([]byte, 256<<10)
	var err error
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if s.have+int64(n) > maxVideoDownload {
				err = errors.New("video exceeds the 256 MiB playback download limit")
				break
			}
			if _, err = file.Write(buf[:n]); err != nil {
				err = errors.New("could not write temporary video file")
				break
			}
			s.mu.Lock()
			s.have += int64(n)
			s.mu.Unlock()
			s.grew.Broadcast()
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			err = errors.New("video download was interrupted")
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			break
		}
	}
	s.mu.Lock()
	if err == nil && s.have == 0 {
		err = errors.New("video file is empty")
	}
	if err == nil {
		s.total = s.have
	}
	s.done, s.err = true, err
	s.mu.Unlock()
	s.grew.Broadcast()
}

// wait returns once the file is at least end bytes long, or holds all there is.
func (s *videoStream) wait(end int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.have < end && !s.done {
		s.grew.Wait()
	}
	return s.err
}

// size waits for the full size when the server did not say it up front.
func (s *videoStream) size() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.total < 0 && !s.done {
		s.grew.Wait()
	}
	return s.total, s.err
}

func (s *videoStream) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != s.key {
		http.NotFound(w, r)
		return
	}
	s.handlers.Add(1)
	defer s.handlers.Done()
	if s.fetchAhead(w, r) {
		return
	}
	total, err := s.size()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	file, err := os.Open(s.path)
	if err != nil {
		http.Error(w, "video unavailable", http.StatusInternalServerError)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, &growingReader{s: s, f: file, total: total})
}

// aheadGap is how far past the download a read must start to be fetched from
// the source rather than waited for.
const aheadGap = 1 << 20

// fetchAhead answers a read far past the download from the source. Videos
// that keep their index at the end are read there first, and would otherwise
// wait for the whole download.
func (s *videoStream) fetchAhead(w http.ResponseWriter, r *http.Request) bool {
	var start, end int64 = 0, -1
	spec, ok := strings.CutPrefix(r.Header.Get("Range"), "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return false
	}
	from, to, _ := strings.Cut(spec, "-")
	start, err := strconv.ParseInt(from, 10, 64)
	if err != nil {
		return false
	}
	if to != "" {
		if end, err = strconv.ParseInt(to, 10, 64); err != nil || end < start {
			return false
		}
	}
	s.mu.Lock()
	far := !s.done && s.total > 0 && start > s.have+aheadGap && start < s.total
	s.mu.Unlock()
	if !far {
		return false
	}
	req, err := http.NewRequestWithContext(s.ctx, http.MethodGet, s.source, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Range", "bytes="+spec)
	resp, err := s.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") == "" {
		// The source does not do ranges: wait for the download instead.
		return false
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Range", resp.Header.Get("Content-Range"))
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(http.StatusPartialContent)
	io.Copy(w, io.LimitReader(resp.Body, maxVideoDownload))
	return true
}

// Error reports why the download failed, once it has.
func (s *videoStream) Error() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *videoStream) Close() {
	s.stop()
	s.mu.Lock()
	if !s.done {
		s.done, s.err = true, context.Canceled
	}
	s.mu.Unlock()
	s.grew.Broadcast()
	s.srv.Close()
	<-s.finished
	s.handlers.Wait()
	os.Remove(s.path)
}

// growingReader reads the file being downloaded, waiting for bytes not yet
// written.
type growingReader struct {
	s     *videoStream
	f     *os.File
	off   int64
	total int64
}

func (g *growingReader) Read(p []byte) (int, error) {
	if g.off >= g.total {
		return 0, io.EOF
	}
	if err := g.s.wait(g.off + 1); err != nil {
		return 0, err
	}
	g.s.mu.Lock()
	have := g.s.have
	g.s.mu.Unlock()
	if have <= g.off {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(p)) > have-g.off {
		p = p[:have-g.off]
	}
	n, err := g.f.ReadAt(p, g.off)
	g.off += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return n, err
}

func (g *growingReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		offset += g.off
	case io.SeekEnd:
		offset += g.total
	}
	if offset < 0 {
		return 0, errors.New("negative position")
	}
	g.off = offset
	return offset, nil
}
