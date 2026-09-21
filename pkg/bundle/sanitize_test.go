package bundle

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeBundleName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"argo-cd", "argo-cd"},
		{"../../etc/passwd", "etc_passwd"},
		{"a/b\\c", "a_b_c"},
		{"..", "unknown"}, // falls back to unknown because the name is empty after sanitization
		{"normal-1.0.0", "normal-1.0.0"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := safeBundleName(c.in)
			assert.Equal(t, c.want, got)
			assert.NotContains(t, got, "..", "result must not contain traversal")
			assert.NotContains(t, got, "/", "result must not contain path separator")
		})
	}
}
