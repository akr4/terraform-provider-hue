package hue

import (
	"encoding/json"
	"fmt"
)

// BehaviorInstance keeps script-specific configuration intact. Runtime state
// and dependees are intentionally not part of update requests.
type BehaviorInstance struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	ScriptID      string          `json:"script_id"`
	Enabled       bool            `json:"enabled"`
	Configuration json.RawMessage `json:"configuration"`
	Metadata      Metadata        `json:"metadata"`
	Status        string          `json:"status"`
	LastError     string          `json:"last_error"`
}

func ValidateConfiguration(raw []byte) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return fmt.Errorf("configuration must be a JSON object; use jsonencode({...})")
	}
	return nil
}
