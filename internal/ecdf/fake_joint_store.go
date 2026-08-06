package ecdf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"time"
)

type fakeCurrentJoint struct {
	Timestamp time.Time
	Bytes     []byte
}

type fakeJointStore struct {
	current map[int]map[int]*fakeCurrentJoint
}

func (js *fakeJointStore) Publish(ctx context.Context, serviceID, indicatorID int, intervalEnd time.Time, build func(io.Writer) error) (int64, bool, error) {
	if js.current == nil {
		js.current = make(map[int]map[int]*fakeCurrentJoint)
	}
	m := js.current[serviceID]
	if m == nil {
		m = make(map[int]*fakeCurrentJoint)
		js.current[serviceID] = m
	}
	current := m[indicatorID]
	if current == nil {
		var buf bytes.Buffer
		if err := build(&buf); err != nil {
			return 0, false, err
		}
		current = &fakeCurrentJoint{
			Timestamp: intervalEnd,
			Bytes:     buf.Bytes(),
		}
		m[indicatorID] = current
		return int64(len(current.Bytes)), true, nil
	}
	if intervalEnd.After(current.Timestamp) {
		var buf bytes.Buffer
		if err := build(&buf); err != nil {
			return 0, false, err
		}
		current.Timestamp = intervalEnd
		current.Bytes = buf.Bytes()
		return int64(len(current.Bytes)), true, nil
	}
	return 0, false, nil
}

func (js *fakeJointStore) ReadCurrent(ctx context.Context, serviceID, indicatorID int) ([]byte, string, error) {
	if js.current != nil {
		if m := js.current[serviceID]; m != nil {
			if current := m[indicatorID]; current != nil {
				sum := sha256.Sum256(current.Bytes)
				return current.Bytes, hex.EncodeToString(sum[:]), nil
			}
		}
	}
	return nil, "", sql.ErrNoRows
}

func BuildFakeJointECDF(currentJointECDF []byte) func(io.Writer) error {
	return func(dst io.Writer) error {
		src := bytes.NewReader(currentJointECDF)
		_, err := io.Copy(dst, src)
		return err
	}
}

func NewFakeJointStore() JointStore {
	return &fakeJointStore{}
}
