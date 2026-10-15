package session

import (
	"context"
	"sync/atomic"
	"testing"
)

// invalidCountingCallbacks counts the commits and welcomes told invalid.
type invalidCountingCallbacks struct {
	kpCapturingCallbacks
	invalid atomic.Int32
}

func (c *invalidCountingCallbacks) SendInvalidCommitWelcome(uint16) error {
	c.invalid.Add(1)
	return nil
}

// TestCommitOfTransitionJoinedByWelcome checks that a member added by a
// transition, which gets that transition's commit beside its welcome, keeps
// the epoch it joined instead of trying to roll back a commit it never made
// and having Discord reset it.
func TestCommitOfTransitionJoinedByWelcome(t *testing.T) {
	cb := &invalidCountingCallbacks{}
	s := New("123456789", cb)
	s.SetChannelID(987654321)
	s.OnSelectProtocolAck(1)
	botKP := cb.lastKeyPackage()
	if len(botKP) == 0 {
		t.Fatal("expected a key package after protocol ack")
	}
	externalSenderPackage := buildExternalSenderPackage(t)
	setExternalSenderPackage(t, s, externalSenderPackage)

	ctx := context.Background()
	peer := peerWithIdentity(t, "987654321")
	peerKP, err := peer.FreshKeyPackageBytes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groupID, err := peer.CreateGroupWithExternalSender(ctx, []byte("12345678"), peerKP, externalSenderPackage)
	if err != nil {
		t.Fatal(err)
	}
	commit, welcome, err := peer.InviteMember(ctx, groupID, botKP)
	if err != nil {
		t.Fatal(err)
	}

	const transition = 7
	s.OnDaveMLSWelcome(transition, welcome)
	if s.Stats().WelcomesJoined != 1 {
		t.Fatalf("the welcome was not joined (failed %d)", s.Stats().WelcomesFailed)
	}
	s.OnDaveMLSPrepareCommitTransition(transition, commit)
	if n := cb.invalid.Load(); n != 0 {
		t.Fatalf("told Discord %d commits or welcomes were invalid", n)
	}
	s.mu.RLock()
	pending := s.pendingEpoch != nil
	s.mu.RUnlock()
	if !pending {
		t.Fatal("the epoch joined by the welcome was lost")
	}
	s.OnDaveExecuteTransition(transition)
	if !s.Ready() {
		t.Fatal("not ready after the transition")
	}
}
