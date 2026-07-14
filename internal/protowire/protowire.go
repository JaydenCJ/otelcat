// Package protowire implements the low-level protobuf wire format —
// just enough of it, hand-written, to decode OTLP export requests
// without pulling in a protobuf runtime. It knows nothing about OTLP;
// it only reads tags, varints, fixed-width scalars and length-delimited
// fields, and can skip anything it does not recognize.
package protowire

import (
	"errors"
	"fmt"
	"math"
)

// Type is a protobuf wire type as encoded in the low 3 bits of a tag.
type Type uint8

// The wire types defined by the protobuf encoding. Groups (3 and 4) are
// long-deprecated and never emitted by OTLP SDKs; the decoder rejects them.
const (
	TypeVarint     Type = 0
	TypeFixed64    Type = 1
	TypeBytes      Type = 2
	TypeStartGroup Type = 3
	TypeEndGroup   Type = 4
	TypeFixed32    Type = 5
)

// Sentinel errors returned by the decoder. Callers wrap them with field
// context; tests assert on them with errors.Is.
var (
	ErrTruncated = errors.New("truncated message")
	ErrOverflow  = errors.New("varint overflows 64 bits")
	ErrGroup     = errors.New("deprecated group wire type is not supported")
	ErrFieldZero = errors.New("field number 0 is invalid")
)

// Decoder is a forward-only reader over one serialized message.
// The zero value is not usable; construct with New. Decoders are cheap
// (two words) and are created per nested message.
type Decoder struct {
	buf []byte
	pos int
}

// New returns a Decoder reading from buf. The Decoder does not copy buf;
// byte slices returned by Bytes alias it.
func New(buf []byte) *Decoder { return &Decoder{buf: buf} }

// Done reports whether the whole buffer has been consumed.
func (d *Decoder) Done() bool { return d.pos >= len(d.buf) }

// Tag reads the next field tag, returning the field number and wire type.
func (d *Decoder) Tag() (int, Type, error) {
	v, err := d.Varint()
	if err != nil {
		return 0, 0, err
	}
	field := int(v >> 3)
	wt := Type(v & 7)
	if field == 0 {
		return 0, 0, ErrFieldZero
	}
	if wt == TypeStartGroup || wt == TypeEndGroup {
		return 0, 0, ErrGroup
	}
	if wt > TypeFixed32 {
		return 0, 0, fmt.Errorf("invalid wire type %d", wt)
	}
	return field, wt, nil
}

// Varint reads one base-128 varint (up to 10 bytes).
func (d *Decoder) Varint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if d.pos >= len(d.buf) {
			return 0, ErrTruncated
		}
		b := d.buf[d.pos]
		d.pos++
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			// The 10th byte may only contribute a single bit.
			if shift == 63 && b > 1 {
				return 0, ErrOverflow
			}
			return v, nil
		}
	}
	return 0, ErrOverflow
}

// Fixed64 reads 8 little-endian bytes as a uint64.
func (d *Decoder) Fixed64() (uint64, error) {
	if d.pos+8 > len(d.buf) {
		return 0, ErrTruncated
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(d.buf[d.pos+i]) << (8 * i)
	}
	d.pos += 8
	return v, nil
}

// Fixed32 reads 4 little-endian bytes as a uint32.
func (d *Decoder) Fixed32() (uint32, error) {
	if d.pos+4 > len(d.buf) {
		return 0, ErrTruncated
	}
	var v uint32
	for i := 0; i < 4; i++ {
		v |= uint32(d.buf[d.pos+i]) << (8 * i)
	}
	d.pos += 4
	return v, nil
}

// Double reads a fixed64 and reinterprets it as an IEEE-754 double.
func (d *Decoder) Double() (float64, error) {
	v, err := d.Fixed64()
	if err != nil {
		return 0, err
	}
	return math.Float64frombits(v), nil
}

// Bytes reads one length-delimited field and returns the payload,
// aliasing the underlying buffer (no copy).
func (d *Decoder) Bytes() ([]byte, error) {
	n, err := d.Varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(d.buf)-d.pos) {
		return nil, ErrTruncated
	}
	b := d.buf[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return b, nil
}

// String reads a length-delimited field as a string (one copy).
func (d *Decoder) String() (string, error) {
	b, err := d.Bytes()
	return string(b), err
}

// Skip discards the value of a field with the given wire type. It lets
// the OTLP decoder ignore fields added by future protocol revisions.
func (d *Decoder) Skip(wt Type) error {
	switch wt {
	case TypeVarint:
		_, err := d.Varint()
		return err
	case TypeFixed64:
		_, err := d.Fixed64()
		return err
	case TypeFixed32:
		_, err := d.Fixed32()
		return err
	case TypeBytes:
		_, err := d.Bytes()
		return err
	default:
		return fmt.Errorf("cannot skip wire type %d", wt)
	}
}
