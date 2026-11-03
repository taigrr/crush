// Package qr is a small QR Code Model 2 encoder (ISO/IEC 18004) that
// supports byte mode, all four error correction levels, and versions 1
// through 40. It exists so pairing codes can be shown without pulling in
// a third-party dependency.
package qr

import (
	"errors"
	"fmt"
)

// Level is the error correction level of a symbol.
type Level int

// Error correction levels, from lowest to highest redundancy.
const (
	Low      Level = iota // Recovers about 7% of codewords.
	Medium                // Recovers about 15% of codewords.
	Quartile              // Recovers about 25% of codewords.
	High                  // Recovers about 30% of codewords.
)

const (
	// MinVersion and MaxVersion bound the symbol versions.
	MinVersion = 1
	MaxVersion = 40

	// AutoMask lets Encode pick the mask with the lowest penalty.
	AutoMask = -1

	numMasks = 8

	modeByte       = 0x4
	padByteA       = 0xEC
	padByteB       = 0x11
	formatMaskBits = 0x5412
	formatGen      = 0x537
	versionGen     = 0x1F25
)

// ErrTooLong is returned when the data does not fit in a version 40
// symbol at the requested level.
var ErrTooLong = errors.New("qr: data too long")

// formatBits maps a Level to its two-bit format code.
var formatBits = [4]int{Low: 1, Medium: 0, Quartile: 3, High: 2}

// eccPerBlock[level][version] is the number of error correction
// codewords in each block.
var eccPerBlock = [4][41]int{
	{-1, 7, 10, 15, 20, 26, 18, 20, 24, 30, 18, 20, 24, 26, 30, 22, 24, 28, 30, 28, 28, 28, 28, 30, 30, 26, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	{-1, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24, 28, 28, 26, 26, 26, 26, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28, 28},
	{-1, 13, 22, 18, 26, 18, 24, 18, 22, 20, 24, 28, 26, 24, 20, 30, 24, 28, 28, 26, 30, 28, 30, 30, 30, 30, 28, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
	{-1, 17, 28, 22, 16, 22, 28, 26, 26, 24, 28, 24, 28, 22, 24, 24, 30, 28, 28, 26, 28, 30, 24, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30, 30},
}

// numBlocks[level][version] is the number of error correction blocks.
var numBlocks = [4][41]int{
	{-1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 4, 4, 4, 4, 4, 6, 6, 6, 6, 7, 8, 8, 9, 9, 10, 12, 12, 12, 13, 14, 15, 16, 17, 18, 19, 19, 20, 21, 22, 24, 25},
	{-1, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10, 10, 11, 13, 14, 16, 17, 17, 18, 20, 21, 23, 25, 26, 28, 29, 31, 33, 35, 37, 38, 40, 43, 45, 47, 49},
	{-1, 1, 1, 2, 2, 4, 4, 6, 6, 8, 8, 8, 10, 12, 16, 12, 17, 16, 18, 21, 20, 23, 23, 25, 27, 29, 34, 34, 35, 38, 40, 43, 45, 48, 51, 53, 56, 59, 62, 65, 68},
	{-1, 1, 1, 2, 4, 4, 4, 5, 6, 8, 8, 11, 11, 16, 16, 18, 16, 19, 21, 25, 25, 25, 34, 30, 32, 35, 37, 40, 42, 45, 48, 51, 54, 57, 60, 63, 66, 70, 74, 77, 81},
}

// Code is an encoded QR symbol.
type Code struct {
	Version int
	Level   Level
	Mask    int
	Size    int

	modules  [][]bool
	function [][]bool
}

// Dark reports whether the module at column x, row y is dark. Coordinates
// outside the symbol (the quiet zone) are light.
func (c *Code) Dark(x, y int) bool {
	if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
		return false
	}
	return c.modules[y][x]
}

// Options tunes Encode. The zero value picks the smallest version and
// the best mask.
type Options struct {
	MinVersion int
	Mask       int
}

// Encode encodes data in byte mode at the given level, choosing the
// smallest version that fits and the lowest-penalty mask.
func Encode(data []byte, level Level) (*Code, error) {
	return EncodeWith(data, level, Options{Mask: AutoMask})
}

// EncodeWith is Encode with explicit options.
func EncodeWith(data []byte, level Level, opts Options) (*Code, error) {
	if level < Low || level > High {
		return nil, fmt.Errorf("qr: invalid level %d", level)
	}
	if opts.Mask < AutoMask || opts.Mask >= numMasks {
		return nil, fmt.Errorf("qr: invalid mask %d", opts.Mask)
	}
	minVer := max(opts.MinVersion, MinVersion)
	if minVer > MaxVersion {
		return nil, fmt.Errorf("qr: invalid minimum version %d", opts.MinVersion)
	}
	version := 0
	for ver := minVer; ver <= MaxVersion; ver++ {
		if segmentBits(len(data), ver) <= DataCodewords(ver, level)*8 {
			version = ver
			break
		}
	}
	if version == 0 {
		return nil, ErrTooLong
	}

	codewords := addECCAndInterleave(dataCodewords(data, version, level), version, level)
	code := newCode(version, level)
	code.drawFunctionPatterns()
	code.drawCodewords(codewords)

	mask := opts.Mask
	if mask == AutoMask {
		best := -1
		for candidate := range numMasks {
			code.applyMask(candidate)
			code.drawFormatBits(candidate)
			if p := code.penalty(); best < 0 || p < best {
				best, mask = p, candidate
			}
			code.applyMask(candidate)
		}
	}
	code.Mask = mask
	code.applyMask(mask)
	code.drawFormatBits(mask)
	return code, nil
}

// Capacity returns the maximum number of bytes a symbol of the given
// version and level holds in byte mode.
func Capacity(version int, level Level) int {
	bits := DataCodewords(version, level)*8 - 4 - countBits(version)
	return bits / 8
}

// DataCodewords returns the number of data (non-ECC) codewords of a
// symbol.
func DataCodewords(version int, level Level) int {
	return rawDataModules(version)/8 - eccPerBlock[level][version]*numBlocks[level][version]
}

func countBits(version int) int {
	if version <= 9 {
		return 8
	}
	return 16
}

func segmentBits(n, version int) int {
	if version <= 9 && n > 0xFF {
		return 1 << 30
	}
	return 4 + countBits(version) + n*8
}

// rawDataModules is the number of modules available for data and ECC
// codewords, including remainder bits.
func rawDataModules(version int) int {
	result := (16*version+128)*version + 64
	if version >= 2 {
		numAlign := version/7 + 2
		result -= (25*numAlign-10)*numAlign - 55
		if version >= 7 {
			result -= 36
		}
	}
	return result
}

type bitBuffer []bool

func (b *bitBuffer) append(value, n int) {
	for i := n - 1; i >= 0; i-- {
		*b = append(*b, (value>>i)&1 != 0)
	}
}

func dataCodewords(data []byte, version int, level Level) []byte {
	capBits := DataCodewords(version, level) * 8
	var bits bitBuffer
	bits.append(modeByte, 4)
	bits.append(len(data), countBits(version))
	for _, b := range data {
		bits.append(int(b), 8)
	}
	bits.append(0, min(4, capBits-len(bits)))
	bits.append(0, (8-len(bits)%8)%8)
	for pad := padByteA; len(bits) < capBits; pad ^= padByteA ^ padByteB {
		bits.append(pad, 8)
	}
	out := make([]byte, len(bits)/8)
	for i, bit := range bits {
		if bit {
			out[i>>3] |= 1 << (7 - i&7)
		}
	}
	return out
}

func addECCAndInterleave(data []byte, version int, level Level) []byte {
	blocks := numBlocks[level][version]
	eccLen := eccPerBlock[level][version]
	raw := rawDataModules(version) / 8
	numShort := blocks - raw%blocks
	shortLen := raw / blocks
	gen := rsGenerator(eccLen)

	out := make([][]byte, blocks)
	k := 0
	for i := range blocks {
		datLen := shortLen - eccLen
		if i >= numShort {
			datLen++
		}
		dat := data[k : k+datLen]
		k += datLen
		block := make([]byte, 0, shortLen+1)
		block = append(block, dat...)
		if i < numShort {
			block = append(block, 0)
		}
		block = append(block, rsRemainder(dat, gen)...)
		out[i] = block
	}

	result := make([]byte, 0, raw)
	for i := range shortLen + 1 {
		for j, block := range out {
			if i == shortLen-eccLen && j < numShort {
				continue
			}
			result = append(result, block[i])
		}
	}
	return result
}

func newCode(version int, level Level) *Code {
	size := version*4 + 17
	c := &Code{Version: version, Level: level, Size: size}
	c.modules = make([][]bool, size)
	c.function = make([][]bool, size)
	for i := range size {
		c.modules[i] = make([]bool, size)
		c.function[i] = make([]bool, size)
	}
	return c
}

func (c *Code) setFunction(x, y int, dark bool) {
	c.modules[y][x] = dark
	c.function[y][x] = true
}

func (c *Code) drawFunctionPatterns() {
	for i := range c.Size {
		c.setFunction(6, i, i%2 == 0)
		c.setFunction(i, 6, i%2 == 0)
	}
	c.drawFinder(3, 3)
	c.drawFinder(c.Size-4, 3)
	c.drawFinder(3, c.Size-4)

	positions := alignmentPositions(c.Version)
	last := len(positions) - 1
	for i, x := range positions {
		for j, y := range positions {
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			c.drawAlignment(x, y)
		}
	}

	// Reserve the format areas; drawFormatBits fills them in.
	c.drawFormatBits(0)
	c.drawVersion()
}

func (c *Code) drawFinder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || y < 0 || x >= c.Size || y >= c.Size {
				continue
			}
			dist := max(abs(dx), abs(dy))
			c.setFunction(x, y, dist != 2 && dist != 4)
		}
	}
}

func (c *Code) drawAlignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			c.setFunction(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

func alignmentPositions(version int) []int {
	if version == 1 {
		return nil
	}
	numAlign := version/7 + 2
	step := (version*8 + numAlign*3 + 5) / (numAlign*4 - 4) * 2
	result := make([]int, numAlign)
	result[0] = 6
	pos := version*4 + 17 - 7
	for i := numAlign - 1; i >= 1; i-- {
		result[i] = pos
		pos -= step
	}
	return result
}

// FormatInfo returns the 15-bit format information word for a level and
// mask, including BCH bits and the fixed XOR mask.
func FormatInfo(level Level, mask int) int {
	data := formatBits[level]<<3 | mask
	rem := data
	for range 10 {
		rem = rem<<1 ^ (rem>>9)*formatGen
	}
	return (data<<10 | rem) ^ formatMaskBits
}

// VersionInfo returns the 18-bit version information word for versions
// 7 and up.
func VersionInfo(version int) int {
	rem := version
	for range 12 {
		rem = rem<<1 ^ (rem>>11)*versionGen
	}
	return version<<12 | rem
}

func (c *Code) drawFormatBits(mask int) {
	bits := FormatInfo(c.Level, mask)
	bit := func(i int) bool { return (bits>>i)&1 != 0 }
	for i := 0; i <= 5; i++ {
		c.setFunction(8, i, bit(i))
	}
	c.setFunction(8, 7, bit(6))
	c.setFunction(8, 8, bit(7))
	c.setFunction(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		c.setFunction(14-i, 8, bit(i))
	}
	for i := range 8 {
		c.setFunction(c.Size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		c.setFunction(8, c.Size-15+i, bit(i))
	}
	c.setFunction(8, c.Size-8, true)
}

func (c *Code) drawVersion() {
	if c.Version < 7 {
		return
	}
	bits := VersionInfo(c.Version)
	for i := range 18 {
		dark := (bits>>i)&1 != 0
		a := c.Size - 11 + i%3
		b := i / 3
		c.setFunction(a, b, dark)
		c.setFunction(b, a, dark)
	}
}

func (c *Code) drawCodewords(data []byte) {
	i := 0
	total := len(data) * 8
	for right := c.Size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		upward := (right+1)&2 == 0
		for vert := range c.Size {
			for j := range 2 {
				x := right - j
				y := vert
				if upward {
					y = c.Size - 1 - vert
				}
				if c.function[y][x] || i >= total {
					continue
				}
				c.modules[y][x] = (data[i>>3]>>(7-i&7))&1 != 0
				i++
			}
		}
	}
}

func maskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return x*y%2+x*y%3 == 0
	case 6:
		return (x*y%2+x*y%3)%2 == 0
	default:
		return ((x+y)%2+x*y%3)%2 == 0
	}
}

// applyMask XORs the mask into the data modules. Applying the same mask
// twice undoes it.
func (c *Code) applyMask(mask int) {
	for y := range c.Size {
		for x := range c.Size {
			if !c.function[y][x] && maskBit(mask, x, y) {
				c.modules[y][x] = !c.modules[y][x]
			}
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
