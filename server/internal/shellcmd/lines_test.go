package shellcmd

import (
	"reflect"
	"testing"
)

func TestLines(t *testing.T) {
	for _, c := range []struct {
		text string
		want []Line
	}{
		{"a\n\nb", []Line{{"a", 1}, {"", 2}, {"b", 3}}},
		// A continuation, and a quote over several lines (a curl body), each one command line.
		{"curl x \\\n  -d '{\n  \"model\": \"m\"\n}'\nnext", []Line{{"curl x \\\n  -d '{\n  \"model\": \"m\"\n}'", 1}, {"next", 5}}},
		{"alias a=\"A=1\nclaude\"\nb", []Line{{"alias a=\"A=1\nclaude\"", 1}, {"b", 3}}},
		// An escaped quote, and a quote in a comment, open nothing.
		{"a \\' b\n# don't\nc", []Line{{"a \\' b", 1}, {"# don't", 2}, {"c", 3}}},
		{"x=it\\'s # it's\ny", []Line{{"x=it\\'s # it's", 1}, {"y", 2}}},
		// A quote that never closes is its own line only.
		{"it's\nb\nc", []Line{{"it's", 1}, {"b", 2}, {"c", 3}}},
		{"a \\", []Line{{"a \\", 1}}},
	} {
		if got := Lines(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v, want %+v", c.text, got, c.want)
		}
	}
}
