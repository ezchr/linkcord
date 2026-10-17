package gui

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/discord"
)

func TestStaffRanks(t *testing.T) {
	const guild = discord.GuildID(1)
	st := newState("fixture")
	c := &chatView{u: &ui{}, st: st}
	st.Cabinet.GuildSet(&discord.Guild{ID: guild, OwnerID: 10}, false)
	roles := []discord.Role{
		// A bot's role, higher than all: it does not count.
		{ID: 100, Position: 9, Permissions: discord.PermissionAdministrator, Managed: true},
		{ID: 101, Position: 6, Permissions: discord.PermissionAdministrator},
		{ID: 102, Position: 5, Permissions: discord.PermissionManageMessages | discord.PermissionKickMembers},
		{ID: 103, Position: 3, Permissions: discord.PermissionAdministrator},
	}
	for i := range roles {
		st.Cabinet.RoleSet(guild, &roles[i], false)
	}
	members := map[discord.UserID][]discord.RoleID{
		10: nil,        // the owner
		11: {101},      // the highest admin role
		12: {103},      // a lower admin role
		13: {102},      // moderates, not an admin
		14: {100},      // only a bot's role
		15: {103, 101}, // both admin roles
		16: nil,
	}
	for id, rs := range members {
		st.Cabinet.MemberSet(guild, &discord.Member{User: discord.User{ID: id}, RoleIDs: rs}, false)
	}
	want := map[discord.UserID]staffRank{10: staffTop, 11: staffTop, 12: staffAdmin, 13: staffNone, 14: staffNone, 15: staffTop, 16: staffNone}
	for id, w := range want {
		if got := c.staffRankOf(guild, id); got != w {
			t.Errorf("user %d: rank %d, want %d", id, got, w)
		}
	}
}
