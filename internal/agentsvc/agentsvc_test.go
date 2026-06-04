package agentsvc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"netlogger/internal/localsettings"
)

func TestHandleSettingsGet(t *testing.T) {
	dir := t.TempDir()
	p := &Program{
		SettingsPath:   localsettings.Path(dir),
		DBPath:         filepath.Join(dir, "netlogger.db"),
		DefaultDataDir: dir,
		Interactive:    true,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	rec := httptest.NewRecorder()
	p.handleSettings(rec, req)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["db_dir"] != dir {
		t.Fatalf("db_dir = %v, want %v", got["db_dir"], dir)
	}
	if got["default_dir"] != dir {
		t.Fatalf("default_dir = %v, want %v", got["default_dir"], dir)
	}
	if got["configured"] != "" {
		t.Fatalf("configured = %v, want empty", got["configured"])
	}
}

func TestHandleSettingsPostPersistsWritableDir(t *testing.T) {
	dir := t.TempDir()
	newDir := filepath.Join(t.TempDir(), "data") // does not exist yet; handler must create it
	p := &Program{
		SettingsPath:   localsettings.Path(dir),
		DBPath:         filepath.Join(dir, "netlogger.db"),
		DefaultDataDir: dir,
		Interactive:    true,
	}
	body := strings.NewReader(`{"db_dir":` + jsonStr(newDir) + `}`)
	req := httptest.NewRequest(http.MethodPost, "/api/settings", body)
	rec := httptest.NewRecorder()
	p.handleSettings(rec, req)

	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["ok"] != true {
		t.Fatalf("ok = %v, want true (error: %v)", got["ok"], got["error"])
	}
	if got["restart_required"] != true {
		t.Fatalf("restart_required = %v, want true", got["restart_required"])
	}
	// Persisted to settings.json and reloadable.
	ls, err := localsettings.Load(p.SettingsPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ls.DBDir != newDir {
		t.Fatalf("persisted DBDir = %q, want %q", ls.DBDir, newDir)
	}
}

func TestHandleSettingsPostEmptyClearsOverride(t *testing.T) {
	dir := t.TempDir()
	p := &Program{
		SettingsPath:   localsettings.Path(dir),
		DBPath:         filepath.Join(dir, "netlogger.db"),
		DefaultDataDir: dir,
		Interactive:    true,
	}
	// Pre-seed an override.
	if err := localsettings.Save(p.SettingsPath, &localsettings.Settings{DBDir: dir}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"db_dir":""}`))
	rec := httptest.NewRecorder()
	p.handleSettings(rec, req)

	ls, err := localsettings.Load(p.SettingsPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ls.DBDir != "" {
		t.Fatalf("DBDir = %q, want cleared", ls.DBDir)
	}
}

func TestHandleSettingsPostListenValidates(t *testing.T) {
	dir := t.TempDir()
	p := &Program{
		SettingsPath:   localsettings.Path(dir),
		DBPath:         filepath.Join(dir, "netlogger.db"),
		DefaultDataDir: dir,
		Listen:         "0.0.0.0:8088",
		Interactive:    true,
	}
	// Bad address is rejected and nothing is persisted.
	req := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"listen":"not-an-address"}`))
	rec := httptest.NewRecorder()
	p.handleSettings(rec, req)
	var bad map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &bad)
	if bad["ok"] != false {
		t.Fatalf("expected rejection of bad address, got %v", bad)
	}

	// Valid address persists.
	req = httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(`{"listen":"0.0.0.0:8088"}`))
	rec = httptest.NewRecorder()
	p.handleSettings(rec, req)
	ls, err := localsettings.Load(p.SettingsPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if ls.Listen != "0.0.0.0:8088" {
		t.Fatalf("persisted Listen = %q, want 0.0.0.0:8088", ls.Listen)
	}
	// db_dir was not in the body, so it must remain unset.
	if ls.DBDir != "" {
		t.Fatalf("DBDir should be untouched, got %q", ls.DBDir)
	}
}

// jsonStr produces a JSON-quoted string literal so path separators (backslashes
// on Windows) are escaped correctly in the request body.
func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
