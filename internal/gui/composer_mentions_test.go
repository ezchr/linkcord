package gui

import "testing"

func TestMentionRanges(t *testing.T) {
	got := mentionRanges("hi @Ana and @Anabel, é @Ana", map[string]string{"@Ana": "<@1>"})
	if len(got) != 2 || got[0] != [2]int{3, 7} || got[1] != [2]int{23, 27} {
		t.Errorf("got %v", got)
	}
}
