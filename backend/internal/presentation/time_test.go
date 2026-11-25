package presentation

import "testing"

func TestTimestampFormatsAndInvalidValues(t *testing.T) {
	for _, value := range []string{"2026-11-07 00:00:00", "2026-11-07T01:00:00+01:00"} {
		got := ParseTimestamp(value)
		if got == nil || got.Format("2006-01-02T15:04:05Z07:00") != "2026-11-07T00:00:00Z" {
			t.Fatalf("timestamp: %v", got)
		}
	}
	if ParseTimestamp("invalid") != nil {
		t.Fatal("invalid timestamp accepted")
	}
}
