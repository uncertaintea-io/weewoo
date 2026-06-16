package main

import "sort"

type Coord struct {
	X float64
	Y float64
}

func MakeECDF(values []float64) []Coord {
	if len(values) == 0 {
		return []Coord{}
	}

	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)

	ecdf := make([]Coord, 0, len(sorted))
	total := float64(len(sorted))
	for i := 0; i < len(sorted); {
		x := sorted[i]
		j := i + 1
		for j < len(sorted) && sorted[j] == x {
			j++
		}
		ecdf = append(ecdf, Coord{X: x, Y: float64(j) / total})
		i = j
	}
	return ecdf
}
