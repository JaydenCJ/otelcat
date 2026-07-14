// Test-only protobuf encoder: a tiny wire-format writer used to build
// OTLP payloads byte-for-byte, so the decoder is exercised against real
// encodings instead of hand-pasted hex blobs. It lives in the test
// binary only and never ships.
package otlp

import "encoding/hex"

type enc struct{ b []byte }

func (e *enc) varint(v uint64) {
	for v >= 0x80 {
		e.b = append(e.b, byte(v)|0x80)
		v >>= 7
	}
	e.b = append(e.b, byte(v))
}

func (e *enc) tag(field, wt int) { e.varint(uint64(field)<<3 | uint64(wt)) }

func (e *enc) blob(field int, b []byte) {
	e.tag(field, 2)
	e.varint(uint64(len(b)))
	e.b = append(e.b, b...)
}

func (e *enc) str(field int, s string) { e.blob(field, []byte(s)) }

func (e *enc) uvarint(field int, v uint64) {
	e.tag(field, 0)
	e.varint(v)
}

func (e *enc) fixed64(field int, v uint64) {
	e.tag(field, 1)
	for i := 0; i < 8; i++ {
		e.b = append(e.b, byte(v>>(8*i)))
	}
}

func (e *enc) msg(field int, fill func(*enc)) {
	var inner enc
	fill(&inner)
	e.blob(field, inner.b)
}

func mustHex(t interface{ Fatalf(string, ...any) }, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex fixture %q: %v", s, err)
	}
	return b
}

// strAttr writes a KeyValue{key, stringValue} into the given field.
func strAttr(e *enc, field int, key, val string) {
	e.msg(field, func(kv *enc) {
		kv.str(1, key)
		kv.msg(2, func(v *enc) { v.str(1, val) })
	})
}

// spanRequest wraps one encoded span (plus optional resource/scope
// fill) into a full ExportTraceServiceRequest.
func spanRequest(fillResource func(*enc), fillScope func(*enc), fillSpans func(*enc)) []byte {
	var e enc
	e.msg(1, func(rs *enc) {
		if fillResource != nil {
			rs.msg(1, fillResource)
		}
		rs.msg(2, func(ss *enc) {
			if fillScope != nil {
				ss.msg(1, fillScope)
			}
			fillSpans(ss)
		})
	})
	return e.b
}
