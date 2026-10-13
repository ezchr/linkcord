package voicegateway

import (
	"encoding/binary"
	"io"
	"strconv"

	"github.com/ayn2op/arikawa/v3/discord"
	"github.com/ayn2op/arikawa/v3/utils/ws"
)

//go:generate go run ../../utils/cmd/genevent -p voicegateway -o event_methods.go

// OpUnmarshalers contains the Op unmarshalers for the voice gateway events.
var OpUnmarshalers = ws.NewOpUnmarshalers()

// IdentifyCommand is a command for Op 0.
//
// https://discord.com/developers/docs/topics/voice-connections#establishing-a-voice-websocket-connection-example-voice-identify-payload
type IdentifyCommand struct {
	// GuildID is the channel ID for a private channel.
	GuildID   discord.GuildID   `json:"server_id"` // yes, this should be "server_id"
	ChannelID discord.ChannelID `json:"channel_id"`
	UserID    discord.UserID    `json:"user_id"`
	SessionID string            `json:"session_id"`
	Token     string            `json:"token"`
	// MaxDAVEProtocolVersion is the highest DAVE protocol version supported, 0 being none.
	MaxDAVEProtocolVersion int `json:"max_dave_protocol_version"`
}

// SelectProtocolCommand is a command for Op 1.
//
// https://discord.com/developers/docs/topics/voice-connections#establishing-a-voice-udp-connection-example-select-protocol-payload
type SelectProtocolCommand struct {
	Protocol string             `json:"protocol"`
	Data     SelectProtocolData `json:"data"`
}

// SelectProtocolData is the data inside a SelectProtocolCommand.
type SelectProtocolData struct {
	Address string `json:"address"`
	Port    uint16 `json:"port"`
	Mode    string `json:"mode"`
}

// ReadyEvent is an event for Op 2.
//
// https://discord.com/developers/docs/topics/voice-connections#establishing-a-voice-websocket-connection-example-voice-ready-payload
type ReadyEvent struct {
	SSRC        uint32   `json:"ssrc"`
	IP          string   `json:"ip"`
	Port        int      `json:"port"`
	Modes       []string `json:"modes"`
	Experiments []string `json:"experiments"`

	// From Discord's API Docs:
	//
	// `heartbeat_interval` here is an erroneous field and should be ignored.
	// The correct `heartbeat_interval` value comes from the Hello payload.

	// HeartbeatInterval discord.Milliseconds `json:"heartbeat_interval"`
}

// Addr formats the URL inside Ready to be of format "host:port".
func (r ReadyEvent) Addr() string {
	return r.IP + ":" + strconv.Itoa(r.Port)
}

// HeartbeatCommand is a command for Op 3.
//
// https://discord.com/developers/docs/topics/voice-connections#heartbeating-example-heartbeat-payload
type HeartbeatCommand struct {
	// T is a nonce that the gateway sends back in HeartbeatAckEvent.
	T int64 `json:"t"`
	// SeqAck is the sequence number of the last event received.
	SeqAck int64 `json:"seq_ack,omitempty"`
}

// SessionDescriptionEvent is an event for Op 4.
//
// https://discord.com/developers/docs/topics/voice-connections#establishing-a-voice-udp-connection-example-session-description-payload
type SessionDescriptionEvent struct {
	Mode      string   `json:"mode"`
	SecretKey [32]byte `json:"secret_key"`
	// DAVEProtocolVersion is the DAVE protocol version to use, 0 being none.
	DAVEProtocolVersion uint16 `json:"dave_protocol_version"`
}

// https://discord.com/developers/docs/topics/voice-connections#speaking
type SpeakingFlag uint64

const NotSpeaking SpeakingFlag = 0

const (
	Microphone SpeakingFlag = 1 << iota
	Soundshare
	Priority
)

// SpeakingEvent is an event for Op 5. It is also a command.
//
// https://discord.com/developers/docs/topics/voice-connections#speaking-example-speaking-payload
type SpeakingEvent struct {
	Speaking SpeakingFlag   `json:"speaking"`
	Delay    int            `json:"delay"`
	SSRC     uint32         `json:"ssrc"`
	UserID   discord.UserID `json:"user_id,omitempty"`
}

// HeartbeatAckEvent is an event for Op 6.
//
// https://discord.com/developers/docs/topics/voice-connections#heartbeating-example-heartbeat-ack-payload
type HeartbeatAckEvent struct {
	T int64 `json:"t"`
}

// ResumeCommand is a command for Op 7.
//
// https://discord.com/developers/docs/topics/voice-connections#resuming-voice-connection-example-resume-connection-payload
type ResumeCommand struct {
	// GuildID is the channel ID for a private channel.
	GuildID   discord.GuildID   `json:"server_id"` // yes, this should be "server_id"
	ChannelID discord.ChannelID `json:"channel_id"`
	SessionID string            `json:"session_id"`
	Token     string            `json:"token"`
	// SeqAck is the sequence number of the last event received.
	SeqAck int64 `json:"seq_ack,omitempty"`
}

// HelloEvent is an event for Op 8.
//
// https://discord.com/developers/docs/topics/voice-connections#heartbeating-example-hello-payload-since-v3
type HelloEvent struct {
	HeartbeatInterval discord.Milliseconds `json:"heartbeat_interval"`
}

// ResumedEvent is an event for Op 9.
// https://discord.com/developers/docs/topics/voice-connections#resuming-voice-connection-example-resumed-payload
type ResumedEvent struct{}

// ClientsConnectEvent is an event for Op 11. It is sent after connecting for
// the users already in the channel and whenever users connect.
type ClientsConnectEvent struct {
	UserIDs []discord.UserID `json:"user_ids"`
}

// ClientDisconnectEvent is an event for Op 13.
type ClientDisconnectEvent struct {
	UserID discord.UserID `json:"user_id"`
}

// DAVEPrepareTransitionEvent is an event for Op 21. It announces a downgrade
// from the DAVE protocol.
//
// https://daveprotocol.com/#voice-gateway-opcodes
type DAVEPrepareTransitionEvent struct {
	ProtocolVersion uint16 `json:"protocol_version"`
	TransitionID    uint16 `json:"transition_id"`
}

// DAVEExecuteTransitionEvent is an event for Op 22.
type DAVEExecuteTransitionEvent struct {
	TransitionID uint16 `json:"transition_id"`
}

// DAVETransitionReadyCommand is a command for Op 23.
type DAVETransitionReadyCommand struct {
	TransitionID uint16 `json:"transition_id"`
}

// DAVEPrepareEpochEvent is an event for Op 24. It announces a change of the
// DAVE protocol version or of the MLS group.
type DAVEPrepareEpochEvent struct {
	ProtocolVersion uint16 `json:"protocol_version"`
	Epoch           int    `json:"epoch"`
}

// MLSExternalSenderEvent is an event for Op 25. It is a binary message.
type MLSExternalSenderEvent []byte

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (e *MLSExternalSenderEvent) UnmarshalBinary(b []byte) error {
	*e = b
	return nil
}

// MLSKeyPackageCommand is a command for Op 26. It is a binary message.
type MLSKeyPackageCommand []byte

// MarshalBinary implements encoding.BinaryMarshaler.
func (c *MLSKeyPackageCommand) MarshalBinary() ([]byte, error) {
	return *c, nil
}

// MLSProposalsEvent is an event for Op 27. It is a binary message.
type MLSProposalsEvent []byte

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (e *MLSProposalsEvent) UnmarshalBinary(b []byte) error {
	*e = b
	return nil
}

// MLSCommitWelcomeCommand is a command for Op 28. It is a binary message of a
// commit and an optional welcome.
type MLSCommitWelcomeCommand []byte

// MarshalBinary implements encoding.BinaryMarshaler.
func (c *MLSCommitWelcomeCommand) MarshalBinary() ([]byte, error) {
	return *c, nil
}

// MLSAnnounceCommitEvent is an event for Op 29. It is a binary message.
type MLSAnnounceCommitEvent struct {
	TransitionID uint16
	Commit       []byte
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (e *MLSAnnounceCommitEvent) UnmarshalBinary(b []byte) error {
	if len(b) < 2 {
		return io.ErrUnexpectedEOF
	}
	e.TransitionID, e.Commit = binary.BigEndian.Uint16(b), b[2:]
	return nil
}

// MLSWelcomeEvent is an event for Op 30. It is a binary message.
type MLSWelcomeEvent struct {
	TransitionID uint16
	Welcome      []byte
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (e *MLSWelcomeEvent) UnmarshalBinary(b []byte) error {
	if len(b) < 2 {
		return io.ErrUnexpectedEOF
	}
	e.TransitionID, e.Welcome = binary.BigEndian.Uint16(b), b[2:]
	return nil
}

// MLSInvalidCommitWelcomeCommand is a command for Op 31.
type MLSInvalidCommitWelcomeCommand struct {
	TransitionID uint16 `json:"transition_id"`
}
