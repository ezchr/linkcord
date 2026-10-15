package gui

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Games are detected as Discord's app detects them: Discord lists the
// programs it knows as games, by the name and folder of their executable,
// and what runs is matched against that list.

// detectableURL is Discord's list of the games it detects, some 13 MB.
const detectableURL = "https://discord.com/api/v9/applications/detectable"

// knownGame is a game of Discord's list: its application and the endings of
// the paths of its executables on Windows, such as "gta5.exe" or "grand theft
// auto v/gta5.exe".
type knownGame struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

// gameList finds games by the file name of their executables.
type gameList map[string][]gameExe

type gameExe struct {
	game *knownGame
	// suffix is the end of the path, folders included, to match.
	suffix string
}

func gamesPath() string { return filepath.Join(filepath.Dir(logPath()), "games.json") }

// loadGames is Discord's list, kept a day in games.json, or asked for again.
// When Discord cannot be reached, the old list does.
func loadGames() (gameList, error) {
	file := gamesPath()
	info, err := os.Stat(file)
	if err != nil || time.Since(info.ModTime()) > 24*time.Hour {
		if games, err := fetchGames(); err == nil {
			if data, err := json.Marshal(games); err == nil {
				if err := os.WriteFile(file, data, 0o644); err != nil {
					slog.Warn("could not keep the list of games", "err", err)
				}
			}
			return indexGames(games), nil
		} else {
			slog.Warn("could not get Discord's list of games", "err", err)
		}
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var games []knownGame
	if err := json.Unmarshal(data, &games); err != nil {
		return nil, err
	}
	return indexGames(games), nil
}

// fetchGames asks Discord for its list of games, keeping only what finds
// them on Windows.
func fetchGames() ([]knownGame, error) {
	client := http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Get(detectableURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil, fmt.Errorf("%s", resp.Status)
	}
	var apps []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Executables []struct {
			OS         string `json:"os"`
			Name       string `json:"name"`
			IsLauncher bool   `json:"is_launcher"`
		} `json:"executables"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apps); err != nil {
		return nil, err
	}
	var games []knownGame
	for _, a := range apps {
		g := knownGame{ID: a.ID, Name: a.Name}
		for _, e := range a.Executables {
			// Launchers are not the game; names that begin with > need the
			// program's arguments too, as Half-Life 2's or Java's.
			if e.OS != "win32" || e.IsLauncher || e.Name == "" || strings.HasPrefix(e.Name, ">") {
				continue
			}
			g.Paths = append(g.Paths, strings.ToLower(e.Name))
		}
		if len(g.Paths) > 0 {
			games = append(games, g)
		}
	}
	return games, nil
}

func indexGames(games []knownGame) gameList {
	list := make(gameList)
	for i := range games {
		g := &games[i]
		for _, p := range g.Paths {
			base := path.Base(p)
			list[base] = append(list[base], gameExe{g, p})
		}
	}
	return list
}

// match is the game an executable at exe is, or nil: the one whose path
// ends it most closely, folders and all.
func (l gameList) match(exe string) *knownGame {
	exe = strings.ToLower(filepath.ToSlash(exe))
	var best *gameExe
	for i, e := range l[path.Base(exe)] {
		if exe != e.suffix && !strings.HasSuffix(exe, "/"+e.suffix) {
			continue
		}
		if best == nil || len(e.suffix) > len(best.suffix) {
			best = &l[path.Base(exe)][i]
		}
	}
	if best == nil {
		return nil
	}
	return best.game
}

// playing is a game running, and since when.
type playing struct {
	game  *knownGame
	since time.Time
}

// watchGames looks at what runs every 15 seconds and shows the games of
// Discord's list as being played, as Discord's app does, until done.
func (c *chatView) watchGames() {
	go func() {
		list, err := loadGames()
		if err != nil {
			slog.Warn("games will not be detected", "err", err)
			return
		}
		since := make(map[string]time.Time)
		var last []string
		tick := time.NewTicker(15 * time.Second)
		defer tick.Stop()
		for {
			found := make(map[string]*knownGame)
			for _, exe := range runningExecutables() {
				if g := list.match(exe); g != nil {
					found[g.ID] = g
				}
			}
			now := time.Now()
			var ids []string
			var games []playing
			for id, g := range found {
				if _, ok := since[id]; !ok {
					since[id] = now
				}
				ids = append(ids, id)
				games = append(games, playing{g, since[id]})
			}
			for id := range since {
				if found[id] == nil {
					delete(since, id)
				}
			}
			slices.Sort(ids)
			if !slices.Equal(ids, last) {
				last = ids
				// The newest game first, as Discord shows it.
				slices.SortFunc(games, func(a, b playing) int { return b.since.Compare(a.since) })
				c.u.post(func() {
					if c.closed {
						return
					}
					c.games = games
					c.pushPresence()
				})
			}
			select {
			case <-c.done:
				return
			case <-tick.C:
			}
		}
	}()
}

// gameActivities are the games being played as activities as the gateway
// takes them, but those whose own program shows its Rich Presence.
func (c *chatView) gameActivities() []json.RawMessage {
	if c.u.app.NoGameDetection {
		return nil
	}
	var out []json.RawMessage
	for _, p := range c.games {
		if slices.ContainsFunc(mapValues(c.rpcActivities), func(a rpcActivity) bool { return a.clientID == p.game.ID }) {
			continue
		}
		data, err := json.Marshal(map[string]any{
			"type": 0, "name": p.game.Name, "application_id": p.game.ID,
			"timestamps": map[string]any{"start": p.since.UnixMilli()},
		})
		if err == nil {
			out = append(out, data)
		}
	}
	return out
}

func mapValues[K comparable, V any](m map[K]V) []V {
	out := make([]V, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}
