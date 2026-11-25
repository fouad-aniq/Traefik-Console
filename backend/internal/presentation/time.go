package presentation

import "time"

// ParseTimestamp accepts the SQLite UTC representation and the API ISO format.
func ParseTimestamp(value string) *time.Time {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if parsed, e := time.Parse(layout, value); e == nil {
			utc := parsed.UTC()
			return &utc
		}
	}
	return nil
}
