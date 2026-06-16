// Package appsettings persists small machine-local app preferences (e.g. whether
// to prevent sleep) as JSON next to the data dir.
package appsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Settings are the persisted preferences.
type Settings struct {
	PreventSleep bool `json:"prevent_sleep"`
}

// Default returns the default settings (prevent sleep ON — this is a logger).
func Default() Settings { return Settings{PreventSleep: true} }

// Path returns the settings file path under dir.
func Path(dir string) string { return filepath.Join(dir, "settings.json") }

// Load reads settings from path, returning Default() if missing or unreadable.
func Load(path string) Settings {
	data, err := os.ReadFile(path)
	if err != nil {
		return Default()
	}
	s := Default()
	if json.Unmarshal(data, &s) != nil {
		return Default()
	}
	return s
}

// Save writes settings to path as JSON.
func Save(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
