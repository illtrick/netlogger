package applog

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitCreatesFileAndCapturesOutput(t *testing.T) {
	dir := t.TempDir()
	f, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { f.Close(); log.SetOutput(os.Stderr) }()

	log.Print("hello-netlogger")

	data, err := os.ReadFile(filepath.Join(dir, "netlogger.log"))
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hello-netlogger") {
		t.Fatalf("log file missing message, got: %q", string(data))
	}
}

func TestInitAppends(t *testing.T) {
	dir := t.TempDir()
	f1, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	log.Print("line-one")
	f1.Close()
	f2, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { f2.Close(); log.SetOutput(os.Stderr) }()
	log.Print("line-two")
	data, _ := os.ReadFile(filepath.Join(dir, "netlogger.log"))
	s := string(data)
	if !strings.Contains(s, "line-one") || !strings.Contains(s, "line-two") {
		t.Fatalf("expected both lines appended, got: %q", s)
	}
}
