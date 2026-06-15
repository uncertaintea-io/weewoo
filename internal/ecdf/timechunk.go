package timechunk

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"sort"
	"time"
)

type Sample struct {
	Value float64
	Count uint64
}

func CountSamples(samples []float64) []Sample {
	if len(samples) == 0 {
		return nil
	}
	result := make([]Sample, 0, len(samples))
	sort.Float64Slice(samples).Sort()
	last := samples[0]
	lastIndex := 0
	for i, sample := range samples[1:] {
		if sample == last {
			continue
		}
		result = append(result, Sample{Value: last, Count: uint64(i - lastIndex)})
		lastIndex = i
		last = sample
	}
	result = append(result, Sample{Value: last, Count: uint64(len(samples) - lastIndex)})
	return result
}

// Encode creates a binary blob that records the given samples.
func Encode(timestamp time.Time, x []Sample, y []Sample) ([]byte, error) {
	buf := bytes.NewBuffer(nil)
	//w := snappy.NewBufferedWriter(buf)
	w := bufio.NewWriter(buf)
	err := binary.Write(w, binary.BigEndian, timestamp.Unix())
	if err != nil {
		return nil, err
	}
	err = writeSamples(w, x)
	if err != nil {
		return nil, err
	}
	err = writeSamples(w, y)
	if err != nil {
		return nil, err
	}
	w.Flush()
	return buf.Bytes(), nil
}

// Decode reads samples back from an encoded time chunk.
func Decode(data []byte) (time.Time, []Sample, []Sample, error) {
	r := bufio.NewReader(bytes.NewBuffer(data))
	var t int64
	if err := binary.Read(r, binary.BigEndian, &t); err != nil {
		return time.Time{}, nil, nil, err
	}
	x, err := readSamples(r)
	if err != nil {
		return time.Time{}, nil, nil, err
	}
	y, err := readSamples(r)
	if err != nil {
		return time.Time{}, nil, nil, err
	}
	return time.Unix(t, 0), x, y, nil
}

func writeSamples(w io.Writer, samples []Sample) error {
	// Record the number of samples
	err := writeUvarint(w, uint64(len(samples)))
	if err != nil {
		return err
	}
	// Write the sample values
	for _, sample := range samples {
		err := binary.Write(w, binary.BigEndian, sample.Value)
		if err != nil {
			return err
		}
		err = writeUvarint(w, sample.Count)
		if err != nil {
			return err
		}
	}
	return nil
}

func writeUvarint(writer io.Writer, i uint64) error {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], i)
	_, err := writer.Write(buf[:n])
	return err
}

func readSamples(r *bufio.Reader) ([]Sample, error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	samples := make([]Sample, n)
	for i := range n {
		ptr := &samples[i]
		err := binary.Read(r, binary.BigEndian, &ptr.Value)
		if err != nil {
			return nil, err
		}
		ptr.Count, err = binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
	}
	return samples, nil
}
