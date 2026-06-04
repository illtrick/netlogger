package sysinfo

import (
	"path/filepath"
	"testing"
)

func TestDetectVersionMissingBinaryReturnsEmpty(t *testing.T) {
	if v := detectVersion("definitely-not-a-real-binary-xyz123", "--version"); v != "" {
		t.Fatalf("want empty for missing binary, got %q", v)
	}
}

func TestDataDirWritable(t *testing.T) {
	if !DataDirWritable(t.TempDir()) {
		t.Fatal("temp dir should be writable")
	}
	if DataDirWritable(filepath.Join(t.TempDir(), "does", "not", "exist")) {
		t.Fatal("nonexistent nested dir should not be writable")
	}
}
