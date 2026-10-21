package forge

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestFLACPCMStreamInfoAndRawFormats(t *testing.T) {
	for _, tc := range []struct {
		bits, channels int
		format, layout string
	}{{8, 1, "s8", "mono"}, {16, 2, "s16le", "stereo"}, {24, 3, "s24le", "3.0"}, {32, 4, "s32le", "quad"}, {24, 8, "s24le", "7.1"}} {
		var header [42]byte
		copy(header[:], "fLaC")
		header[4], header[7] = 0x80, 34
		packed := uint64(96000)<<44 | uint64(tc.channels-1)<<41 | uint64(tc.bits-1)<<36 | 12345
		binary.BigEndian.PutUint64(header[18:26], packed)
		checksum := md5.Sum([]byte("fixture PCM"))
		copy(header[26:], checksum[:])
		path := filepath.Join(t.TempDir(), "fixture.flac")
		if err := os.WriteFile(path, header[:], 0600); err != nil {
			t.Fatal(err)
		}
		p, err := readFLACPCM(path)
		if err != nil || p == nil || p.Rate != 96000 || p.Channels != tc.channels || p.Bits != tc.bits || p.Samples != 12345 || p.MD5 != checksum {
			t.Fatal("STREAMINFO fields were not preserved", tc, p, err)
		}
		want := []string{"-f", tc.format, "-ar", "96000", "-ac", strconv.Itoa(tc.channels), "-channel_layout", tc.layout}
		if !reflect.DeepEqual(p.inputArgs(), want) || p.byteCount() != 12345*int64(tc.channels)*int64(tc.bits/8) {
			t.Fatal("raw PCM format would change the source audio", p.inputArgs(), p.byteCount())
		}
		for _, field := range []string{"md5", "samples", "rate", "unaligned"} {
			missing := header
			switch field {
			case "md5":
				clear(missing[26:])
			case "samples":
				binary.BigEndian.PutUint64(missing[18:26], packed & ^uint64((1<<36)-1))
			case "rate":
				binary.BigEndian.PutUint64(missing[18:26], packed&((1<<44)-1))
			case "unaligned":
				binary.BigEndian.PutUint64(missing[18:26], packed & ^(uint64(31)<<36) | 19<<36)
			}
			if err := os.WriteFile(path, missing[:], 0600); err != nil {
				t.Fatal(err)
			}
			if fallback, err := readFLACPCM(path); err != nil || fallback != nil {
				t.Fatal("unverifiable PCM enabled tolerant decoding", field, fallback, err)
			}
		}
	}
}

func TestFLACPCMRequiresExactLengthAndMD5(t *testing.T) {
	content := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	p := flacPCM{Rate: 44100, Channels: 2, Bits: 16, Samples: 2, MD5: md5.Sum(content)}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"complete", content, ""},
		{"short", content[:4], "sample count mismatch"},
		{"extra", append(append([]byte(nil), content...), bytes.Repeat([]byte{0}, 4096)...), "sample count mismatch"},
		{"changed", []byte{1, 2, 3, 4, 5, 6, 7, 9}, "MD5 mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			err := p.copyVerified(&output, bytes.NewReader(tc.data))
			if tc.want == "" {
				if err != nil || !bytes.Equal(output.Bytes(), content) {
					t.Fatal("verified audio was changed", output.Bytes(), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatal("invalid PCM was accepted", err)
			}
			if int64(output.Len()) > p.byteCount() {
				t.Fatal("boundary probe leaked into the encoder input")
			}
			if tc.name == "extra" && !bytes.Equal(output.Bytes(), content) {
				t.Fatal("extra decoder output changed the declared PCM", output.Bytes())
			}
		})
	}
	if err := p.copyVerified(io.Discard, io.MultiReader(bytes.NewReader(content), failingPCMReader{})); err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatal("a read failure after all samples was accepted", err)
	}
}

type failingPCMReader struct{}

func (failingPCMReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestFLACSpecificationReadsOnly42BytesAndDoesNotRequireVerificationFields(t *testing.T) {
	var header [42]byte
	copy(header[:], "fLaC")
	header[7] = 34 // More metadata follows; reading it must be unnecessary.
	binary.BigEndian.PutUint64(header[18:26], uint64(44100)<<44|uint64(1)<<41|uint64(19)<<36)
	p, err := readFLACStreamInfo(io.MultiReader(bytes.NewReader(header[:]), failingPCMReader{}))
	if err != nil || p.Rate != 44100 || p.Bits != 20 || p.Channels != 2 || p.Samples != 0 || p.MD5 != [16]byte{} {
		t.Fatal("display specification read tags or required PCM verification fields", p, err)
	}
	for _, raw := range [][]byte{header[:41], bytes.Repeat([]byte{0}, 42)} {
		if _, err := readFLACStreamInfo(bytes.NewReader(raw)); err == nil {
			t.Fatal("invalid STREAMINFO accepted")
		}
	}
}
