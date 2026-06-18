package installer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitWords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"space separated", "Running Running Running", []string{"Running", "Running", "Running"}},
		{"mixed whitespace", "  Running\tPending\n", []string{"Running", "Pending"}},
		{"single word", "single", []string{"single"}},
		{"empty string", "", nil},
		{"only whitespace", "   \t\n", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splitWords(tt.in))
		})
	}
}
