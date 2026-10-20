package gui

import (
	"log/slog"
	"net"
	"slices"
	"time"
)

// watchNetwork reconnects to Discord when the computer's IP addresses
// change, as on joining another network or a router giving a new address:
// the old connections may be dead without either side knowing, and Discord
// only notices a dead connection after missing two heartbeats (about a
// minute and a half). Discord's own client reconnects as the network
// changes; so does this.
func (u *ui) watchNetwork() {
	last := localAddrs()
	for waitAddrChange() {
		// An address change comes in steps (the old one going, the new one
		// coming): let it settle.
		time.Sleep(3 * time.Second)
		now := localAddrs()
		if slices.Equal(now, last) || len(now) == 0 {
			last = now
			continue
		}
		slog.Info("the network changed; reconnecting", "from", last, "to", now)
		last = now
		u.post(u.reconnectAll)
	}
}

// localAddrs is the computer's addresses that reach other machines,
// sorted.
func localAddrs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, ipn.IP.String())
	}
	slices.Sort(out)
	return out
}

// reconnectAll resumes the gateway session on a new connection and, once it
// is back, joins the voice channel again: its connection went out through
// the old address.
func (u *ui) reconnectAll() {
	if u.chat == nil {
		return
	}
	st := u.chat.st
	st.Gateway().Reconnect()
	v := &u.voice
	if v.session == nil || v.st != st || !v.session.Status().Joined {
		return
	}
	session := v.session
	go func() {
		// Joining asks the gateway for the voice server: it has to be up.
		for i := 0; i < 30 && !st.GatewayIsAlive(); i++ {
			time.Sleep(time.Second)
		}
		time.Sleep(3 * time.Second)
		session.Reconnect()
	}()
}
