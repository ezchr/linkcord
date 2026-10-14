package discord

// Added by Linkcord: polls, which Discord sends with messages and Arikawa
// dropped.
//
// https://discord.com/developers/docs/resources/poll

// Poll is a question with answers that people vote for.
type Poll struct {
	Question PollMedia    `json:"question"`
	Answers  []PollAnswer `json:"answers"`
	// Expiry is when voting ends; zero for a poll that never does.
	Expiry           Timestamp    `json:"expiry"`
	AllowMultiselect bool         `json:"allow_multiselect"`
	LayoutType       int          `json:"layout_type"`
	Results          *PollResults `json:"results,omitempty"`
}

// PollMedia is the text, and the emoji, of a question or an answer.
type PollMedia struct {
	Text  string `json:"text,omitempty"`
	Emoji *Emoji `json:"emoji,omitempty"`
}

// PollAnswer is an answer of a poll.
type PollAnswer struct {
	AnswerID  int       `json:"answer_id"`
	PollMedia PollMedia `json:"poll_media"`
}

// PollResults are the votes of a poll so far, final once it ended.
type PollResults struct {
	IsFinalized  bool              `json:"is_finalized"`
	AnswerCounts []PollAnswerCount `json:"answer_counts"`
}

// PollAnswerCount is how many voted for an answer, and whether the user did.
type PollAnswerCount struct {
	ID      int  `json:"id"`
	Count   int  `json:"count"`
	MeVoted bool `json:"me_voted"`
}
