// Package applog routes the standard logger to a file in the data dir, since a
// -H windowsgui build has no console to print to.
package applog

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Init opens (appends to) <dir>/netlogger.log and routes log output to it.
// The caller closes the returned file at shutdown.
func Init(dir string) (*os.File, error) {
	path := filepath.Join(dir, "netlogger.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	return f, nil
}
