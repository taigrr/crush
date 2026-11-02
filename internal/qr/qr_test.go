package qr

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The golden symbols in testdata were produced by libqrencode 4.x:
//
//	qrencode -8 -l <L|M|Q|H> -m 0 -t ASCII <data>
//
// with each module reduced to 1 (dark) or 0 (light). The first line of
// each file is the encoded data.
func TestGoldenSymbols(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "*.txt"))
	require.NoError(t, err)
	require.NotEmpty(t, files)
	levels := map[byte]Level{'L': Low, 'M': Medium, 'Q': Quartile, 'H': High}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(file)
			require.NoError(t, err)
			lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
			data, rows := lines[0], lines[1:]
			level := levels[filepath.Base(file)[0]]
			size := len(rows)
			version := (size - 17) / 4

			format := 0
			for i := range 6 {
				format |= bit(rows[i][8]) << i
			}
			format |= bit(rows[7][8])<<6 | bit(rows[8][8])<<7 | bit(rows[8][7])<<8
			for i := 9; i < 15; i++ {
				format |= bit(rows[8][14-i]) << i
			}
			wantMask := (format ^ formatMaskBits) >> 10 & 7
			require.Equal(t, FormatInfo(level, wantMask), format, "format bits")

			code, err := EncodeWith([]byte(data), level, Options{Mask: wantMask})
			require.NoError(t, err)
			require.Equal(t, version, code.Version)
			require.Equal(t, size, code.Size)
			for y, row := range rows {
				var got strings.Builder
				for x := range size {
					if code.Dark(x, y) {
						got.WriteByte('1')
					} else {
						got.WriteByte('0')
					}
				}
				require.Equal(t, row, got.String(), "row %d", y)
			}

			auto, err := Encode([]byte(data), level)
			require.NoError(t, err)
			require.Equal(t, version, auto.Version)
			require.LessOrEqual(t, auto.penalty(), code.penalty())
		})
	}
}

func bit(b byte) int {
	if b == '1' {
		return 1
	}
	return 0
}

func TestFormatAndVersionInfo(t *testing.T) {
	t.Parallel()
	// Values from ISO/IEC 18004 Annex C and D.
	require.Equal(t, 0b111011111000100, FormatInfo(Low, 0))
	require.Equal(t, 0b101010000010010, FormatInfo(Medium, 0))
	require.Equal(t, 0b100000011001110, FormatInfo(Medium, 5))
	require.Equal(t, 0b100101010100000, FormatInfo(Medium, 7))
	require.Equal(t, 0b001011010001001, FormatInfo(High, 0))
	require.Equal(t, 0x07C94, VersionInfo(7))
	require.Equal(t, 0x28C69, VersionInfo(40))
}

func TestCapacity(t *testing.T) {
	t.Parallel()
	// Byte-mode capacities from ISO/IEC 18004 Table 7.
	cases := []struct {
		version int
		level   Level
		want    int
	}{
		{1, Low, 17},
		{1, Medium, 14},
		{1, High, 7},
		{10, Medium, 213},
		{15, Medium, 412},
		{15, Low, 520},
		{40, Low, 2953},
		{40, Medium, 2331},
		{40, High, 1273},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, Capacity(tc.version, tc.level), "v%d level %d", tc.version, tc.level)
	}
}

func TestEncodeErrors(t *testing.T) {
	t.Parallel()
	_, err := Encode(make([]byte, 2332), Medium)
	require.ErrorIs(t, err, ErrTooLong)
	_, err = EncodeWith([]byte("x"), Medium, Options{Mask: 8})
	require.Error(t, err)
	_, err = EncodeWith([]byte("x"), Level(9), Options{})
	require.Error(t, err)
	code, err := EncodeWith([]byte("x"), Medium, Options{MinVersion: 5, Mask: AutoMask})
	require.NoError(t, err)
	require.Equal(t, 5, code.Version)
}

func TestHalfBlocks(t *testing.T) {
	t.Parallel()
	code, err := Encode([]byte("hi"), Medium)
	require.NoError(t, err)
	span := code.Size + 2*QuietZone

	for _, invert := range []bool{false, true} {
		lines := strings.Split(code.HalfBlocks(invert), "\n")
		require.Len(t, lines, (span+1)/2)
		for row, line := range lines {
			cells := []rune(line)
			require.Len(t, cells, span)
			for col, cell := range cells {
				x, y := col-QuietZone, row*2-QuietZone
				top := code.Dark(x, y) == invert
				bottom := code.Dark(x, y+1) == invert
				want := map[[2]bool]rune{
					{true, true}: '█', {true, false}: '▀',
					{false, true}: '▄', {false, false}: ' ',
				}[[2]bool{top, bottom}]
				require.Equal(t, want, cell, "invert=%v row %d col %d", invert, row, col)
			}
		}
	}
	// The quiet zone is ink in normal mode and blank when inverted.
	require.True(t, strings.HasPrefix(code.HalfBlocks(false), "█"))
	require.True(t, strings.HasPrefix(code.HalfBlocks(true), " "))
}

func TestWritePNG(t *testing.T) {
	t.Parallel()
	code, err := Encode([]byte("hi"), Medium)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, code.WritePNG(&buf, 3))
	img, err := png.Decode(&buf)
	require.NoError(t, err)
	span := (code.Size + 2*QuietZone) * 3
	require.Equal(t, span, img.Bounds().Dx())
	r, _, _, _ := img.At(QuietZone*3, QuietZone*3).RGBA()
	require.Zero(t, r, "top-left finder module is dark")
	r, _, _, _ = img.At(0, 0).RGBA()
	require.NotZero(t, r, "quiet zone is light")
}
