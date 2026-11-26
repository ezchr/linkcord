package gui

import (
	"testing"
	"time"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestRestrictPerms(t *testing.T) {
	now := time.Now()
	me := discord.UserID(1)
	all := discord.PermissionSendMessages | discord.PermissionViewChannel | discord.PermissionReadMessageHistory |
		discord.PermissionKickMembers | discord.PermissionManageMessages
	g := discord.Guild{OwnerID: 2}
	later := discord.NewTimestamp(now.Add(time.Hour))
	earlier := discord.NewTimestamp(now.Add(-time.Hour))

	if got := restrictPerms(g, discord.Member{}, me, false, all, now); got != all {
		t.Errorf("plain member changed: %v", got)
	}
	if got := restrictPerms(g, discord.Member{CommunicationDisabledUntil: later}, me, false, all, now); got != readOnlyPerms {
		t.Errorf("timed out: got %v, want read only", got)
	}
	if got := restrictPerms(g, discord.Member{CommunicationDisabledUntil: earlier}, me, false, all, now); got != all {
		t.Errorf("ended timeout still applied: %v", got)
	}
	admin := all | discord.PermissionAdministrator
	if got := restrictPerms(g, discord.Member{CommunicationDisabledUntil: later}, me, false, admin, now); got != admin {
		t.Errorf("admin timed out lost permissions: %v", got)
	}
	if got := restrictPerms(g, discord.Member{IsPending: true}, me, false, admin, now); got != readOnlyPerms {
		t.Errorf("pending admin: got %v, want read only", got)
	}
	g2 := discord.Guild{OwnerID: 2, MFA: discord.ElevatedMFA}
	if got := restrictPerms(g2, discord.Member{}, me, false, all, now); got.Has(discord.PermissionKickMembers) || got.Has(discord.PermissionManageMessages) || !got.Has(discord.PermissionSendMessages) {
		t.Errorf("2FA server, no 2FA: %v", got)
	}
	if got := restrictPerms(g2, discord.Member{}, me, true, all, now); got != all {
		t.Errorf("2FA server, has 2FA: %v", got)
	}
	g3 := discord.Guild{OwnerID: me, MFA: discord.ElevatedMFA}
	if got := restrictPerms(g3, discord.Member{CommunicationDisabledUntil: later}, me, false, all, now); got != all {
		t.Errorf("owner restricted: %v", got)
	}
}

func TestSplitScheduled(t *testing.T) {
	a, b := discord.UserID(10), discord.UserID(20)
	all := []scheduledMsg{{ID: 1, Owner: a}, {ID: 2, Owner: b}, {ID: 3}}
	mine, others := splitScheduled(all, a)
	if len(mine) != 2 || mine[0].ID != 1 || mine[1].ID != 3 || mine[1].Owner != a {
		t.Errorf("mine = %+v", mine)
	}
	if len(others) != 1 || others[0].ID != 2 {
		t.Errorf("others = %+v", others)
	}
}
