package main

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestWriteICNS(t *testing.T) {
	var buf bytes.Buffer
	// Two fake "PNG" payloads are fine — writeICNS only frames bytes.
	err := writeICNS(&buf, []icnsChunk{
		{"ic07", []byte("png-a")},
		{"ic08", []byte("png-bb")},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	if string(b[:4]) != "icns" {
		t.Fatalf("magic = %q", b[:4])
	}
	total := binary.BigEndian.Uint32(b[4:8])
	if int(total) != len(b) {
		t.Errorf("total len field = %d, actual %d", total, len(b))
	}
	if string(b[8:12]) != "ic07" {
		t.Errorf("first chunk type = %q", b[8:12])
	}
	c1len := binary.BigEndian.Uint32(b[12:16])
	if int(c1len) != 8+len("png-a") {
		t.Errorf("chunk1 len = %d, want %d", c1len, 8+len("png-a"))
	}
}
