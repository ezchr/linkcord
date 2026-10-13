// Package zlib provides abstractions on top of compress/zlib to work with
// Discord's method of compressing websocket packets.
package zlib

import (
	"bytes"
	"fmt"
)

var Suffix = [4]byte{'\x00', '\x00', '\xff', '\xff'}

type Inflator struct {
	zlib Reader
	wbuf bytes.Buffer // write buffer for writing compressed bytes
	rbuf bytes.Buffer // read buffer for writing uncompressed bytes
}

func NewInflator() *Inflator {
	return &Inflator{
		wbuf: bytes.Buffer{},
		rbuf: bytes.Buffer{},
	}
}

func (i *Inflator) Write(p []byte) (n int, err error) {
	return i.wbuf.Write(p)
}

// CanFlush returns if Flush() should be called.
func (i *Inflator) CanFlush() bool {
	if i.wbuf.Len() < 4 {
		return false
	}
	p := i.wbuf.Bytes()
	return bytes.Equal(p[len(p)-4:], Suffix[:])
}

func (i *Inflator) Flush() ([]byte, error) {
	// We can reset the read buffer while returning its byte slice. This works
	// as long as we copy the byte slice before resetting.
	defer i.rbuf.Reset()

	// Guarantee there's a zlib writer. Since Discord streams zlib, we have to
	// reuse the same Reader. Only the first packet has the zlib header.
	if i.zlib == nil {
		r, err := zlibStreamer(&i.wbuf)
		if err != nil {
			return nil, fmt.Errorf("failed to make a FLATE reader: %w", err)
		}
		i.zlib = r
	}

	// We can ignore zlib.Read's error, as zlib.Close would return them.
	_, err := i.rbuf.ReadFrom(i.zlib)

	if err != nil {
		return nil, fmt.Errorf("failed to read from FLATE reader: %w", err)
	}

	return bytes.Clone(i.rbuf.Bytes()), nil
}
