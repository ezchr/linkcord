package session

// A simulated Discord voice gateway, run against real sessions, following
// Discord's DAVE protocol (protocol.md): it hands out the external sender,
// proposes adds and removes as the external sender, broadcasts the FIRST
// commit it receives for an epoch (op 29) with welcomes (op 30), executes a
// transition once every member is ready or a timeout passes (op 22), resets a
// sole remaining member (op 24, then op 21 with transition 0), and answers an
// invalid commit/welcome report (op 31) by removing and re-adding the member.
//
// Members join and leave over time, and after each change every member must
// decrypt every other member's media, as Discord's own clients do.

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/disgoorg/godave"
	daveframe "github.com/thomas-vilte/dave-go/frame"
	"github.com/thomas-vilte/mls-go/ciphersuite"
	"github.com/thomas-vilte/mls-go/framing"
	"github.com/thomas-vilte/mls-go/group"
	"github.com/thomas-vilte/mls-go/keypackages"
)

type simMember struct {
	uid     string
	ssrc    uint32
	s       *Session
	kp      []byte // the latest key package received
	inGroup bool   // a member of the established group
	gone    bool
}

type simGateway struct {
	t         *testing.T
	channelID uint64
	extPkg    []byte
	extPriv   *ecdsa.PrivateKey

	work chan func()
	idle sync.WaitGroup

	// Only touched on the work goroutine.
	members     map[string]*simMember
	order       []string
	established bool
	epoch       uint64 // the established group's epoch, as the gateway knows it
	// committed is set once a commit was broadcast for the current epoch:
	// later commits for it are dropped.
	committed  bool
	transition uint16
	// waiting are the members a transition waits for, by transition.
	waiting map[uint16]map[string]bool
	// proposed are the members a batch was sent to, for this epoch: whose
	// commit can win.
	proposed map[string]bool
	// sent are the adds proposed this epoch, by recipient and member added:
	// each is proposed once.
	sent map[string]bool
	// proposals counts, by recipient, the proposals sent this epoch: a
	// commit must reference them all.
	proposals map[string]int
}

func newSimGateway(t *testing.T) *simGateway {
	pkg, priv := buildExternalSenderPackageWithKey(t)
	g := &simGateway{
		t: t, channelID: 987654321, extPkg: pkg, extPriv: priv,
		work: make(chan func(), 4096), members: map[string]*simMember{},
		waiting: map[uint16]map[string]bool{}, proposed: map[string]bool{}, sent: map[string]bool{}, proposals: map[string]int{},
	}
	go func() {
		for f := range g.work {
			f()
			g.idle.Done()
		}
	}()
	return g
}

// do queues f on the gateway's goroutine, as a message to the gateway.
func (g *simGateway) do(f func()) {
	g.idle.Add(1)
	g.work <- f
}

// simCallbacks are a session's connection to the gateway.
type simCallbacks struct {
	g   *simGateway
	uid string
}

func (c *simCallbacks) SendMLSKeyPackage(kp []byte) error {
	kp = append([]byte(nil), kp...)
	c.g.do(func() { c.g.onKeyPackage(c.uid, kp) })
	return nil
}
func (c *simCallbacks) SendMLSCommitWelcome(b []byte) error {
	b = append([]byte(nil), b...)
	c.g.do(func() { c.g.onCommit(c.uid, b) })
	return nil
}
func (c *simCallbacks) SendReadyForTransition(id uint16) error {
	c.g.do(func() { c.g.onReady(c.uid, id) })
	return nil
}
func (c *simCallbacks) SendInvalidCommitWelcome(id uint16) error {
	c.g.do(func() { c.g.onInvalid(c.uid, id) })
	return nil
}

// join connects a member whose session commits after delay.
func (g *simGateway) join(uid string, delay time.Duration) {
	g.do(func() {
		m := &simMember{uid: uid, ssrc: uint32(len(g.order) + 100)}
		cb := &simCallbacks{g: g, uid: uid}
		opts := []Option{WithRecoveryTimeout(3 * time.Second)}
		if os.Getenv("SIM_DEBUG") != "" {
			opts = append(opts, WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))))
		}
		m.s = New(godave.UserID(uid), cb, opts...)
		m.s.SetChannelID(godave.ChannelID(g.channelID))
		m.s.AssignSsrcToCodec(m.ssrc, godave.CodecOpus)
		g.members[uid] = m
		g.order = append(g.order, uid)
		// clients_connect: each knows the others.
		for _, o := range g.live() {
			if o.uid == uid {
				continue
			}
			o.s.AddUser(godave.UserID(uid))
			m.s.AddUser(godave.UserID(o.uid))
		}
		// select_protocol_ack sends the key package; then the external sender.
		m.s.OnSelectProtocolAck(1)
		m.s.OnDaveMLSExternalSenderPackage(g.extPkg)
		g.propose()
	})
}

// leave disconnects a member.
func (g *simGateway) leave(uid string) {
	g.do(func() {
		m := g.members[uid]
		if m == nil || m.gone {
			return
		}
		m.gone = true
		wasIn := m.inGroup
		m.inGroup = false
		m.s.Close()
		for _, o := range g.live() {
			o.s.RemoveUser(godave.UserID(uid))
		}
		for id, w := range g.waiting {
			delete(w, uid)
			g.maybeExecute(id)
		}
		in := g.inGroup()
		if g.established && len(in) == 1 && len(g.pending()) == 0 {
			g.soleReset(in[0])
			return
		}
		if g.established && wasIn {
			g.proposeRemove(m)
		}
		g.propose()
	})
}

func (g *simGateway) live() []*simMember {
	var out []*simMember
	for _, uid := range g.order {
		if m := g.members[uid]; !m.gone {
			out = append(out, m)
		}
	}
	return out
}

func (g *simGateway) inGroup() []*simMember {
	var out []*simMember
	for _, m := range g.live() {
		if m.inGroup {
			out = append(out, m)
		}
	}
	return out
}

func (g *simGateway) pending() []*simMember {
	var out []*simMember
	for _, m := range g.live() {
		if !m.inGroup && m.kp != nil {
			out = append(out, m)
		}
	}
	return out
}

func (g *simGateway) onKeyPackage(uid string, kp []byte) {
	if m := g.members[uid]; m != nil && !m.gone {
		m.kp = kp
		g.propose()
	}
}

// soleReset makes m the only member of a new, unestablished group.
func (g *simGateway) soleReset(m *simMember) {
	g.established, g.committed, g.epoch = false, false, 0
	g.proposed = map[string]bool{}
	g.sent = map[string]bool{}
	g.proposals = map[string]int{}
	m.inGroup = false
	m.s.OnDavePrepareEpoch(1, 1)
	m.s.OnDavePrepareTransition(0, 1)
	// The reset member's key package for the next group came from
	// OnDavePrepareEpoch, which sent it to the gateway.
}

// propose sends add proposals for the members waiting to join.
func (g *simGateway) propose() {
	pend := g.pending()
	if len(pend) == 0 {
		return
	}
	if !g.established {
		// No group yet: everyone is pending, each with a local group; each
		// is proposed the others, and the first commit makes the group.
		if len(pend) < 2 {
			return
		}
		for _, to := range pend {
			var msgs [][]byte
			for _, o := range pend {
				if k := to.uid + ">" + o.uid; o.uid != to.uid && !g.sent[k] {
					g.sent[k] = true
					msgs = append(msgs, g.addProposal(o.kp, g.groupID(), 0))
				}
			}
			if len(msgs) == 0 {
				continue
			}
			g.proposed[to.uid] = true
			g.proposals[to.uid] += len(msgs)
			to.s.OnDaveMLSProposals(batch(msgs))
		}
		return
	}
	in := g.inGroup()
	if len(in) == 0 {
		return
	}
	var msgs [][]byte
	for _, p := range pend {
		if k := "*>" + p.uid; !g.sent[k] {
			g.sent[k] = true
			msgs = append(msgs, g.addProposal(p.kp, g.groupID(), g.epoch))
		}
	}
	if len(msgs) == 0 {
		return
	}
	b := batch(msgs)
	for _, m := range in {
		g.proposed[m.uid] = true
		g.proposals[m.uid] += len(msgs)
		m.s.OnDaveMLSProposals(b)
	}
}

// proposeRemove proposes removing a member who left the group.
func (g *simGateway) proposeRemove(gone *simMember) {
	in := g.inGroup()
	if len(in) == 0 {
		return
	}
	leaf, ok := g.leafOf(in[0], gone.uid)
	if !ok {
		return
	}
	p := g.externalProposal(group.ProposalMarshal(group.NewRemoveProposal(group.LeafNodeIndex(leaf))), g.groupID(), g.epoch)
	b := batch([][]byte{p})
	for _, m := range in {
		g.proposed[m.uid] = true
		g.proposals[m.uid]++
		m.s.OnDaveMLSProposals(b)
	}
}

func (g *simGateway) leafOf(via *simMember, uid string) (uint32, bool) {
	via.s.mu.Lock()
	defer via.s.mu.Unlock()
	if via.s.mlsClient == nil {
		return 0, false
	}
	members, err := via.s.mlsClient.client.ListMembers(context.Background(), g.groupID())
	if err != nil {
		return 0, false
	}
	want, _ := userIDToIdentityBytes(godave.UserID(uid))
	for _, m := range members {
		if string(m.Identity) == string(want) {
			return m.LeafIndex, true
		}
	}
	return 0, false
}

func (g *simGateway) groupID() []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, g.channelID)
	return b
}

func batch(msgs [][]byte) []byte {
	var body []byte
	for _, m := range msgs {
		body = append(body, m...)
	}
	return append([]byte{0}, writeVLBytes(body)...)
}

func (g *simGateway) addProposal(kp, groupID []byte, epoch uint64) []byte {
	k, err := keypackages.UnmarshalKeyPackage(kp)
	if err != nil {
		g.t.Errorf("gateway: key package: %v", err)
		return nil
	}
	return g.externalProposal(group.ProposalMarshal(group.NewAddProposal(k)), groupID, epoch)
}

func (g *simGateway) externalProposal(data, groupID []byte, epoch uint64) []byte {
	content := framing.FramedContent{
		GroupID:           groupID,
		Epoch:             epoch,
		Sender:            framing.Sender{Type: framing.SenderTypeExternal, SenderIndex: 0},
		AuthenticatedData: []byte{},
		Body:              framing.ProposalBody{Data: data},
	}
	pm, err := framing.NewPublicMessage(content, ciphersuite.NewSignaturePrivateKey(g.extPriv), nil, nil, ciphersuite.MLS128DHKEMP256)
	if err != nil {
		g.t.Errorf("gateway: proposal: %v", err)
		return nil
	}
	return framing.NewMLSMessagePublic(pm).Marshal()
}

// onCommit broadcasts the first commit of the epoch, and its welcome.
func (g *simGateway) onCommit(uid string, payload []byte) {
	m := g.members[uid]
	if m == nil || m.gone || g.committed || !g.proposed[uid] {
		return
	}
	commit, err := splitCommit(payload)
	if err != nil {
		g.t.Errorf("gateway: commit from %s: %v", uid, err)
		return
	}
	welcome := payload[len(commit):]
	msg, err := framing.UnmarshalMLSMessage(commit)
	if err != nil {
		g.t.Errorf("gateway: commit from %s: %v", uid, err)
		return
	}
	pub, ok := msg.AsPublic()
	if !ok || pub == nil || pub.Content.Epoch != g.epoch {
		return // for a past or future epoch
	}
	// A commit must reference every proposal sent this epoch.
	if data, ok := pub.Content.CommitData(); ok {
		c, err := group.UnmarshalCommit(data)
		if err != nil || len(c.Proposals) != g.proposals[uid] {
			g.t.Logf("gateway: epoch %d: rejected commit from %s (%d refs, %d proposals)", g.epoch, uid, len(c.Proposals), g.proposals[uid])
			return
		}
	}
	g.committed = true
	g.transition++
	id := g.transition
	// The committer's group becomes the group: those already in it, and the
	// committer, get the commit; the others it added, the welcome.
	// Those the commit adds: the adds proposed to the committer this epoch.
	var getsCommit, getsWelcome []*simMember
	if g.established {
		getsCommit = g.inGroup()
		for _, o := range g.pending() {
			if g.sent["*>"+o.uid] {
				getsWelcome = append(getsWelcome, o)
			}
		}
	} else {
		getsCommit = []*simMember{m}
		for _, o := range g.pending() {
			if o.uid != uid && g.sent[uid+">"+o.uid] {
				getsWelcome = append(getsWelcome, o)
			}
		}
	}
	g.established = true
	g.epoch++
	g.committed = false
	g.proposed = map[string]bool{}
	g.sent = map[string]bool{}
	g.proposals = map[string]int{}
	w := map[string]bool{}
	for _, o := range getsCommit {
		o.inGroup = true
		w[o.uid] = true
	}
	for _, o := range getsWelcome {
		o.inGroup = true
		w[o.uid] = true
	}
	g.waiting[id] = w
	g.t.Logf("gateway: epoch %d: commit from %s wins as transition %d; commit to %v, welcome to %v", g.epoch-1, uid, id, uids(getsCommit), uids(getsWelcome))
	for _, o := range getsCommit {
		o.s.OnDaveMLSPrepareCommitTransition(id, commit)
	}
	if len(welcome) > 0 {
		for _, o := range getsWelcome {
			o.s.OnDaveMLSWelcome(id, welcome)
		}
	}
	time.AfterFunc(500*time.Millisecond, func() {
		g.do(func() { g.execute(id) })
	})
}

func (g *simGateway) onReady(uid string, id uint16) {
	g.t.Logf("gateway: ready %d from %s (waiting %v)", id, uid, g.waiting[id])
	if w := g.waiting[id]; w != nil {
		delete(w, uid)
		g.maybeExecute(id)
	}
}

func (g *simGateway) maybeExecute(id uint16) {
	if w := g.waiting[id]; w != nil && len(w) == 0 {
		g.execute(id)
	}
}

func (g *simGateway) execute(id uint16) {
	if _, ok := g.waiting[id]; !ok {
		return
	}
	delete(g.waiting, id)
	g.t.Logf("gateway: execute %d to %v", id, uids(g.inGroup()))
	for _, m := range g.inGroup() {
		m.s.OnDaveExecuteTransition(id)
	}
	// Proposals that came in meanwhile, such as a member who joined during
	// the transition.
	g.propose()
}

// onInvalid removes the member who reported an invalid commit or welcome;
// its new key package adds it again.
func (g *simGateway) onInvalid(uid string, _ uint16) {
	m := g.members[uid]
	if m == nil || m.gone {
		return
	}
	wasIn := m.inGroup
	m.inGroup = false
	m.kp = nil
	if wasIn && g.established {
		g.proposeRemove(m)
	}
}

// settle waits for the gateway to have nothing left to do for a while.
func (g *simGateway) settle(d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		g.idle.Wait()
		time.Sleep(50 * time.Millisecond)
	}
	g.idle.Wait()
}

// check reports which members cannot decrypt which others' media.
func (g *simGateway) check() []string {
	done := make(chan []string, 1)
	g.do(func() {
		var bad []string
		live := g.live()
		for _, m := range live {
			st, ss := m.s.State(), m.s.Stats()
			m.s.mu.Lock()
			var roster []string
			var senders []string
			if m.s.mlsClient != nil {
				if ms, err := m.s.mlsClient.client.ListMembers(context.Background(), g.groupID()); err == nil {
					for _, x := range ms {
						roster = append(roster, fmt.Sprintf("%d:%x", x.LeafIndex, x.Identity[len(x.Identity)-2:]))
					}
				}
			}
			if m.s.activeEpoch != nil {
				for u := range m.s.activeEpoch.senders {
					senders = append(senders, string(u))
				}
			}
			auth := ""
			if m.s.mlsClient != nil {
				if a, err := m.s.mlsClient.client.EpochAuthenticator(context.Background(), g.groupID()); err == nil {
					auth = fmt.Sprintf("%x", a[:4])
				}
			}
			retained := m.s.retainedSendRatchet != nil && time.Now().Before(m.s.retainedSendExpiresAt)
			g.t.Logf("proto %s: version=%d degradedSince=%v pending=%v activeID=%v", m.uid, m.s.protocolVersion, m.s.degradedSince, m.s.pendingEpoch != nil, func() uint64 { if m.s.activeEpoch != nil { return m.s.activeEpoch.id }; return 0 }())
			g.t.Logf("send %s: ratchet=%v retainedInUse=%v retainedLeft=%v", m.uid, m.s.sendRatchet != nil, retained, time.Until(m.s.retainedSendExpiresAt).Round(time.Millisecond))
			m.s.mu.Unlock()
			g.t.Logf("auth=%s state %s: inGroup=%v ready=%v epoch=%d welcomes=%d/%d commits=%d/%d roster=%v senders=%v", auth, m.uid, m.inGroup, st.Ready, st.EpochID, ss.WelcomesJoined, ss.WelcomesFailed, ss.CommitsProcessed, ss.CommitsFailed, roster, senders)
		}
		for _, from := range live {
			frame := []byte("opus frame from " + from.uid)
			enc := make([]byte, from.s.MaxEncryptedFrameSize(len(frame)))
			n, err := from.s.Encrypt(from.ssrc, frame, enc)
			if err != nil {
				bad = append(bad, fmt.Sprintf("%s cannot encrypt: %v", from.uid, err))
				continue
			}
			for _, to := range live {
				if to == from {
					continue
				}
				out := make([]byte, to.s.MaxDecryptedFrameSize(godave.UserID(from.uid), n))
				m, err := to.s.Decrypt(godave.UserID(from.uid), enc[:n], out)
				if err != nil || string(out[:m]) != string(frame) {
					bad = append(bad, fmt.Sprintf("%s cannot hear %s (%v)", to.uid, from.uid, err))
				}
			}
		}
		done <- bad
	})
	return <-done
}

func uidN(i int) string { return strconv.Itoa(1000 + i) }

// simDelays are how long members wait before committing: Linkcord's delay,
// and none, as Discord's own clients.
const (
	linkcordDelay = 0
	officialDelay = 0
)

// TestSimStreamNewcomers is a stream that runs a while: viewers come and go,
// and the streamer is left alone between them, which resets the group. Each
// newcomer must see the stream.
func TestSimStreamNewcomers(t *testing.T) {
	for _, tc := range []struct {
		name               string
		streamer, viewers  time.Duration
	}{
		{"linkcord streamer, official viewers", linkcordDelay, officialDelay},
		{"linkcord streamer, linkcord viewers", linkcordDelay, linkcordDelay},
		{"official streamer, linkcord viewers", officialDelay, linkcordDelay},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := newSimGateway(t)
			streamer := uidN(0)
			g.join(streamer, tc.streamer)
			g.settle(time.Second)
			for round := 1; round <= 3; round++ {
				viewer := uidN(round)
				g.join(viewer, tc.viewers)
				g.settle(2 * time.Second)
				if bad := g.check(); len(bad) > 0 {
					t.Fatalf("round %d, viewer %s joined: %v", round, viewer, bad)
				}
				g.leave(viewer)
				g.settle(time.Second)
			}
		})
	}
}

// TestSimVoiceChurn is a voice channel that stays up while people join and
// leave, some at the same moment, mixing Linkcord and Discord's own clients.
// Everyone must keep hearing everyone.
func TestSimVoiceChurn(t *testing.T) {
	g := newSimGateway(t)
	delays := []time.Duration{linkcordDelay, officialDelay, officialDelay, linkcordDelay, officialDelay}
	for i := 0; i < 3; i++ {
		g.join(uidN(i), delays[i])
		g.settle(1500 * time.Millisecond)
	}
	if bad := g.check(); len(bad) > 0 {
		t.Fatalf("after the first three joined: %v", bad)
	}
	steps := []struct {
		join, leave []int
	}{
		{join: []int{3}},
		{join: []int{4}, leave: []int{1}},
		{leave: []int{3}},
		{join: []int{1}},
		{join: []int{3}, leave: []int{0, 4}},
		{join: []int{0, 4}},
	}
	for i, st := range steps {
		for _, j := range st.leave {
			g.leave(uidN(j))
		}
		for _, j := range st.join {
			id := uidN(j)
			// Rejoining is a new connection, as on Discord.
			g.do(func() { delete(g.members, id); g.order = removeID(g.order, id) })
			g.join(id, delays[j])
		}
		g.settle(2500 * time.Millisecond)
		if bad := g.check(); len(bad) > 0 {
			t.Fatalf("step %d (%+v): %v", i, st, bad)
		}
	}
}

func removeID(ids []string, id string) []string {
	var out []string
	for _, x := range ids {
		if x != id {
			out = append(out, x)
		}
	}
	return out
}

// splitCommit is the commit at the start of a commit and welcome payload:
// the shortest prefix that reads as an MLS message and back to the same
// bytes.
func splitCommit(payload []byte) ([]byte, error) {
	for n := 8; n <= len(payload); n++ {
		msg, err := framing.UnmarshalMLSMessage(payload[:n])
		if err == nil && bytes.Equal(msg.Marshal(), payload[:n]) {
			return payload[:n], nil
		}
	}
	return nil, fmt.Errorf("no commit in %d bytes", len(payload))
}

func uids(ms []*simMember) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.uid)
	}
	return out
}

// TestSimRandomChurn runs a long channel with random joins and leaves, often
// several at once, mixing Linkcord and Discord's own clients, and checks
// after each change that everyone hears everyone.
func TestSimRandomChurn(t *testing.T) {
	seed := time.Now().UnixNano()
	if v, err := strconv.ParseInt(os.Getenv("SIM_SEED"), 10, 64); err == nil {
		seed = v
	}
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	g := newSimGateway(t)
	const people = 6
	in := map[int]bool{}
	delay := func(i int) time.Duration {
		if i%2 == 0 {
			return linkcordDelay
		}
		return officialDelay
	}
	g.join(uidN(0), delay(0))
	g.settle(time.Second)
	in[0] = true
	for step := 0; step < 12; step++ {
		var joins, leaves []int
		for i := 0; i < people; i++ {
			if rng.Intn(3) != 0 {
				continue
			}
			if in[i] && len(in)-len(leaves) > 1 {
				leaves = append(leaves, i)
			} else if !in[i] {
				joins = append(joins, i)
			}
		}
		for _, i := range leaves {
			g.leave(uidN(i))
			delete(in, i)
		}
		for _, i := range joins {
			id := uidN(i)
			g.do(func() { delete(g.members, id); g.order = removeID(g.order, id) })
			g.join(id, delay(i))
			in[i] = true
		}
		g.settle(3 * time.Second)
		if bad := g.check(); len(bad) > 0 {
			t.Fatalf("step %d (join %v, leave %v): %v", step, joins, leaves, bad)
		}
	}
}

// TestSimTimerRecoveryBreaks shows the old timer-driven resets, which the
// debug button brings back, breaking a channel where people join and leave
// together: kept as proof of the bug the fix removes.
func TestSimTimerRecoveryBreaks(t *testing.T) {
	if testing.Short() {
		t.Skip("reproduces a known bug")
	}
	// As the debug button does.
	SetTimerRecovery(true)
	t.Cleanup(func() { SetTimerRecovery(false) })
	broke := 0
	for run := 0; run < 4 && broke == 0; run++ {
		g := newSimGateway(t)
		for i := 0; i < 3; i++ {
			g.join(uidN(i), 0)
			g.settle(1500 * time.Millisecond)
		}
		// Two leave and one joins at once, then two join at once: the
		// batches that reach members after their commit is out.
		g.leave(uidN(1))
		g.leave(uidN(2))
		g.join(uidN(3), 0)
		g.settle(4 * time.Second)
		g.join(uidN(4), 0)
		g.join(uidN(5), 0)
		g.settle(6 * time.Second)
		if bad := g.check(); len(bad) > 0 {
			broke++
			t.Logf("run %d broke, as the old code did: %v", run, bad)
		}
	}
	if broke == 0 {
		t.Fatal("the old behaviour did not break the channel: the debug button reproduces nothing")
	}
}

// linkcordVeil flips the tag of an end-to-end encrypted frame as Linkcord
// does for Linkcord-only voice (arikawa voice.flipTag) and streams (golive
// veil): the same mask at the same place.
func linkcordVeil(f []byte) bool {
	mask := [8]byte{'L', 'i', 'n', 'k', 'c', 'o', 'r', 'd'}
	p, err := daveframe.Parse(f)
	if err != nil {
		return false
	}
	at := len(f) - int(p.SupplementalSize)
	if at < 0 || at+len(mask) > len(f) {
		return false
	}
	for i := range mask {
		f[at+i] ^= mask[i]
	}
	return true
}

// TestSimLinkcordOnlyMedia checks Linkcord-only voice and streams in a
// channel whose keys came through the gateway: a veiled frame fails for a
// receiver decrypting as Discord's clients do, and decrypts for one that
// unveils it, as Linkcord does; also after people join and leave.
func TestSimLinkcordOnlyMedia(t *testing.T) {
	g := newSimGateway(t)
	for i := 0; i < 3; i++ {
		g.join(uidN(i), 0)
		g.settle(1500 * time.Millisecond)
	}
	check := func(when string) {
		done := make(chan error, 1)
		g.do(func() {
			from, to := g.members[uidN(0)], g.members[uidN(1)]
			opus := []byte("an opus frame of voice, long enough to carry a tag")
			h264 := append([]byte{0, 0, 0, 1, 0x65, 0x88, 0x80}, []byte("a keyframe of the stream, long enough")...)
			for _, media := range []struct {
				name string
				enc  func() ([]byte, error)
				want []byte
			}{
				{"voice", func() ([]byte, error) {
					out := make([]byte, from.s.MaxEncryptedFrameSize(len(opus)))
					n, err := from.s.Encrypt(from.ssrc, opus, out)
					return out[:n], err
				}, opus},
				{"stream", func() ([]byte, error) { return from.s.EncryptH264(h264) }, nil},
			} {
				f, err := media.enc()
				if err != nil {
					done <- fmt.Errorf("%s: encrypt: %v", media.name, err)
					return
				}
				if !daveframe.LooksLikeDAVEFrame(f) {
					done <- fmt.Errorf("%s: not end-to-end encrypted", media.name)
					return
				}
				veiled := append([]byte(nil), f...)
				if !linkcordVeil(veiled) {
					done <- fmt.Errorf("%s: could not veil", media.name)
					return
				}
				out := make([]byte, to.s.MaxDecryptedFrameSize(godave.UserID(from.uid), len(veiled)))
				if _, err := to.s.Decrypt(godave.UserID(from.uid), veiled, out); err == nil {
					done <- fmt.Errorf("%s: a veiled frame decrypted as Discord's clients decrypt", media.name)
					return
				}
				unveiled := append([]byte(nil), veiled...)
				linkcordVeil(unveiled)
				n, err := to.s.Decrypt(godave.UserID(from.uid), unveiled, out)
				if err != nil {
					done <- fmt.Errorf("%s: Linkcord could not decrypt a veiled frame: %v", media.name, err)
					return
				}
				if media.want != nil && string(out[:n]) != string(media.want) {
					done <- fmt.Errorf("%s: decrypted to something else", media.name)
					return
				}
			}
			done <- nil
		})
		if err := <-done; err != nil {
			t.Fatalf("%s: %v", when, err)
		}
	}
	check("three in the channel")
	g.leave(uidN(2))
	g.join(uidN(3), 0)
	g.join(uidN(4), 0)
	g.settle(3 * time.Second)
	check("after one left and two joined at once")
}
