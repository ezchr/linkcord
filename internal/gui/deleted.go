package gui

import (
	"strings"
	"maps"
	"bufio"
	"cmp"
	"encoding/json"
	"image"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"github.com/ayn2op/arikawa/v3/discord"
)

// maxDeleted is how many deleted messages are kept, the newest.
const maxDeleted = 5000

// deletedLog keeps the messages deleted while Linkcord watched, so that they
// stay in their place with a sign. Discord's history no longer returns them,
// so they are kept on disk and put back among the loaded messages.
type deletedLog struct {
	mu   sync.Mutex
	path string // empty: kept in memory only, as in the demo
	msgs map[discord.MessageID]deletedEntry
	// mine are the messages deleted from here, which just go.
	mine map[discord.MessageID]bool
	// off is set when the user chose not to see deleted messages: none
	// are kept, and those kept do not show.
	off bool
	// fmu guards f, the log file open for appending (nil until the first
	// change is written).
	fmu sync.Mutex
	f   *os.File
}

// setOff turns the keeping of deleted messages off, or back on.
func (d *deletedLog) setOff(off bool) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.off = off
	d.mu.Unlock()
}

type deletedEntry struct {
	Message   discord.Message `json:"message"`
	DeletedAt time.Time       `json:"deleted_at"`
}

func newDeletedLog(path string) *deletedLog {
	d := &deletedLog{path: path, msgs: make(map[discord.MessageID]deletedEntry), mine: make(map[discord.MessageID]bool)}
	if path == "" {
		return d
	}
	// Releases before 1.10.5 kept the messages as one JSON array, written
	// whole on every change.
	if data, err := os.ReadFile(path); err == nil {
		var entries []deletedEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			slog.Warn("failed to read deleted messages", "err", err)
		}
		for _, e := range entries {
			d.msgs[e.Message.ID] = e
		}
	}
	d.readLines()
	d.trim()
	d.compact()
	return d
}

// deletedLogPath is %LocalAppData%\linkcord\deleted.json, the file of old
// releases; the log itself is deleted.jsonl beside it (linesPath).
func deletedLogPath() string {
	return filepath.Join(filepath.Dir(logPath()), "deleted.json")
}

// The log is a file of JSON lines: one written per message deleted, as it is
// deleted, a few hundred bytes, instead of the whole file again (5,000
// messages, 6 MB, which with a deletion a few seconds apart on a busy
// account was gigabytes of garbage an hour). A line is a deletedEntry, which
// replaces any earlier one of the message, or {"forget": id}. At start the
// file is read and written again with the newest maxDeleted only.
type deletedLine struct {
	deletedEntry
	Forget discord.MessageID `json:"forget,omitempty"`
}

func (d *deletedLog) linesPath() string {
	return strings.TrimSuffix(d.path, ".json") + ".jsonl"
}

// readLines applies the lines of the log to d.msgs.
func (d *deletedLog) readLines() {
	f, err := os.Open(d.linesPath())
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var l deletedLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue // a line cut short by a crash
		}
		switch {
		case l.Forget.IsValid():
			delete(d.msgs, l.Forget)
		case l.Message.ID.IsValid():
			d.msgs[l.Message.ID] = l.deletedEntry
		}
	}
}

// trim keeps the newest maxDeleted messages. d.mu is held, or d is not
// shared yet.
func (d *deletedLog) trim() {
	if len(d.msgs) <= maxDeleted {
		return
	}
	oldest := slices.SortedFunc(func(yield func(deletedEntry) bool) {
		for _, e := range d.msgs {
			if !yield(e) {
				return
			}
		}
	}, func(a, b deletedEntry) int { return a.DeletedAt.Compare(b.DeletedAt) })
	for _, e := range oldest[:len(d.msgs)-maxDeleted] {
		delete(d.msgs, e.Message.ID)
	}
}

// compact writes the log again with what d.msgs holds, one line each, and
// removes the file of old releases. d is not shared yet.
func (d *deletedLog) compact() {
	entries := slices.SortedFunc(maps.Values(d.msgs), func(a, b deletedEntry) int { return cmp.Compare(a.Message.ID, b.Message.ID) })
	if err := os.MkdirAll(filepath.Dir(d.path), 0o755); err != nil {
		return
	}
	// Written whole and then renamed, so that a crash leaves the old file.
	tmp := d.linesPath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		slog.Warn("failed to save deleted messages", "err", err)
		return
	}
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			break
		}
	}
	err = w.Flush()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, d.linesPath())
	}
	if err != nil {
		slog.Warn("failed to save deleted messages", "err", err)
		return
	}
	_ = os.Remove(d.path)
}

// write appends a line to the log.
func (d *deletedLog) write(l deletedLine) {
	if d.path == "" {
		return
	}
	data, err := json.Marshal(l)
	if err != nil {
		return
	}
	data = append(data, '\n')
	d.fmu.Lock()
	defer d.fmu.Unlock()
	if d.f == nil {
		f, err := os.OpenFile(d.linesPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			slog.Warn("failed to save deleted messages", "err", err)
			return
		}
		d.f = f
	}
	if _, err := d.f.Write(data); err != nil {
		slog.Warn("failed to save deleted messages", "err", err)
	}
}

// record keeps a message that was just deleted. It may be called from any
// goroutine. Deletions come from every channel the store holds, a thousand an
// hour on a busy account: each is one line added to the log.
func (d *deletedLog) record(m discord.Message) {
	if d == nil || !m.ID.IsValid() {
		return
	}
	d.mu.Lock()
	if _, ok := d.msgs[m.ID]; ok || d.mine[m.ID] || d.off {
		d.mu.Unlock()
		return
	}
	e := deletedEntry{Message: m, DeletedAt: time.Now()}
	d.msgs[m.ID] = e
	// Trimmed back to maxDeleted once it is a twentieth over, so that the
	// sort runs once every few hundred deletions, not on each. The file
	// keeps the others until the next start.
	if len(d.msgs) > maxDeleted+maxDeleted/20 {
		d.trim()
	}
	d.mu.Unlock()
	d.write(deletedLine{deletedEntry: e})
}

// flush closes the log file; the next change opens it again.
func (d *deletedLog) flush() {
	if d == nil {
		return
	}
	d.fmu.Lock()
	defer d.fmu.Unlock()
	if d.f != nil {
		d.f.Close()
		d.f = nil
	}
}

// historyStart is where loaded history begins: its oldest message, or the
// beginning of the channel when there is nothing older.
func historyStart(msgs []discord.Message, more bool) discord.MessageID {
	if !more || len(msgs) == 0 {
		return 0
	}
	return slices.MinFunc(msgs, func(a, b discord.Message) int { return cmp.Compare(a.ID, b.ID) }).ID
}

// deleting marks a message the user deletes, which is not kept.
func (d *deletedLog) deleting(id discord.MessageID) {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.mine[id] = true
	d.mu.Unlock()
}

func (d *deletedLog) has(id discord.MessageID) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.msgs[id]
	return ok && !d.off
}

// merge puts the deleted messages of a channel from from up to, but not
// including, to back among msgs, in order; to zero has no end. The stretch is
// what the loaded history covers, so that older ones come with older history.
func (d *deletedLog) merge(ch discord.ChannelID, msgs []discord.Message, from, to discord.MessageID) []discord.Message {
	if d == nil {
		return msgs
	}
	have := make(map[discord.MessageID]bool, len(msgs))
	for _, m := range msgs {
		have[m.ID] = true
	}
	d.mu.Lock()
	added := false
	if d.off {
		d.mu.Unlock()
		return msgs
	}
	for _, e := range d.msgs {
		m := e.Message
		if m.ChannelID == ch && m.ID >= from && (to == 0 || m.ID < to) && !have[m.ID] {
			msgs = append(msgs, m)
			added = true
		}
	}
	d.mu.Unlock()
	if added {
		msgs = sortedMessages(msgs)
	}
	return msgs
}

// messageDeleted keeps a deleted message in its place, marked, when its
// contents are known; otherwise it goes.
func (c *chatView) messageDeleted(id discord.MessageID) {
	if i := c.messageIndex(id); i >= 0 {
		c.deleted.record(c.messages[i])
	}
	c.forgetMessage(id)
	if !c.deleted.has(id) {
		c.messages = slices.DeleteFunc(c.messages, func(m discord.Message) bool { return m.ID == id })
	}
}

// layoutDeletedSign draws the sign at the end of a deleted message, which
// says so under the pointer.
func (c *chatView) layoutDeletedSign(gtx layout.Context, id discord.MessageID) layout.Dimensions {
	cl := c.msgClick(id, 9)
	return layout.Inset{Left: unit.Dp(8), Top: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		dims := cl.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return icon(gtx, iconDelete, 16, colorRed)
		})
		if cl.Hovered() {
			// The tooltip goes to the left of the sign, on top of the text.
			macro := op.Record(gtx.Ops)
			tgtx := gtx
			tgtx.Constraints.Min = image.Point{}
			tip := rounded(tgtx, colorServers, 4, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx,
					oneLine(label(c.u.th, 13, colorHeader, "Deleted")).Layout)
			})
			call := macro.Stop()
			off := image.Pt(-tip.Size.X-gtx.Dp(4), (dims.Size.Y-tip.Size.Y)/2)
			defer op.Offset(off).Push(gtx.Ops).Pop()
			op.Defer(gtx.Ops, call)
		}
		return dims
	})
}

// forget takes a kept message away, for good, as the user asked.
func (d *deletedLog) forget(id discord.MessageID) {
	if d == nil {
		return
	}
	d.mu.Lock()
	delete(d.msgs, id)
	d.mine[id] = true
	d.mu.Unlock()
	d.write(deletedLine{Forget: id})
}

// setContent has a kept message say text, for good, as the user changed
// it.
func (d *deletedLog) setContent(id discord.MessageID, text string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	e, ok := d.msgs[id]
	if ok {
		e.Message.Content = text
		d.msgs[id] = e
	}
	d.mu.Unlock()
	if ok {
		d.write(deletedLine{deletedEntry: e})
	}
}
