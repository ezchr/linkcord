package discord

import (
	"testing"

	"github.com/ayn2op/arikawa/v3/utils/json"
)

func TestSelectLimitsRead(t *testing.T) {
	var c TopLevelComponents
	err := json.Unmarshal([]byte(`[{"type":1,"components":[{"type":3,"custom_id":"a","min_values":0,"max_values":3,"options":[{"label":"x","value":"x"}]}]},
		{"type":1,"components":[{"type":8,"custom_id":"b","channel_types":[0],"default_values":[{"id":"5","type":"channel"}]}]}]`), &c)
	if err != nil {
		t.Fatal(err)
	}
	s := (*c[0].(*ActionRowComponent))[0].(*StringSelectComponent)
	if s.ValueLimits != [2]int{0, 3} || len(s.Options) != 1 {
		t.Fatalf("string select: %+v", s)
	}
	ch := (*c[1].(*ActionRowComponent))[0].(*ChannelSelectComponent)
	if len(ch.DefaultChannels) != 1 || ch.DefaultChannels[0] != 5 || len(ch.ChannelTypes) != 1 {
		t.Fatalf("channel select: %+v", ch)
	}
}
