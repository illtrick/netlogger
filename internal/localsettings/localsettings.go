// Package localsettings stores per-machine preferences that must NOT be shared
// with peers. Unlike the network config (which the coordinator serves to other
// nodes), these never leave the box — e.g. which directory this node keeps its
// SQLite database in.
package localsettings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Settings are machine-local preferences.
type Settings struct {
	// DBDir overrides the directory that holds this node's SQLite database.
	// Empty means "use the default data dir".
	DBDir string `json:"db_dir,omitempty"`
	// Listen overrides the control server bind address (host:port). Empty means
	// "use the default". Set to 0.0.0.0:8088 to make this node reachable by
	// peers; 127.0.0.1:8088 keeps it loopback-only.
	Listen string `json:"listen,omitempty"`
}

// Path returns the settings file location under the fixed data dir. The settings
// file itself always lives in the default data dir, even when it points the
// database elsewhere.
func Path(dataDir string) string { return filepath.Join(dataDir, "settings.json") }

// Load reads settings from path. A missing file yields empty settings with no
// error, so a first run just uses the defaults.
func Load(path string) (*Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Settings{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse settings: %w", err)
	}
	return &s, nil
}

// Save writes settings to path as indented JSON, creating the directory if needed.
func Save(path string, s *Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir for settings: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// ResolveDBPath returns the full path to the database file, given the default
// data dir, the db filename, and loaded settings. A non-empty DBDir override
// wins over the default.
func ResolveDBPath(defaultDir, dbName string, s *Settings) string {
	dir := defaultDir
	if s != nil && s.DBDir != "" {
		dir = s.DBDir
	}
	return filepath.Join(dir, dbName)
}

// ResolveListen returns the control server bind address. An explicit flag value
// wins; otherwise a saved Listen override; otherwise the default. This lets the
// GUI set the bind address (persisted in settings) without a command-line flag.
func ResolveListen(flagVal, defaultVal string, s *Settings) string {
	if flagVal != "" {
		return flagVal
	}
	if s != nil && s.Listen != "" {
		return s.Listen
	}
	return defaultVal
}
