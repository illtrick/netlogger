package mesh

import "testing"

func TestOffsetsStoreAndGet(t *testing.T) {
	o := NewOffsets()
	if _, ok := o.Get("ncase"); ok {
		t.Fatal("unknown agent should not be present")
	}
	o.Set("ncase", Offset{OffsetUS: 1234, RTTus: 200, Reliable: true})
	got, ok := o.Get("ncase")
	if !ok || got.OffsetUS != 1234 {
		t.Fatalf("get wrong: %+v ok=%v", got, ok)
	}
}
