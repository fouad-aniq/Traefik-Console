package db

import (
	"encoding/json"
	"fmt"
)

// SQL NULL and JSON null have different meanings; retain SQL NULL as absent.
type rawScanner struct{ dst *json.RawMessage }

func (s rawScanner) Scan(v any) error {
	switch x := v.(type) {
	case nil:
		*s.dst = nil
		return nil
	case string:
		*s.dst = append((*s.dst)[:0], x...)
	case []byte:
		*s.dst = append((*s.dst)[:0], x...)
	default:
		return fmt.Errorf("unsupported JSON persistence value")
	}
	if !json.Valid(*s.dst) {
		return fmt.Errorf("invalid persisted JSON")
	}
	return nil
}
