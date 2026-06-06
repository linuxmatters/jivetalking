package processor

import "testing"

func TestEscapeFilterGraphOptionValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "clean path is a no-op",
			in:   "/tmp/podcast/.loudnorm-1234.tmp.json",
			want: "/tmp/podcast/.loudnorm-1234.tmp.json",
		},
		{
			name: "colon is escaped",
			in:   "/tmp/a:b/stats.json",
			want: `/tmp/a\:b/stats.json`,
		},
		{
			name: "comma is escaped",
			in:   "/tmp/a,b/stats.json",
			want: `/tmp/a\,b/stats.json`,
		},
		{
			name: "backslash is escaped",
			in:   `/tmp/a\b/stats.json`,
			want: `/tmp/a\\b/stats.json`,
		},
		{
			name: "single quote is escaped",
			in:   "/tmp/a'b/stats.json",
			want: `/tmp/a\'b/stats.json`,
		},
		{
			name: "open bracket is escaped",
			in:   "/tmp/a[b/stats.json",
			want: `/tmp/a\[b/stats.json`,
		},
		{
			name: "close bracket is escaped",
			in:   "/tmp/a]b/stats.json",
			want: `/tmp/a\]b/stats.json`,
		},
		{
			name: "all special chars together",
			in:   `/t:m,p\['x']/stats.json`,
			want: `/t\:m\,p\\\[\'x\'\]/stats.json`,
		},
		{
			name: "empty string is a no-op",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := escapeFilterGraphOptionValue(tt.in); got != tt.want {
				t.Errorf("escapeFilterGraphOptionValue(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
