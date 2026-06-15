package timechunk

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/apache/arrow-go/v18/parquet"
	"github.com/apache/arrow-go/v18/parquet/compress"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
)

type Sample struct {
	Value float64
	Count uint64
}

type JointSample struct {
	X      float64
	Y      float64
	Weight uint64
}

type TimeChunk struct {
	Timestamp time.Time
	Total     uint64
	Samples   []JointSample
}

// Build constructs a time chunk from a timestamp, x samples, and y samples
func Build(timestamp time.Time, x []Sample, y []Sample) (*TimeChunk, error) {
	nx := uint64(0)
	for _, xs := range x {
		nx += xs.Count
	}
	ny := uint64(0)
	for _, ys := range y {
		ny += ys.Count
	}
	samples := make([]JointSample, 0, len(x)*len(y))
	for _, xs := range x {
		for _, ys := range y {
			samples = append(samples, JointSample{X: xs.Value, Y: ys.Value, Weight: xs.Count * ys.Count})
		}
	}
	return &TimeChunk{
		Timestamp: timestamp,
		Total:     nx * ny,
		Samples:   samples,
	}, nil
}

func (tc *TimeChunk) WriteTo(w io.Writer) error {
	err := binary.Write(w, binary.BigEndian, tc.Timestamp.Unix())
	if err != nil {
		return err
	}
	return writeJointSamples(w, tc.Samples)
}

func ReadFrom(r io.Reader) (*TimeChunk, error) {
	timestamp := int64(0)
	err := binary.Read(r, binary.BigEndian, &timestamp)
	if err != nil {
		return nil, err
	}
	samples, err := readJointSamples(r)
	if err != nil {
		return nil, err
	}
	return &TimeChunk{
		Timestamp: time.Unix(timestamp, 0),
		Samples:   samples,
	}, nil
}

var jointSampleSchema = arrow.NewSchema([]arrow.Field{
	{Name: "x", Type: arrow.PrimitiveTypes.Float64},
	{Name: "y", Type: arrow.PrimitiveTypes.Float64},
	{Name: "w", Type: arrow.PrimitiveTypes.Uint64},
}, nil)

func readJointSamples(r io.Reader) ([]JointSample, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read joint samples parquet data: %w", err)
	}

	mem := memory.NewGoAllocator()
	tbl, err := pqarrow.ReadTable(context.Background(), bytes.NewReader(data), nil, pqarrow.ArrowReadProperties{}, mem)
	if err != nil {
		return nil, fmt.Errorf("read joint samples parquet table: %w", err)
	}
	defer tbl.Release()

	if tbl.NumCols() != int64(len(jointSampleSchema.Fields())) {
		return nil, fmt.Errorf("read joint samples parquet table: expected %d columns, got %d", len(jointSampleSchema.Fields()), tbl.NumCols())
	}
	for i, field := range jointSampleSchema.Fields() {
		gotField := tbl.Schema().Field(i)
		if gotField.Name != field.Name || !arrow.TypeEqual(gotField.Type, field.Type) {
			return nil, fmt.Errorf("read joint samples parquet table: column %d has field %s %s, expected %s %s", i, gotField.Name, gotField.Type, field.Name, field.Type)
		}
	}

	samples := make([]JointSample, 0, int(tbl.NumRows()))
	tableReader := array.NewTableReader(tbl, 0)
	defer tableReader.Release()
	for tableReader.Next() {
		record := tableReader.RecordBatch()
		xs := record.Column(0).(*array.Float64)
		ys := record.Column(1).(*array.Float64)
		weights := record.Column(2).(*array.Uint64)

		rowOffset := len(samples)
		for i := 0; i < int(record.NumRows()); i++ {
			if xs.IsNull(i) || ys.IsNull(i) || weights.IsNull(i) {
				return nil, fmt.Errorf("read joint samples parquet table: row %d contains null", rowOffset+i)
			}
			samples = append(samples, JointSample{
				X:      xs.Value(i),
				Y:      ys.Value(i),
				Weight: weights.Value(i),
			})
		}
	}
	if err := tableReader.Err(); err != nil {
		return nil, fmt.Errorf("read joint samples parquet records: %w", err)
	}

	return samples, nil
}

func writeJointSamples(w io.Writer, samples []JointSample) error {
	fw, err := pqarrow.NewFileWriter(
		jointSampleSchema,
		w,
		parquet.NewWriterProperties(parquet.WithCompression(compress.Codecs.Snappy)),
		pqarrow.DefaultWriterProps(),
	)
	if err != nil {
		return nil
	}
	mem := memory.NewGoAllocator()
	recordBuilder := array.NewRecordBuilder(mem, jointSampleSchema)
	xBuilder := recordBuilder.Field(0).(*array.Float64Builder)
	yBuilder := recordBuilder.Field(1).(*array.Float64Builder)
	wBuilder := recordBuilder.Field(2).(*array.Uint64Builder)

	// Write all the rows as a single record batch
	for _, sample := range samples {
		xBuilder.Append(sample.X)
		yBuilder.Append(sample.Y)
		wBuilder.Append(sample.Weight)
	}

	// Write the record batch to the file writer
	record := recordBuilder.NewRecord()
	defer record.Release()
	if err := fw.WriteBuffered(record); err != nil {
		return fmt.Errorf("write univariate record: %w", err)
	}

	// Close the file writer
	if err := fw.Close(); err != nil {
		return fmt.Errorf("close univariate writer: %w", err)
	}
	return nil
}
