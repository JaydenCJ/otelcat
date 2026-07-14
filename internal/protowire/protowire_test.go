// Tests for the hand-rolled protobuf wire reader. These pin down the
// exact byte-level behavior everything above depends on: varint edge
// cases, truncation detection, and the ability to skip unknown fields
// (forward compatibility with newer OTLP revisions).
package protowire

import (
	"errors"
	"math"
	"testing"
)

func TestVarintDecodesCanonicalValues(t *testing.T) {
	cases := []struct {
		buf  []byte
		want uint64
	}{
		{[]byte{0x07}, 7},         // single byte
		{[]byte{0xAC, 0x02}, 300}, // multi byte: 300 = 0b1_0010_1100
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01}, // canonical 10-byte max
			math.MaxUint64},
	}
	for _, c := range cases {
		d := New(c.buf)
		v, err := d.Varint()
		if err != nil || v != c.want {
			t.Errorf("varint % x: got %d, %v (want %d)", c.buf, v, err, c.want)
		}
		if !d.Done() {
			t.Errorf("varint % x: decoder should be exhausted", c.buf)
		}
	}
}

func TestVarintTruncatedReturnsErrTruncated(t *testing.T) {
	// Continuation bit set on the last available byte: the value is
	// incomplete, and reporting it as valid would corrupt every later
	// field in the message.
	d := New([]byte{0x80})
	if _, err := d.Varint(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestVarintOverflowRejected(t *testing.T) {
	// An 11-byte varint cannot fit in uint64.
	d := New([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0x01})
	if _, err := d.Varint(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
}

func TestTagSplitsFieldAndWireType(t *testing.T) {
	// Field 2, wire type 2 → tag 0x12 (the classic string field tag).
	d := New([]byte{0x12})
	field, wt, err := d.Tag()
	if err != nil || field != 2 || wt != TypeBytes {
		t.Fatalf("got field=%d wt=%d err=%v", field, wt, err)
	}
}

func TestTagFieldZeroRejected(t *testing.T) {
	// Field 0 is reserved; real encoders never emit it, so its presence
	// means the payload is garbage (often an off-by-one read upstream).
	d := New([]byte{0x00})
	if _, _, err := d.Tag(); !errors.Is(err, ErrFieldZero) {
		t.Fatalf("want ErrFieldZero, got %v", err)
	}
}

func TestTagGroupWireTypesRejected(t *testing.T) {
	for _, tag := range []byte{0x0B, 0x0C} { // field 1, wt 3 and 4
		d := New([]byte{tag})
		if _, _, err := d.Tag(); !errors.Is(err, ErrGroup) {
			t.Fatalf("tag 0x%02x: want ErrGroup, got %v", tag, err)
		}
	}
}

func TestFixedWidthLittleEndian(t *testing.T) {
	d := New([]byte{0x01, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	v, err := d.Fixed64()
	if err != nil || v != 0x0201 {
		t.Fatalf("fixed64: got %#x, %v", v, err)
	}
	d32 := New([]byte{0xFF, 0x00, 0x00, 0x00})
	v32, err := d32.Fixed32()
	if err != nil || v32 != 0xFF {
		t.Fatalf("fixed32: got %#x, %v", v32, err)
	}
}

func TestFixed64Truncated(t *testing.T) {
	d := New([]byte{0x01, 0x02, 0x03})
	if _, err := d.Fixed64(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestDoubleRoundTrip(t *testing.T) {
	bits := math.Float64bits(12.5)
	buf := make([]byte, 8)
	for i := 0; i < 8; i++ {
		buf[i] = byte(bits >> (8 * i))
	}
	d := New(buf)
	v, err := d.Double()
	if err != nil || v != 12.5 {
		t.Fatalf("got %v, %v", v, err)
	}
}

func TestBytesReadsLengthDelimited(t *testing.T) {
	d := New([]byte{0x03, 'a', 'b', 'c', 0x01})
	b, err := d.Bytes()
	if err != nil || string(b) != "abc" {
		t.Fatalf("got %q, %v", b, err)
	}
	if d.Done() {
		t.Fatal("one byte should remain")
	}
	// String, by contrast, must copy: mutating the source afterwards
	// cannot be allowed to change an already-decoded span name.
	src := []byte{0x02, 'o', 'k'}
	ds := New(src)
	s, err := ds.String()
	if err != nil || s != "ok" {
		t.Fatalf("got %q, %v", s, err)
	}
	src[1] = 'X'
	if s != "ok" {
		t.Fatal("String must copy, not alias")
	}
}

func TestBytesLengthBeyondBufferIsTruncated(t *testing.T) {
	// Length prefix claims 100 bytes but only 2 follow — the classic
	// shape of a cut-off upload; must not panic or over-read.
	d := New([]byte{0x64, 'a', 'b'})
	if _, err := d.Bytes(); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
}

func TestSkipEveryWireType(t *testing.T) {
	// varint 5, fixed64, bytes(2), fixed32 back to back; skipping all
	// four must land exactly at the end of the buffer.
	buf := []byte{
		0x05,
		1, 2, 3, 4, 5, 6, 7, 8,
		0x02, 'h', 'i',
		1, 2, 3, 4,
	}
	d := New(buf)
	for _, wt := range []Type{TypeVarint, TypeFixed64, TypeBytes, TypeFixed32} {
		if err := d.Skip(wt); err != nil {
			t.Fatalf("skip %d: %v", wt, err)
		}
	}
	if !d.Done() {
		t.Fatal("skips should consume the whole buffer")
	}
}
