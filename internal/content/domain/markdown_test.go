package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBreakLinkAdjacency(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "link injection split apart",
			in:   "  [reset password](https://evil.example/reset_(now))",
			want: "  [reset password] (https://evil.example/reset_(now))",
		},
		{
			name: "image syntax split apart",
			in:   "![proof](https://evil.example/image.png)",
			want: "![proof] (https://evil.example/image.png)",
		},
		{
			name: "nested escape: a link inside link text",
			in:   "[see [docs](https://evil.example)](https://good.example)",
			want: "[see [docs] (https://evil.example)] (https://good.example)",
		},
		{
			name: "reference-style definitions unaffected",
			in:   "  [reset password]: https://evil.example\n\n[reset password]",
			want: "  [reset password]: https://evil.example\n\n[reset password]",
		},
		{name: "normal CJK text untouched", in: "fix login failure", want: "fix login failure"},
		{name: "normal English text untouched", in: "Fix login failure", want: "Fix login failure"},
		{name: "orphaned parentheses untouched", in: "value (in parens) [tag]", want: "value (in parens) [tag]"},
		{name: "empty string", in: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := BreakLinkAdjacency(tc.in)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, got, BreakLinkAdjacency(got), "idempotent: second processing makes no changes")
			assert.NotContains(t, got, "](", `  output must not keep "](" adjacency`)
		})
	}
}
