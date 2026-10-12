package forge

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

type flacPCM struct {
	Rate     int
	Channels int
	Bits     int
	Samples  int64
	MD5      [16]byte
}

// Only a nonzero STREAMINFO checksum and a known byte-aligned sample format
// permit error-tolerant decoding. Other streams keep the strict decoder.
func readFLACPCM(path string) (*flacPCM, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var header [42]byte
	if _, err = io.ReadFull(f, header[:]); err != nil {
		return nil, fmt.Errorf("read FLAC STREAMINFO: %w", err)
	}
	if string(header[:4]) != "fLaC" || header[4]&0x7f != 0 || header[5] != 0 || header[6] != 0 || header[7] != 34 {
		return nil, errors.New("invalid FLAC STREAMINFO header")
	}
	packed := binary.BigEndian.Uint64(header[18:26])
	p := &flacPCM{
		Rate: int(packed >> 44), Channels: int(packed>>41&7) + 1,
		Bits: int(packed>>36&31) + 1, Samples: int64(packed & ((1 << 36) - 1)),
	}
	copy(p.MD5[:], header[26:42])
	if p.Rate == 0 || p.Samples == 0 || p.MD5 == [16]byte{} {
		return nil, nil
	}
	switch p.Bits {
	case 8, 16, 24, 32:
		return p, nil
	default:
		return nil, nil
	}
}

func (p *flacPCM) byteCount() int64 {
	return p.Samples * int64(p.Channels) * int64(p.Bits/8)
}

func (p *flacPCM) inputArgs() []string {
	format := "s" + strconv.Itoa(p.Bits) + "le"
	if p.Bits == 8 {
		format = "s8"
	}
	// Raw PCM has no speaker labels. Preserve FLAC's specified channel order.
	layouts := []string{"mono", "stereo", "3.0", "quad", "5.0", "5.1", "6.1", "7.1"}
	return []string{"-f", format, "-ar", strconv.Itoa(p.Rate), "-ac", strconv.Itoa(p.Channels), "-channel_layout", layouts[p.Channels-1]}
}

func (p *flacPCM) copyVerified(dst io.Writer, src io.Reader) error {
	// MD5 is the checksum defined by FLAC for signed, little-endian PCM.
	h := md5.New()
	expected := p.byteCount()
	n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(src, expected))
	if err != nil {
		return fmt.Errorf("copy FLAC PCM: %w", err)
	}
	if n != expected {
		return fmt.Errorf("FLAC PCM sample count mismatch: expected %d bytes (%d samples), got %d bytes", expected, p.Samples, n)
	}
	if !bytes.Equal(h.Sum(nil), p.MD5[:]) {
		return fmt.Errorf("FLAC PCM MD5 mismatch: expected %x, got %x", p.MD5, h.Sum(nil))
	}
	// The decoder is instructed to stop at Samples. Check that boundary without
	// forwarding a probe byte to ffmpeg as an incomplete PCM sample.
	var extra [1]byte
	if n, err := io.ReadFull(src, extra[:]); n != 0 {
		return fmt.Errorf("FLAC PCM sample count mismatch: decoder emitted data beyond the declared %d bytes (%d samples)", expected, p.Samples)
	} else if err != io.EOF {
		return fmt.Errorf("read FLAC PCM boundary: %w", err)
	}
	return nil
}
