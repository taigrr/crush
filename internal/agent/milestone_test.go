package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMilestoneBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		afterTurn  int64
		totalTurns int
		want       []int64
	}{
		{
			name:       "no boundary yet",
			afterTurn:  0,
			totalTurns: 9,
			want:       nil,
		},
		{
			name:       "exactly one boundary",
			afterTurn:  0,
			totalTurns: 10,
			want:       []int64{10},
		},
		{
			name:       "single run crosses several boundaries at once",
			afterTurn:  0,
			totalTurns: 32,
			want:       []int64{10, 20, 30},
		},
		{
			name:       "incremental from prior milestone",
			afterTurn:  30,
			totalTurns: 45,
			want:       []int64{40},
		},
		{
			name:       "incremental crossing multiple boundaries",
			afterTurn:  10,
			totalTurns: 41,
			want:       []int64{20, 30, 40},
		},
		{
			name:       "no new boundary since last milestone",
			afterTurn:  40,
			totalTurns: 45,
			want:       nil,
		},
		{
			name:       "prior milestone off a multiple still aligns to next multiple",
			afterTurn:  32,
			totalTurns: 60,
			want:       []int64{40, 50, 60},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, milestoneBoundaries(tt.afterTurn, tt.totalTurns))
		})
	}
}
