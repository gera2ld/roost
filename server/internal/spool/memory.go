package spool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"sync"
)

// Memory is an in-memory Spool for tests and local development.
type Memory struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

type memoryObject struct {
	data []byte
	meta map[string]string
}

func NewMemory() *Memory { return &Memory{objects: map[string]memoryObject{}} }

// Put stores raw bytes under key with the given envelope.
func (m *Memory) Put(key string, raw []byte, envelopeFrom, envelopeTo string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memoryObject{data: raw, meta: map[string]string{
		MetaEnvelopeFrom: envelopeFrom,
		MetaEnvelopeTo:   envelopeTo,
	}}
}

func (m *Memory) List(_ context.Context, startAfter string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for k := range m.objects {
		if k > startAfter {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	return keys, nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, Object{}, fmt.Errorf("spool: no such key %q", key)
	}
	return io.NopCloser(bytes.NewReader(obj.data)), Object{
		Key:          key,
		Size:         int64(len(obj.data)),
		EnvelopeFrom: metaLookup(obj.meta, MetaEnvelopeFrom),
		EnvelopeTo:   metaLookup(obj.meta, MetaEnvelopeTo),
	}, nil
}
