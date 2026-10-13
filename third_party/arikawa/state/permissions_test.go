package state

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/ayn2op/arikawa/v3/api"
	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/gateway"
	"github.com/ayn2op/arikawa/v3/session"
	"github.com/ayn2op/arikawa/v3/state/store/defaultstore"
	"github.com/ayn2op/arikawa/v3/utils/handler"
	"github.com/ayn2op/arikawa/v3/utils/httputil"
	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

type permissionDriver struct {
	mu       sync.Mutex
	requests map[string]int
	roles    chan struct{}
	once     sync.Once
}

func (d *permissionDriver) NewRequest(ctx context.Context, method, url string) (httpdriver.Request, error) {
	return httpdriver.NewMockRequestWithContext(ctx, method, url, nil, nil), nil
}

func (d *permissionDriver) Do(request httpdriver.Request) (httpdriver.Response, error) {
	path := request.GetPath()
	d.mu.Lock()
	d.requests[path]++
	d.mu.Unlock()

	var body any
	switch path {
	case "/api/v9/guilds/1":
		<-d.roles
		body = discord.Guild{ID: 1, OwnerID: 3}
	case "/api/v9/guilds/1/members/2":
		body = discord.Member{User: discord.User{ID: 2}}
	case "/api/v9/guilds/1/roles":
		d.once.Do(func() { close(d.roles) })
		body = []discord.Role{{ID: 1, Permissions: discord.PermissionViewChannel}}
	}
	return httpdriver.NewMockResponse(http.StatusOK, http.Header{}, body), nil
}

func (d *permissionDriver) count(path string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.requests[path]
}

func TestPermissionsFetchesMissingDataOnce(t *testing.T) {
	driver := &permissionDriver{requests: map[string]int{}, roles: make(chan struct{})}
	client := api.NewCustomClient("token", httputil.NewClientWithDriver(driver))
	session := session.NewCustom(gateway.DefaultIdentifier("token"), client, handler.New())
	cabinet := defaultstore.New()
	if err := cabinet.ChannelSet(&discord.Channel{ID: 10, GuildID: 1}, false); err != nil {
		t.Fatal(err)
	}

	state := NewFromSession(session, cabinet)
	if _, err := state.Permissions(10, 2); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v9/guilds/1",
		"/api/v9/guilds/1/members/2",
		"/api/v9/guilds/1/roles",
	} {
		if got := driver.count(path); got != 1 {
			t.Errorf("requests to %s = %d, want 1", path, got)
		}
	}
}
