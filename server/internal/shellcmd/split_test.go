package shellcmd

import (
	"reflect"
	"testing"
)

func TestSplit(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		want []Segment
		ok   bool
	}{
		{`A="x y" claude --model 'm 1' # note`, []Segment{{Words: []string{"A=x y", "claude", "--model", "m 1"}, Text: `A="x y" claude --model 'm 1' # note`}}, true},
		{"a \\\n  b; c && d | e", []Segment{
			{Words: []string{"a", "b"}, Text: "a \\\n  b"}, {Words: []string{"c"}, Text: "c"}, {Words: []string{"d"}, Text: "d"},
			{Words: []string{"e"}, Text: "e"},
		}, true},
		{`echo "$(id)" > /tmp/x 2>&1`, []Segment{{Words: []string{"echo", "$(id)"}, Text: `echo "$(id)" > /tmp/x 2>&1`, Subst: true,
			ArgSubst: true, Nested: []string{"id"}, Redirect: ">/tmp/x 2>&1"}}, true},
		{`echo 'open`, nil, false},
		{`(cd x)`, nil, false},
		{`cat <<EOF`, nil, false},
	} {
		got, ok := Split(c.cmd)
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v %v, want %+v %v", c.cmd, got, ok, c.want, c.ok)
		}
	}
}
