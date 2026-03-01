package embeddings

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestSerializeFloat32(t *testing.T) {
	vec := []float32{1.0, 2.5, -3.14, 0.0}
	buf := SerializeFloat32(vec)

	if len(buf) != 4*len(vec) {
		t.Fatalf("expected %d bytes, got %d", 4*len(vec), len(buf))
	}

	// Verify round-trip.
	for i, want := range vec {
		bits := binary.LittleEndian.Uint32(buf[i*4:])
		got := math.Float32frombits(bits)
		if got != want {
			t.Errorf("vec[%d] = %f, want %f", i, got, want)
		}
	}
}

func TestSerializeFloat32Empty(t *testing.T) {
	buf := SerializeFloat32(nil)
	if len(buf) != 0 {
		t.Errorf("expected empty buffer, got %d bytes", len(buf))
	}
}

func TestIsConnectionError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"connection refused", true},
		{"connect ECONNREFUSED", true},
		{"model not found", false},
		{"timeout", false},
	}
	for _, tt := range tests {
		got := isConnectionError(errStr(tt.msg))
		if got != tt.want {
			t.Errorf("isConnectionError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
