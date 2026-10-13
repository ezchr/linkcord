package gui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/draw"
	"image/png"
	"time"
)

const pngSignature = "\x89PNG\r\n\x1a\n"

func isAPNG(data []byte) bool {
	if !bytes.HasPrefix(data, []byte(pngSignature)) {
		return false
	}
	for pos := 8; pos+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		if n > len(data)-pos-12 {
			return false
		}
		switch string(data[pos+4 : pos+8]) {
		case "acTL":
			return true
		case "IDAT", "IEND":
			return false
		}
		pos += n + 12
	}
	return false
}

func decodeAnimation(data []byte, size int) ([]image.Image, []time.Duration, error) {
	if isGIF(data) {
		return decodeGIF(data, size)
	}
	if isAPNG(data) {
		return decodeAPNG(data, size)
	}
	return nil, nil, errors.New("not an animation")
}

type apngFrame struct {
	rect           image.Rectangle
	delay          time.Duration
	dispose, blend byte
	compressed     [][]byte
}

func appendPNGChunk(buf *bytes.Buffer, kind string, data []byte) {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(data)))
	buf.Write(n[:])
	buf.WriteString(kind)
	buf.Write(data)
	h := crc32.NewIEEE()
	h.Write([]byte(kind))
	h.Write(data)
	binary.BigEndian.PutUint32(n[:], h.Sum32())
	buf.Write(n[:])
}

// decodeAPNG rebuilds each subframe as a PNG, then composites it on the
// transparent canvas. The default PNG poster need not be animation frame 0.
// https://www.w3.org/TR/png-3/#11fcTL
func decodeAPNG(data []byte, size int) ([]image.Image, []time.Duration, error) {
	if !bytes.HasPrefix(data, []byte(pngSignature)) || size <= 0 {
		return nil, nil, errors.New("invalid APNG")
	}
	var header []byte
	var shared bytes.Buffer
	var frames []apngFrame
	var current *apngFrame
	var bounds image.Rectangle
	var count uint32
	var sequence uint32
	seenData, ended := false, false
	finish := func() error {
		if current == nil {
			return nil
		}
		if len(current.compressed) == 0 {
			return errors.New("APNG frame has no image data")
		}
		frames = append(frames, *current)
		current = nil
		return nil
	}
	for pos := 8; pos < len(data); {
		if len(data)-pos < 12 {
			return nil, nil, errors.New("truncated APNG chunk")
		}
		n := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		if n > len(data)-pos-12 {
			return nil, nil, errors.New("truncated APNG data")
		}
		kind := string(data[pos+4 : pos+8])
		payload := data[pos+8 : pos+8+n]
		crc := binary.BigEndian.Uint32(data[pos+8+n : pos+12+n])
		if crc32.ChecksumIEEE(data[pos+4:pos+8+n]) != crc {
			return nil, nil, fmt.Errorf("invalid APNG %s checksum", kind)
		}
		pos += n + 12
		switch kind {
		case "IHDR":
			if header != nil || n != 13 {
				return nil, nil, errors.New("invalid APNG header")
			}
			w, h := binary.BigEndian.Uint32(payload[:4]), binary.BigEndian.Uint32(payload[4:8])
			if w == 0 || h == 0 || w > 4096 || h > 4096 || uint64(w)*uint64(h)*4 > animBudget {
				return nil, nil, errors.New("APNG canvas exceeds memory limit")
			}
			header = payload
			bounds = image.Rect(0, 0, int(w), int(h))
		case "acTL":
			if header == nil || count != 0 || seenData || n != 8 {
				return nil, nil, errors.New("invalid APNG animation control")
			}
			count = binary.BigEndian.Uint32(payload[:4])
			if count == 0 || count > 2048 {
				return nil, nil, errors.New("invalid APNG frame count")
			}
		case "PLTE", "tRNS":
			if seenData {
				return nil, nil, errors.New("late APNG palette")
			}
			appendPNGChunk(&shared, kind, payload)
		case "fcTL":
			if header == nil || n != 26 || binary.BigEndian.Uint32(payload[:4]) != sequence {
				return nil, nil, errors.New("invalid APNG frame control")
			}
			sequence++
			if err := finish(); err != nil {
				return nil, nil, err
			}
			if count != 0 && len(frames) >= int(count) {
				return nil, nil, errors.New("too many APNG frames")
			}
			w, h := binary.BigEndian.Uint32(payload[4:8]), binary.BigEndian.Uint32(payload[8:12])
			x, y := binary.BigEndian.Uint32(payload[12:16]), binary.BigEndian.Uint32(payload[16:20])
			if w == 0 || h == 0 || uint64(x)+uint64(w) > uint64(bounds.Dx()) || uint64(y)+uint64(h) > uint64(bounds.Dy()) || payload[24] > 2 || payload[25] > 1 {
				return nil, nil, errors.New("invalid APNG frame bounds or operation")
			}
			if !seenData && (x != 0 || y != 0 || int(w) != bounds.Dx() || int(h) != bounds.Dy()) {
				return nil, nil, errors.New("APNG default frame must fill canvas")
			}
			num, den := binary.BigEndian.Uint16(payload[20:22]), binary.BigEndian.Uint16(payload[22:24])
			if den == 0 {
				den = 100
			}
			delay := time.Duration(num) * time.Second / time.Duration(den)
			if delay <= 0 {
				delay = 100 * time.Millisecond
			} else if delay < 10*time.Millisecond {
				delay = 10 * time.Millisecond
			}
			current = &apngFrame{
				rect:  image.Rect(int(x), int(y), int(x+w), int(y+h)),
				delay: delay, dispose: payload[24], blend: payload[25],
			}
		case "IDAT":
			if header == nil {
				return nil, nil, errors.New("APNG data before header")
			}
			seenData = true
			if current != nil {
				current.compressed = append(current.compressed, payload)
			}
		case "fdAT":
			if !seenData || current == nil || n < 4 || binary.BigEndian.Uint32(payload[:4]) != sequence {
				return nil, nil, errors.New("invalid APNG frame data")
			}
			sequence++
			current.compressed = append(current.compressed, payload[4:])
		case "IEND":
			if n != 0 {
				return nil, nil, errors.New("invalid APNG end")
			}
			if err := finish(); err != nil {
				return nil, nil, err
			}
			ended = true
		}
		if ended {
			break
		}
	}
	if !ended || count == 0 || len(frames) != int(count) {
		return nil, nil, errors.New("incomplete APNG animation")
	}
	sw, sh := fitSize(bounds.Dx(), bounds.Dy(), size)
	keep := max(1, min(len(frames), animBudget/max(sw*sh*4, 1)))
	step := (len(frames) + keep - 1) / keep
	canvas := image.NewRGBA(bounds)
	var result []image.Image
	var delays []time.Duration
	for i, f := range frames {
		var buf bytes.Buffer
		buf.WriteString(pngSignature)
		ihdr := bytes.Clone(header)
		binary.BigEndian.PutUint32(ihdr[:4], uint32(f.rect.Dx()))
		binary.BigEndian.PutUint32(ihdr[4:8], uint32(f.rect.Dy()))
		appendPNGChunk(&buf, "IHDR", ihdr)
		buf.Write(shared.Bytes())
		for _, compressed := range f.compressed {
			appendPNGChunk(&buf, "IDAT", compressed)
		}
		appendPNGChunk(&buf, "IEND", nil)
		src, err := png.Decode(&buf)
		if err != nil {
			return nil, nil, err
		}
		var previous *image.RGBA
		if f.dispose == 2 {
			previous = image.NewRGBA(bounds)
			draw.Draw(previous, bounds, canvas, image.Point{}, draw.Src)
		}
		mode := draw.Src
		if f.blend == 1 {
			mode = draw.Over
		}
		draw.Draw(canvas, f.rect, src, src.Bounds().Min, mode)
		if i%step == 0 {
			result = append(result, scaleCopy(canvas, size))
			delays = append(delays, f.delay)
		} else {
			delays[len(delays)-1] += f.delay
		}
		switch f.dispose {
		case 1:
			draw.Draw(canvas, f.rect, image.Transparent, image.Point{}, draw.Src)
		case 2:
			canvas = previous
		}
	}
	return result, delays, nil
}
