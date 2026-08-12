package kernel

import (
	"fmt"
	"testing"
)

func TestIsUniqueViolation(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{fmt.Errorf("UNIQUE constraint failed: tenants.slug"), true},
		{fmt.Errorf(`duplicate key value violates unique constraint "tenants_slug_key"`), true},
		{fmt.Errorf("connection refused"), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := IsUniqueViolation(c.err); got != c.want {
			t.Errorf("IsUniqueViolation(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
