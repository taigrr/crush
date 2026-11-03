package qr

// Penalty weights from ISO/IEC 18004 section 7.8.3.
const (
	penaltyN1 = 3
	penaltyN2 = 3
	penaltyN3 = 40
	penaltyN4 = 10
)

// finderLike patterns: 1:1:3:1:1 dark/light with four light modules on
// one side.
var finderLike = [2][11]bool{
	{true, false, true, true, true, false, true, false, false, false, false},
	{false, false, false, false, true, false, true, true, true, false, true},
}

// penalty scores the current symbol (with mask and format bits applied).
// Lower is better.
func (c *Code) penalty() int {
	result := 0
	size := c.Size

	for _, horizontal := range []bool{true, false} {
		at := func(line, pos int) bool {
			if horizontal {
				return c.Dark(pos, line)
			}
			return c.Dark(line, pos)
		}
		for line := range size {
			run := 1
			for pos := 1; pos < size; pos++ {
				if at(line, pos) == at(line, pos-1) {
					run++
					continue
				}
				if run >= 5 {
					result += penaltyN1 + run - 5
				}
				run = 1
			}
			if run >= 5 {
				result += penaltyN1 + run - 5
			}

			for start := -10; start < size; start++ {
				for _, pattern := range finderLike {
					match := true
					for k, want := range pattern {
						if at(line, start+k) != want {
							match = false
							break
						}
					}
					if match {
						result += penaltyN3
					}
				}
			}
		}
	}

	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			dark := c.modules[y][x]
			if dark == c.modules[y][x+1] && dark == c.modules[y+1][x] && dark == c.modules[y+1][x+1] {
				result += penaltyN2
			}
		}
	}

	dark := 0
	for y := range size {
		for x := range size {
			if c.modules[y][x] {
				dark++
			}
		}
	}
	total := size * size
	result += abs(dark*20-total*10) / total * penaltyN4
	return result
}
