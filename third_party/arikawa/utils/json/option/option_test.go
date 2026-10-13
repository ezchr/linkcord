package option

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/utils/json"
)

func TestNullable(t *testing.T) {
	type value struct {
		Option   Option[int]    `json:"option,omitempty"`
		Nullable *Nullable[int] `json:"nullable,omitempty"`
	}

	for _, test := range []struct {
		value value
		json  string
	}{
		{value{}, `{}`},
		{value{Option: Some(0)}, `{"option":0}`},
		{value{Nullable: &Nullable[int]{}}, `{"nullable":null}`},
		{value{Nullable: SomeNullable(42)}, `{"nullable":42}`},
	} {
		got, err := json.Marshal(test.value)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != test.json {
			t.Fatalf("got %s, want %s", got, test.json)
		}
	}
}
