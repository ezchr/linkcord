package httputil

import (
	"context"
	"net/http"
	"testing"

	"github.com/ayn2op/arikawa/v3/utils/httputil/httpdriver"
)

type staticDriver struct {
	response httpdriver.Response
}

type testPayload struct {
	Value string `json:"value"`
}

func (d staticDriver) NewRequest(ctx context.Context, method, url string) (httpdriver.Request, error) {
	return httpdriver.NewMockRequestWithContext(ctx, method, url, nil, nil), nil
}

func (d staticDriver) Do(httpdriver.Request) (httpdriver.Response, error) {
	return d.response, nil
}

func TestRequestJSON(t *testing.T) {
	client := NewClientWithDriver(staticDriver{
		response: httpdriver.NewMockResponse(http.StatusOK, nil, testPayload{Value: "value"}),
	})

	got, err := client.RequestJSON[*testPayload](http.MethodGet, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Value != "value" {
		t.Fatalf("got %#v", got)
	}
}

func TestRequestJSONInto(t *testing.T) {
	client := NewClientWithDriver(staticDriver{
		response: httpdriver.NewMockResponse(http.StatusOK, nil, testPayload{Value: "new"}),
	})
	got := testPayload{Value: "old"}

	if err := client.RequestJSONInto(&got, http.MethodGet, "https://example.com"); err != nil {
		t.Fatal(err)
	}
	if got.Value != "new" {
		t.Fatalf("got %#v", got)
	}
}

func TestRequestJSONNoContent(t *testing.T) {
	client := NewClientWithDriver(staticDriver{
		response: httpdriver.NewMockResponse(http.StatusNoContent, nil, nil),
	})

	got, err := client.RequestJSON[int](http.MethodGet, "https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}
