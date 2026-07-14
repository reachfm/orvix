package backup

import (
	"testing"
)

func TestParseStoredTime_DirectValues(t *testing.T) {
	cases := []string{
		"2026-07-01T19:46:27.4017499Z",      // what sqlite actually stored
		"2026-07-01T19:46:27Z",              // RFC3339 no nanos
		"2026-07-01 19:46:27.4017499+00:00", // space separator with nanos
		"2026-07-01 19:46:27+00:00",         // space separator no nanos
		"2026-07-01 19:46:27",               // bare datetime
		"",                                  // empty
		"not a date",                        // garbage
	}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			got, err := parseStoredTime(c)
			t.Logf("input=%q -> got=%v err=%v debug=%s", c, got, err, debugParse(c))
		})
	}
}
