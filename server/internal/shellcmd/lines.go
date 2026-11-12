package shellcmd

import "strings"

// Line is one command line of a text as the shell reads it: a line ending in a backslash, or with a quote that
// closes on a later line, joined with the lines after it (Split reads the joins). Start is its first line, 1-based.
type Line struct {
	Text  string
	Start int
}

// Lines is text's command lines, text's lines split at "\n". A quote that never closes keeps only its own line: one
// stray apostrophe does not swallow what follows.
func Lines(text string) []Line {
	raw := strings.Split(text, "\n")
	var out []Line
	for i := 0; i < len(raw); i++ {
		end, joined := i, raw[i]
		for end+1 < len(raw) {
			cont, quoted := pending(joined)
			if !cont && !quoted {
				break
			}
			end++
			joined += "\n" + raw[end]
		}
		if _, quoted := pending(joined); quoted {
			end, joined = i, raw[i]
		}
		out = append(out, Line{Text: joined, Start: i + 1})
		i = end
	}
	return out
}

// pending says how s ends: cont, in a backslash that continues it on the next line; quoted, inside a quote. A #
// starting a word outside quotes comments out the rest of its line.
func pending(s string) (cont, quoted bool) {
	var quote byte
	word := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
			}
		case quote == '"':
			if c == '\\' {
				i++
			} else if c == '"' {
				quote = 0
			}
		case c == '\\':
			if i+1 == len(s) {
				return true, false
			}
			i, word = i+1, true
		case c == '\'' || c == '"':
			quote, word = c, true
		case c == '#' && !word:
			for i+1 < len(s) && s[i+1] != '\n' {
				i++
			}
		case strings.IndexByte(" \t\n;|&", c) >= 0:
			word = false
		default:
			word = true
		}
	}
	return false, quote != 0
}
