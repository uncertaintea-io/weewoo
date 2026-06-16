package main

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMakeECDF(t *testing.T) {
	tests := []struct {
		input []float64
		want  []Coord
	}{
		{
			input: []float64{},
			want:  []Coord{},
		}, {
			input: []float64{2},
			want:  []Coord{{2.0, 1.0}},
		}, {
			input: []float64{2, 3},
			want:  []Coord{{2.0, 0.5}, {3.0, 1.0}},
		},
	}
	for i, test := range tests {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got := MakeECDF(test.input)
			assert.Equal(t, test.want, got)
		})
	}
}
