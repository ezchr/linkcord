// Command linkcord-api is the small server that Linkcord talks to: it keeps
// who uses Linkcord, for clients to mark them with Linkcord's logo, and the
// error reports clients send, for the developers to read.
//
// It keeps a user's Discord ID and Linkcord version, and reports as sent;
// never addresses, tokens or messages. It listens on a local address, behind
// a web server that gives it HTTPS:
//
//	GET  /health          ok
//	POST /api/hello       {"user_id": "...", "version": "..."}: uses Linkcord
//	GET  /api/users       {"users": ["...", ...]}: who used it lately
//	POST /api/report      {"version", "os", "kind", "text"}: an error report
//	GET  /admin/reports?token=...        the reports, newest first
//	GET  /admin/reports/NAME?token=...   a report
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// seenFor is how long a user who stopped using Linkcord keeps its logo.
	seenFor = 60 * 24 * time.Hour
	// keepReports is how long reports are kept, and maxReports how many.
	keepReports = 90 * 24 * time.Hour
	maxReports  = 5000
	maxReport   = 512 << 10
)

var (
	snowflake = regexp.MustCompile(`^[0-9]{5,20}$`)
	safeName  = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}/[0-9A-Za-z._-]+\.txt$`)
	tag       = regexp.MustCompile(`[^0-9A-Za-z._-]+`)
)

type server struct {
	dir   string
	token string

	mu    sync.Mutex
	users map[string]user
	dirty bool

	limits *limiter
}

type user struct {
	Seen    time.Time `json:"seen"`
	Version string    `json:"version,omitempty"`
}

func main() {
	addr := flag.String("addr", "127.0.0.1:5310", "address to listen on")
	dir := flag.String("data", "/var/lib/linkcord-api", "folder of the data")
	flag.Parse()

	s := &server{dir: *dir, users: make(map[string]user), limits: newLimiter()}
	if err := os.MkdirAll(filepath.Join(s.dir, "reports"), 0o700); err != nil {
		log.Fatal(err)
	}
	s.token = s.adminToken()
	if data, err := os.ReadFile(filepath.Join(s.dir, "users.json")); err == nil {
		if err := json.Unmarshal(data, &s.users); err != nil {
			log.Printf("could not read the users: %v", err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("POST /api/hello", s.hello)
	mux.HandleFunc("GET /api/users", s.listUsers)
	mux.HandleFunc("POST /api/report", s.report)
	mux.HandleFunc("GET /admin/reports", s.adminList)
	mux.HandleFunc("GET /admin/reports/{day}/{name}", s.adminReport)
	srv := &http.Server{
		Addr: *addr, Handler: mux,
		ReadTimeout: 20 * time.Second, WriteTimeout: 20 * time.Second, MaxHeaderBytes: 16 << 10,
	}

	go s.keep()
	go func() {
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
		<-stop
		s.saveUsers()
		srv.Close()
	}()
	log.Printf("listening on %s, data in %s", *addr, *dir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// adminToken returns the token that lets the reports be read, made the first
// time.
func (s *server) adminToken() string {
	path := filepath.Join(s.dir, "admin-token")
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		return strings.TrimSpace(string(data))
	}
	b := make([]byte, 24)
	rand.Read(b)
	t := hex.EncodeToString(b)
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		log.Fatal(err)
	}
	return t
}

// ip is who asks, as the web server in front says.
func ip(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	return r.RemoteAddr
}

func (s *server) hello(w http.ResponseWriter, r *http.Request) {
	if !s.limits.allow("hello:"+ip(r), 30) {
		http.Error(w, "too many", http.StatusTooManyRequests)
		return
	}
	var in struct {
		UserID  string `json:"user_id"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil || !snowflake.MatchString(in.UserID) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.users[in.UserID] = user{Seen: time.Now().UTC(), Version: clip(tag.ReplaceAllString(in.Version, ""), 32)}
	s.dirty = true
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listUsers(w http.ResponseWriter, r *http.Request) {
	if !s.limits.allow("users:"+ip(r), 240) {
		http.Error(w, "too many", http.StatusTooManyRequests)
		return
	}
	s.mu.Lock()
	ids := make([]string, 0, len(s.users))
	for id, u := range s.users {
		if time.Since(u.Seen) < seenFor {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	slices.Sort(ids)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=60")
	json.NewEncoder(w).Encode(map[string]any{"users": ids})
}

func (s *server) report(w http.ResponseWriter, r *http.Request) {
	if !s.limits.allow("report:"+ip(r), 20) {
		http.Error(w, "too many", http.StatusTooManyRequests)
		return
	}
	var in struct {
		Version string `json:"version"`
		OS      string `json:"os"`
		Kind    string `json:"kind"`
		Text    string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxReport+4<<10)).Decode(&in); err != nil || strings.TrimSpace(in.Text) == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	kind := clip(tag.ReplaceAllString(in.Kind, ""), 16)
	version := clip(tag.ReplaceAllString(in.Version, ""), 32)
	b := make([]byte, 4)
	rand.Read(b)
	day := filepath.Join(s.dir, "reports", now.Format("2006-01-02"))
	if err := os.MkdirAll(day, 0o700); err != nil {
		http.Error(w, "could not keep it", http.StatusInternalServerError)
		return
	}
	name := fmt.Sprintf("%s-%s-%s-%s.txt", now.Format("150405"), kind, version, hex.EncodeToString(b))
	body := fmt.Sprintf("Linkcord %s on %s, %s, %s\n\n%s\n", in.Version, clip(in.OS, 200), kind, now.Format(time.RFC3339), clip(in.Text, maxReport))
	if err := os.WriteFile(filepath.Join(day, name), []byte(body), 0o600); err != nil {
		http.Error(w, "could not keep it", http.StatusInternalServerError)
		return
	}
	log.Printf("report %s/%s", now.Format("2006-01-02"), name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) admin(w http.ResponseWriter, r *http.Request) bool {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// reports are the reports kept, newest first, as day/name.
func (s *server) reports() []string {
	files, _ := filepath.Glob(filepath.Join(s.dir, "reports", "*", "*.txt"))
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = filepath.Base(filepath.Dir(f)) + "/" + filepath.Base(f)
	}
	slices.Sort(names)
	slices.Reverse(names)
	return names
}

func (s *server) adminList(w http.ResponseWriter, r *http.Request) {
	if !s.admin(w, r) {
		return
	}
	s.mu.Lock()
	users := len(s.users)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><title>Linkcord reports</title><body style='font-family:sans-serif'><h1>Linkcord</h1><p>%d users seen.</p><h2>Reports</h2><ul>", users)
	tok := r.URL.Query().Get("token")
	for _, n := range s.reports() {
		fmt.Fprintf(w, "<li><a href='reports/%s?token=%s'>%s</a></li>", n, tok, n)
	}
	fmt.Fprint(w, "</ul>")
}

func (s *server) adminReport(w http.ResponseWriter, r *http.Request) {
	if !s.admin(w, r) {
		return
	}
	name := r.PathValue("day") + "/" + r.PathValue("name")
	if !safeName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(s.dir, "reports", filepath.FromSlash(name)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

// keep saves the users every minute, and prunes old reports every hour.
func (s *server) keep() {
	t := time.NewTicker(time.Minute)
	for i := 0; ; i++ {
		<-t.C
		s.saveUsers()
		if i%60 == 0 {
			s.prune()
		}
	}
}

func (s *server) saveUsers() {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return
	}
	for id, u := range s.users {
		if time.Since(u.Seen) > seenFor {
			delete(s.users, id)
		}
	}
	data, err := json.Marshal(s.users)
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, "users.json")
	if err := os.WriteFile(path+".tmp", data, 0o600); err == nil {
		os.Rename(path+".tmp", path)
	}
}

// prune removes reports older than keepReports, and the oldest beyond
// maxReports.
func (s *server) prune() {
	names := s.reports()
	for i, n := range names {
		day, err := time.Parse("2006-01-02", strings.SplitN(n, "/", 2)[0])
		if i >= maxReports || err == nil && time.Since(day) > keepReports {
			os.Remove(filepath.Join(s.dir, "reports", filepath.FromSlash(n)))
		}
	}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// limiter allows each key some requests an hour.
type limiter struct {
	mu    sync.Mutex
	hour  time.Time
	count map[string]int
}

func newLimiter() *limiter { return &limiter{count: make(map[string]int)} }

func (l *limiter) allow(key string, perHour int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h := time.Now().Truncate(time.Hour); h != l.hour {
		l.hour, l.count = h, make(map[string]int)
	}
	l.count[key]++
	return l.count[key] <= perHour
}
