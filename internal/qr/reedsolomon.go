package qr

// gfPoly is the QR field polynomial x^8 + x^4 + x^3 + x^2 + 1.
const gfPoly = 0x11D

// gfMul multiplies two elements of GF(2^8) modulo gfPoly.
func gfMul(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = z<<1 ^ (z>>7)*gfPoly
		z ^= int((y>>i)&1) * int(x)
	}
	return byte(z)
}

// rsGenerator returns the coefficients, highest power first and without
// the leading 1, of the Reed-Solomon generator polynomial
// (x - a^0)(x - a^1)...(x - a^(degree-1)).
func rsGenerator(degree int) []byte {
	result := make([]byte, degree)
	result[degree-1] = 1
	var root byte = 1
	for range degree {
		for j := range degree {
			result[j] = gfMul(result[j], root)
			if j+1 < degree {
				result[j] ^= result[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return result
}

// rsRemainder returns the Reed-Solomon error correction codewords of data
// for the given generator.
func rsRemainder(data, gen []byte) []byte {
	result := make([]byte, len(gen))
	for _, b := range data {
		factor := b ^ result[0]
		copy(result, result[1:])
		result[len(result)-1] = 0
		for i, coef := range gen {
			result[i] ^= gfMul(coef, factor)
		}
	}
	return result
}
