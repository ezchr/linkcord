package udp

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
)

// ErrDecryptionFailed is returned from ReadPacket if the received packet fails
// to decrypt.
var ErrDecryptionFailed = errors.New("decryption failed")

// The encryption modes that a Connection can use.
//
// https://docs.discord.food/topics/voice-connections#transport-encryption-mode
const (
	ModeAES256GCM         = "aead_aes256_gcm_rtpsize"
	ModeXChaCha20Poly1305 = "aead_xchacha20_poly1305_rtpsize"
)

// Modes returns the encryption modes that a Connection can use, the most
// preferred first.
func Modes() []string {
	return []string{ModeAES256GCM, ModeXChaCha20Poly1305}
}

// defaultDialer is the default dialer that this package uses for all its
// dialing.
var defaultDialer = net.Dialer{
	Timeout: 30 * time.Second,
}

// Connection represents a voice connection. It is not thread-safe.
type Connection struct {
	GatewayIP   string
	GatewayPort uint16

	conn net.Conn
	ssrc uint32

	// frequency rate.Limiter
	frequency *time.Ticker
	timeIncr  uint32
	stopFreq  chan struct{}

	packet [12]byte
	aead   cipher.AEAD

	sequence  uint16
	timestamp uint32
	// written is when Write was last called.
	written time.Time
	// nonce is a counter in its first 4 bytes, which are sent after the packet.
	// It is as long as the longest that a mode needs.
	nonce [24]byte

	// recv fields
	recvNonce  [24]byte
	recvBuf    []byte  // len 1400
	recvOpus   []byte  // len 1400
	recvPacket *Packet // uses recvOpus' backing array

	// pinged is when the voice server was last pinged, and latency how long it
	// took to answer, both in nanoseconds.
	pinged, latency atomic.Int64

	closed sync.Once
}

// DialFunc is the UDP dialer function type. It's the function signature for
// udp.DialConnection.
type DialFunc = func(ctx context.Context, addr string, ssrc uint32) (*Connection, error)

// Assert that this is the same.
var _ DialFunc = DialConnection

// DialFuncWithFrequency creates a new DialFunc with the given frame duration
// and time increment. See Connection's ResetFrequency method for more
// information.
func DialFuncWithFrequency(frameDuration time.Duration, timeIncr uint32) DialFunc {
	return func(ctx context.Context, addr string, ssrc uint32) (*Connection, error) {
		u, err := DialConnection(ctx, addr, ssrc)
		if err != nil {
			return nil, err
		}
		u.ResetFrequency(frameDuration, timeIncr)
		return u, nil
	}
}

// DialConnection dials the UDP connection using the given address and SSRC
// number.
func DialConnection(ctx context.Context, addr string, ssrc uint32) (*Connection, error) {
	return DialConnectionCustom(ctx, &defaultDialer, addr, ssrc)
}

// DialConnectionCustom dials the UDP connection with a custom dialer.
func DialConnectionCustom(
	ctx context.Context, dialer *net.Dialer, addr string, ssrc uint32) (*Connection, error) {

	// Create a new UDP connection.
	conn, err := dialer.DialContext(ctx, "udp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed to dial host: %w", err)
	}

	// https://discord.com/developers/docs/topics/voice-connections#ip-discovery
	var ssrcBuffer [74]byte
	binary.BigEndian.PutUint16(ssrcBuffer[0:2], 1)
	binary.BigEndian.PutUint16(ssrcBuffer[2:4], 70)
	binary.BigEndian.PutUint32(ssrcBuffer[4:8], ssrc)

	_, err = conn.Write(ssrcBuffer[:])
	if err != nil {
		return nil, fmt.Errorf("failed to write SSRC buffer: %w", err)
	}

	var ipBuffer [74]byte

	// ReadFull makes sure to read all 74 bytes.
	_, err = io.ReadFull(conn, ipBuffer[:])
	if err != nil {
		return nil, fmt.Errorf("failed to read IP buffer: %w", err)
	}

	ipbody := ipBuffer[8:72]

	nullPos := bytes.Index(ipbody, []byte{'\x00'})
	if nullPos < 0 {
		return nil, errors.New("UDP IP discovery did not contain a null terminator")
	}

	ip := ipbody[:nullPos]
	port := binary.BigEndian.Uint16(ipBuffer[72:74])

	// https://discord.com/developers/docs/topics/voice-connections#encrypting-and-sending-voice
	packet := [12]byte{
		0: 0x80, // Version + Flags
		1: 0x78, // Payload Type
		// [2:4] // Sequence
		// [4:8] // Timestamp
	}

	// Write SSRC to the header.
	binary.BigEndian.PutUint32(packet[8:12], ssrc) // SSRC

	c := &Connection{
		GatewayIP:   string(ip),
		GatewayPort: port,
		frequency:   time.NewTicker(20 * time.Millisecond),
		timeIncr:    960,
		stopFreq:    make(chan struct{}),
		packet:      packet,
		ssrc:        ssrc,
		conn:        conn,
		recvBuf:     make([]byte, 1400),
		recvOpus:    make([]byte, 1400),
		recvPacket:  &Packet{},
		written:     time.Now(),
	}
	go c.keepAlive()
	return c, nil
}

// Latency returns how long the voice server took to answer the last ping that
// ReadPacket read the answer to, or 0 if it has read none.
func (c *Connection) Latency() time.Duration {
	return time.Duration(c.latency.Load())
}

// keepAlive pings the voice server every 5 seconds until the connection is
// closed, so that what is between them keeps passing packets on when nothing
// is written.
//
// https://docs.discord.food/topics/voice-connections#udp-ping
func (c *Connection) keepAlive() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	ping := []byte{0x13, 0x37, 0xCA, 0xFE, 0, 0, 0, 0}
	for seq := uint32(0); ; seq++ {
		binary.BigEndian.PutUint32(ping[4:], seq)
		c.pinged.Store(time.Now().UnixNano())
		if _, err := c.conn.Write(ping); err != nil {
			return
		}

		select {
		case <-ticker.C:
		case <-c.stopFreq:
			return
		}
	}
}

// ResetFrequency resets the internal frequency ticker as well as the timestamp
// incremental number. For more information, refer to
// https://tools.ietf.org/html/rfc7587#section-4.2.
//
// frameDuration controls the Opus frame duration used by the UDP connection to
// control the frequency of packets sent over. 20ms is the default by libopus.
//
// timestampIncr is the timestamp to increment for each Opus packet. This should
// be consistent with th given frameDuration. For the right combination, refer
// to the Valid Parameters section below.
//
// # Valid Parameters
//
// The following table lists the recommended parameters for these variables.
//
//	+---------+-----+-----+------+------+
//	|   Mode  |  10 |  20 |  40  |  60  |
//	+---------+-----+-----+------+------+
//	| ts incr | 480 | 960 | 1920 | 2880 |
//	+---------+-----+-----+------+------+
//
// Note that audio mode is omitted, as it is not recommended. For the full
// table, refer to the IETF RFC7587 section 4.2 link above.
func (c *Connection) ResetFrequency(frameDuration time.Duration, timeIncr uint32) {
	c.frequency.Stop()
	c.frequency = time.NewTicker(frameDuration)
	c.timeIncr = timeIncr
}

// UseSecret encrypts with the given secret in the given mode, which must be
// one of those that Modes returns. This method is not thread-safe, so it should only be used right
// after initialization.
func (c *Connection) UseSecret(mode string, secret [32]byte) (err error) {
	switch mode {
	case ModeAES256GCM:
		var block cipher.Block
		if block, err = aes.NewCipher(secret[:]); err == nil {
			c.aead, err = cipher.NewGCM(block)
		}
	case ModeXChaCha20Poly1305:
		c.aead, err = chacha20poly1305.NewX(secret[:])
	default:
		err = fmt.Errorf("unknown encryption mode %q", mode)
	}
	return err
}

// SetWriteDeadline sets the UDP connection's write deadline.
func (c *Connection) SetWriteDeadline(deadline time.Time) {
	c.conn.SetWriteDeadline(deadline)
}

// SetReadDeadline sets the UDP connection's read deadline.
func (c *Connection) SetReadDeadline(deadline time.Time) {
	c.conn.SetReadDeadline(deadline)
}

// Close closes the connection.
func (c *Connection) Close() error {
	c.closed.Do(func() {
		// Be sure to only run this ONCE.
		c.frequency.Stop()
		close(c.stopFreq)
	})

	return c.conn.Close()
}

// Write sends a packet of audio into the voice UDP connection. It is made to be
// stream-compatible: the internal frequency clock will slow Write down to match
// the real playback time.
func (c *Connection) Write(b []byte) (int, error) {
	// Write a new sequence.
	binary.BigEndian.PutUint16(c.packet[2:4], c.sequence)
	c.sequence++

	// A pause in what is written is one in its timestamps too, at 48kHz, for
	// what is written after it not to seem to arrive late.
	now := time.Now()
	if pause := uint32(now.Sub(c.written) * 48000 / time.Second); pause > 5*c.timeIncr {
		c.timestamp += pause - pause%c.timeIncr
	}
	c.written = now

	binary.BigEndian.PutUint32(c.packet[4:8], c.timestamp)
	c.timestamp += c.timeIncr

	binary.BigEndian.PutUint32(c.nonce[:4], binary.BigEndian.Uint32(c.nonce[:4])+1)

	// Seal the message after the packet's header, which is authenticated but
	// not encrypted, and send the nonce's counter after it.
	toSend := c.aead.Seal(c.packet[:12], c.nonce[:c.aead.NonceSize()], b, c.packet[:12])
	toSend = append(toSend, c.nonce[:4]...)

	select {
	case <-c.frequency.C:
		// ok
	case <-c.stopFreq:
		return 0, fmt.Errorf("frequency ticker stopped: %w", net.ErrClosed)
	}

	_, err := c.conn.Write(toSend)
	if err != nil {
		return 0, err
	}

	return len(b), nil
}

// Packet represents a voice packet.
type Packet struct {
	header []byte
	Opus   []byte
}

// VersionFlags returns the version flags of the current packet.
func (p *Packet) VersionFlags() byte { return p.header[0] }

// Type returns the packet type.
func (p *Packet) Type() byte { return p.header[1] }

// Sequence returns the packet sequence.
func (p *Packet) Sequence() uint16 { return binary.BigEndian.Uint16(p.header[2:4]) }

// Timestamp returns the packet's timestamp.
func (p *Packet) Timestamp() uint32 { return binary.BigEndian.Uint32(p.header[4:8]) }

// SSRC returns the packet's SSRC number.
func (p *Packet) SSRC() uint32 { return binary.BigEndian.Uint32(p.header[8:12]) }

// Copy copies the current packet into the given packet.
func (p *Packet) Copy(dst *Packet) {
	dst.header = append(dst.header[:0], p.header...)
	dst.Opus = append(dst.Opus[:0], p.Opus...)
}

const packetHeaderSize = 12

// ReadPacket reads the UDP connection and returns a packet if successful. The
// returned packet is invalidated once ReadPacket is called again. To avoid
// this, manually Copy the packet.
func (c *Connection) ReadPacket() (*Packet, error) {
	if c.recvPacket.header == nil {
		// Initialize the recvPacket's header.
		c.recvPacket.header = c.recvBuf[:12]
	}

	for {
		i, err := c.conn.Read(c.recvBuf)
		if err != nil {
			return nil, err
		}

		// The answer to a ping.
		if i == 8 && binary.BigEndian.Uint32(c.recvBuf) == 0x1337F00D {
			c.latency.Store(time.Now().UnixNano() - c.pinged.Load())
			continue
		}

		// Skip anything that is not RTP version 2 carrying Opus, such as RTCP.
		if i < packetHeaderSize || c.recvBuf[0]>>6 != 2 || c.recvBuf[1]&0x7F != 0x78 {
			continue
		}

		// The header that is authenticated but not encrypted also covers the
		// CSRCs and the first 4 bytes of the extension, if any. The rest of the
		// extension is encrypted along with the Opus data.
		//
		// https://tools.ietf.org/html/rfc3550#section-5.1
		header := packetHeaderSize + 4*int(c.recvBuf[0]&0x0F)
		extension := 0
		if c.recvBuf[0]&0x10 != 0 && i >= header+4 {
			extension = 4 * int(binary.BigEndian.Uint16(c.recvBuf[header+2:header+4]))
			header += 4
		}
		if i < header+4 {
			continue
		}

		// The last 4 bytes are the nonce's counter.
		copy(c.recvNonce[:4], c.recvBuf[i-4:i])

		opus, err := c.aead.Open(c.recvOpus[:0], c.recvNonce[:c.aead.NonceSize()], c.recvBuf[header:i-4], c.recvBuf[:header])
		if err != nil {
			return nil, ErrDecryptionFailed
		}
		if len(opus) < extension {
			continue
		}
		opus = opus[extension:]

		// The padding bit means the last byte is the number of bytes of padding.
		if c.recvBuf[0]&0x20 != 0 && len(opus) > 0 && int(opus[len(opus)-1]) <= len(opus) {
			opus = opus[:len(opus)-int(opus[len(opus)-1])]
		}
		// A packet of only padding carries nothing.
		if len(opus) == 0 {
			continue
		}
		c.recvPacket.Opus = opus

		return c.recvPacket, nil
	}
}
