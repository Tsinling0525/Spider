package device

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
)

// Ogg packet framing is a transport adapter only; all speech inference is delegated.
func crcOgg(b []byte) uint32 {
	var c uint32
	for _, v := range b {
		c ^= uint32(v) << 24
		for i := 0; i < 8; i++ {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
	}
	return c
}
func oggPage(packet []byte, seq uint32, granule uint64, flags byte) []byte {
	n := len(packet)/255 + 1
	p := make([]byte, 27+n)
	copy(p, "OggS")
	p[5] = flags
	binary.LittleEndian.PutUint64(p[6:], granule)
	binary.LittleEndian.PutUint32(p[14:], 1)
	binary.LittleEndian.PutUint32(p[18:], seq)
	p[26] = byte(n)
	for i := 0; i < n-1; i++ {
		p[27+i] = 255
	}
	p[27+n-1] = byte(len(packet) % 255)
	p = append(p, packet...)
	binary.LittleEndian.PutUint32(p[22:], crcOgg(p))
	return p
}
func opusOgg(packets [][]byte) []byte {
	head := []byte{'O', 'p', 'u', 's', 'H', 'e', 'a', 'd', 1, 1, 0, 0, 0x80, 0x3e, 0, 0, 0, 0, 0}
	tags := append([]byte("OpusTags"), make([]byte, 8)...)
	out := oggPage(head, 0, 0, 2)
	out = append(out, oggPage(tags, 1, 0, 0)...)
	for i, p := range packets {
		var flags byte
		if i == len(packets)-1 {
			flags = 4
		}
		out = append(out, oggPage(p, uint32(i+2), uint64(i+1)*2880, flags)...)
	}
	return out
}
func oggPackets(data []byte) ([][]byte, error) {
	var packets [][]byte
	var partial []byte
	for len(data) > 0 {
		if len(data) < 27 || string(data[:4]) != "OggS" {
			return nil, errors.New("invalid Ogg")
		}
		n := int(data[26])
		if len(data) < 27+n {
			return nil, errors.New("short Ogg table")
		}
		pos := 27 + n
		for _, l := range data[27:pos] {
			end := pos + int(l)
			if end > len(data) {
				return nil, errors.New("short Ogg body")
			}
			partial = append(partial, data[pos:end]...)
			pos = end
			if len(partial) > 65536 {
				return nil, errors.New("large Ogg packet")
			}
			if l < 255 {
				if !bytes.HasPrefix(partial, []byte("OpusHead")) && !bytes.HasPrefix(partial, []byte("OpusTags")) {
					packets = append(packets, partial)
				}
				partial = nil
			}
		}
		data = data[pos:]
	}
	if len(partial) > 0 {
		return nil, errors.New("unfinished Ogg packet")
	}
	return packets, nil
}
func ffmpeg(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	all := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-protocol_whitelist", "pipe", "-i", "pipe:0"}
	all = append(all, args...)
	all = append(all, "pipe:1")
	cmd := exec.CommandContext(ctx, "ffmpeg", all...)
	cmd.Stdin = bytes.NewReader(input)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("audio conversion: %w", err)
	}
	if out.Len() > 8<<20 {
		return nil, errors.New("audio exceeds limit")
	}
	return out.Bytes(), nil
}
func decodeOpus(ctx context.Context, packets [][]byte) ([]byte, error) {
	return ffmpeg(ctx, opusOgg(packets), "-t", "20", "-ac", "1", "-ar", "16000", "-f", "wav")
}
func encodeOpus(ctx context.Context, audio []byte) ([][]byte, error) {
	ogg, err := ffmpeg(ctx, audio, "-t", "60", "-ac", "1", "-ar", "16000", "-c:a", "libopus", "-b:a", "24k", "-frame_duration", "60", "-f", "ogg")
	if err != nil {
		return nil, err
	}
	return oggPackets(ogg)
}
