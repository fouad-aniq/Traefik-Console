package plugins

import "testing"

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
		ok   bool
	}{
		{"v1.0.0", "v1.0.0", 0, true},
		{"v1.9.0", "v1.10.0", -1, true},
		{"v2.0.0", "v1.99.99", 1, true},
		{"v1.2.0-rc.1", "v1.2.0", -1, true},
		{"v1.2.0", "v1.2.0-rc.1", 1, true},
		{"v1.2.0-rc.2", "v1.2.0-rc.10", -1, true},
		{"v1.2.0-alpha", "v1.2.0-beta", -1, true},
		{"v1.2.0-1", "v1.2.0-alpha", -1, true},
		{"v1.2.0-rc", "v1.2.0-rc.1", -1, true},
		{"1.2.0", "v1.2.0", 0, false},
		{"v1.2", "v1.2.0", 0, false},
		{"", "v1.2.0", 0, false},
		{"v1.2.0-", "v1.2.0", 0, false},
		{"v99999999999999999999.0.0", "v1.0.0", 1, true},
	} {
		got, ok := compareVersions(tc.a, tc.b)
		if got != tc.want || ok != tc.ok {
			t.Errorf("compareVersions(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, got, ok, tc.want, tc.ok)
		}
	}
}
