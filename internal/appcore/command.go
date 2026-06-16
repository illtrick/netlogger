package appcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
)

type command struct {
	Cmd string `json:"cmd"`
}

// commandHandler accepts POST {"cmd":"reset"} and invokes reset.
func commandHandler(reset func()) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var c command
		_ = json.NewDecoder(r.Body).Decode(&c)
		if c.Cmd == "reset" {
			reset()
		}
		w.WriteHeader(http.StatusOK)
	}
}

// postCommand POSTs a command to a peer's /api/command.
func postCommand(client *http.Client, baseURL, cmd string) error {
	body, _ := json.Marshal(command{Cmd: cmd})
	resp, err := client.Post(baseURL+"/api/command", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("command: status %d", resp.StatusCode)
	}
	return nil
}
