package voice

import "math"

func framesToMonoI16(samples []int16, channels int) []int16 {
	if channels <= 0 {
		return nil
	}
	if channels == 1 {
		out := make([]int16, len(samples))
		copy(out, samples)
		return out
	}
	n := len(samples) / channels
	mono := make([]int16, 0, n)
	for i := 0; i+channels <= len(samples); i += channels {
		var sum int32
		for c := range channels {
			sum += int32(samples[i+c])
		}
		avg := sum / int32(channels)
		if avg > math.MaxInt16 {
			avg = math.MaxInt16
		} else if avg < math.MinInt16 {
			avg = math.MinInt16
		}
		mono = append(mono, int16(avg))
	}
	return mono
}

func resampleMonoI16(samples []int16, inputRate, outputRate uint32) []int16 {
	if len(samples) == 0 || inputRate == 0 || outputRate == 0 {
		return nil
	}
	if inputRate == outputRate {
		out := make([]int16, len(samples))
		copy(out, samples)
		return out
	}
	outputLen := int((uint64(len(samples)) * uint64(outputRate)) / uint64(inputRate))
	if outputLen < 1 {
		outputLen = 1
	}
	step := float64(inputRate) / float64(outputRate)
	out := make([]int16, 0, outputLen)
	for i := range outputLen {
		srcPos := float64(i) * step
		idx := int(math.Floor(srcPos))
		frac := srcPos - float64(idx)
		s0 := float64(samples[idx])
		s1 := s0
		if idx+1 < len(samples) {
			s1 = float64(samples[idx+1])
		}
		sample := s0 + (s1-s0)*frac
		clamped := math.Round(sample)
		if clamped > math.MaxInt16 {
			clamped = math.MaxInt16
		} else if clamped < math.MinInt16 {
			clamped = math.MinInt16
		}
		out = append(out, int16(clamped))
	}
	return out
}

func pcm16LE(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		out[i*2] = byte(s)
		out[i*2+1] = byte(s >> 8)
	}
	return out
}

func f32ToI16(v float32) int16 {
	scaled := v * 32767
	if scaled > math.MaxInt16 {
		return math.MaxInt16
	}
	if scaled < math.MinInt16 {
		return math.MinInt16
	}
	return int16(scaled)
}
