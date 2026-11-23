package mesh

import (
	"bufio"
	"errors"
	"io"
)

// The stream API frames each protobuf with 0x94 0xC3 and a big-endian
// 16-bit length. Bytes outside frames (a serial port also carries the
// radio's debug log) are skipped.
const (
	start1   = 0x94
	start2   = 0xC3
	maxFrame = 512
)

func frame(payload []byte) []byte {
	out := make([]byte, 0, len(payload)+4)
	out = append(out, start1, start2, byte(len(payload)>>8), byte(len(payload)))
	return append(out, payload...)
}

// readFrame returns the next frame's payload.
func readFrame(r *bufio.Reader) ([]byte, error) {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != start1 {
			continue
		}
		b, err = r.ReadByte()
		if err != nil {
			return nil, err
		}
		if b != start2 {
			if b == start1 {
				_ = r.UnreadByte()
			}
			continue
		}
		hi, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		lo, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		n := int(hi)<<8 | int(lo)
		if n > maxFrame {
			// Not a real frame (debug text that happened to match): resync.
			continue
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		return buf, nil
	}
}
