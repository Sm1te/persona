package shellcmd

import "strings"

// Line is one command line of a text as the shell reads it: a line ending in a backslash, or with a quote that
// closes on a later line, joined with the lines after it (Split reads the joins). Start is its first line, 1-based.
type Line struct {
	Text  string
	Start int
}

// Lines is text's command lines, text's lines split at "\n". A quote that never closes keeps only its own line: one
// stray apostrophe does not swallow what follows. Each line is read once per command line it may join.
func Lines(text string) []Line {
	raw := strings.Split(text, "\n")
	var out []Line
	for i := 0; i < len(raw); i++ {
		var st lineState
		st.read(raw[i])
		end := i
		for end+1 < len(raw) && (st.cont() || st.quote != 0) {
			end++
			st.read("\n")
			st.read(raw[end])
		}
		if st.quote != 0 {
			end = i
		}
		out = append(out, Line{Text: strings.Join(raw[i:end+1], "\n"), Start: i + 1})
		i = end
	}
	return out
}

// lineState is how the text read so far ends: inside a quote, after a backslash (the next byte is taken as it is), in
// a word.
type lineState struct {
	quote     byte
	esc, word bool
}

// cont: the text ends in a backslash that continues it on the next line.
func (st *lineState) cont() bool { return st.esc && st.quote == 0 }

// read goes on over s. A # starting a word outside quotes comments out the rest of its line.
func (st *lineState) read(s string) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case st.esc:
			st.esc = false
		case st.quote == '\'':
			if c == '\'' {
				st.quote = 0
			}
		case st.quote == '"':
			if c == '\\' {
				st.esc = true
			} else if c == '"' {
				st.quote = 0
			}
		case c == '\\':
			st.esc, st.word = true, true
		case c == '\'' || c == '"':
			st.quote, st.word = c, true
		case c == '#' && !st.word:
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		case strings.IndexByte(" \t\n;|&", c) >= 0:
			st.word = false
		default:
			st.word = true
		}
	}
}
