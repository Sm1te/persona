// Package shellcmd reads shell command lines the way a POSIX shell splits them, before it expands anything: nothing
// is expanded or run. The node's command gates and launcher lists and the manager's reading of a pasted configuration
// share it.
package shellcmd

import "strings"

// Segment is one simple command of a command line.
type Segment struct {
	Words    []string // quotes removed
	Text     string   // as written
	Subst    bool     // holds $(…), `…` or <(…): it runs something else too
	ArgSubst bool     // the substitution is in a command word, not only a redirect target
	Nested   []string // command substitutions, classified separately without executing them
	Redirect string   // its redirections, e.g. "> /tmp/x 2>&1"
}

// Split splits a command line at |, ||, &&, ;, & and newlines outside quotes, each part into words. ok is false for
// what it does not follow (heredocs, subshells and groups, unbalanced quotes): a caller then decides nothing.
func Split(cmd string) (segs []Segment, ok bool) {
	r := []rune(cmd)
	var seg Segment
	var word strings.Builder
	inWord := false
	start := 0
	endWord := func() {
		if inWord {
			seg.Words = append(seg.Words, word.String())
			word.Reset()
			inWord = false
		}
	}
	endSeg := func(at int) {
		endWord()
		seg.Text = strings.TrimSpace(string(r[start:at]))
		if len(seg.Words) > 0 || seg.Redirect != "" {
			segs = append(segs, seg)
		}
		seg = Segment{}
		start = at
	}
	// skipGroup copies $( … ) / `…` / <( … ) into the word, nesting and quotes respected; false when it never closes.
	skipGroup := func(i int, open, close rune) (int, bool) {
		depth := 0
		begin := i + 1
		for ; i < len(r); i++ {
			word.WriteRune(r[i])
			switch {
			case r[i] == '\\' && i+1 < len(r):
				i++
				word.WriteRune(r[i])
			case r[i] == '\'' && open != '`':
				for i++; i < len(r) && r[i] != '\''; i++ {
					word.WriteRune(r[i])
				}
				if i < len(r) {
					word.WriteRune(r[i])
				}
			case r[i] == open && (open != close || depth == 0):
				depth++
			case r[i] == close:
				depth--
				if depth == 0 {
					seg.Nested = append(seg.Nested, string(r[begin:i]))
					return i, true
				}
			}
		}
		return i, false
	}
	for i := 0; i < len(r); i++ {
		c := r[i]
		switch {
		case c == '\\':
			if i+1 < len(r) {
				i++
				if r[i] != '\n' { // a line continuation joins the lines
					word.WriteRune(r[i])
					inWord = true
				}
			}
		case c == '\'':
			inWord = true
			j := i + 1
			for j < len(r) && r[j] != '\'' {
				j++
			}
			if j >= len(r) {
				return nil, false
			}
			word.WriteString(string(r[i+1 : j]))
			i = j
		case c == '"':
			inWord = true
			j := i + 1
			for ; j < len(r) && r[j] != '"'; j++ {
				switch {
				case r[j] == '\\' && j+1 < len(r) && strings.ContainsRune("\"\\$`\n", r[j+1]):
					j++
					word.WriteRune(r[j])
				case r[j] == '$' && j+1 < len(r) && r[j+1] == '(', r[j] == '`':
					seg.Subst, seg.ArgSubst = true, true
					at := j
					open, close := rune('`'), rune('`')
					if r[j] == '$' {
						word.WriteRune('$')
						at++
						open, close = '(', ')'
					}
					end, closed := skipGroup(at, open, close)
					if !closed {
						return nil, false
					}
					j = end
				default:
					word.WriteRune(r[j])
				}
			}
			if j >= len(r) {
				return nil, false
			}
			i = j
		case c == '$' && i+1 < len(r) && r[i+1] == '(':
			seg.Subst, seg.ArgSubst, inWord = true, true, true
			word.WriteRune('$')
			end, closed := skipGroup(i+1, '(', ')')
			if !closed {
				return nil, false
			}
			i = end
		case c == '`':
			seg.Subst, seg.ArgSubst, inWord = true, true, true
			end, closed := skipGroup(i, '`', '`')
			if !closed {
				return nil, false
			}
			i = end
		case (c == '<' || c == '>') && i+1 < len(r) && r[i+1] == '(':
			return nil, false // process substitution
		case c == '<' && i+1 < len(r) && r[i+1] == '<':
			return nil, false // heredoc / herestring
		case c == '>' || c == '<' || (c == '&' && i+1 < len(r) && r[i+1] == '>'):
			// A redirection: an fd number just before it belongs to it; then its target.
			op := ""
			if inWord && strings.Trim(word.String(), "0123456789") == "" {
				op = word.String()
				word.Reset()
				inWord = false
			}
			endWord()
			for i < len(r) && strings.ContainsRune("<>&", r[i]) {
				op += string(r[i])
				i++
			}
			for i < len(r) && (r[i] == ' ' || r[i] == '\t') {
				i++
			}
			for i < len(r) && !strings.ContainsRune(" \t\n|;&<>()", r[i]) {
				if r[i] == '$' && i+1 < len(r) && r[i+1] == '(' || r[i] == '`' {
					seg.Subst = true
					open, close := '`', '`'
					at := i
					if r[i] == '$' {
						at++
						open, close = '(', ')'
					}
					end, closed := skipGroup(at, open, close)
					if !closed {
						return nil, false
					}
					i = end + 1
					word.Reset()
					inWord = false
					continue
				}
				op += string(r[i])
				i++
			}
			// "2>&1": the target was the fd after &.
			if strings.HasSuffix(op, "&") && i < len(r) && r[i] >= '0' && r[i] <= '9' {
				op += string(r[i])
				i++
			}
			i--
			seg.Redirect = strings.TrimSpace(seg.Redirect + " " + op)
		case c == '|' || c == ';' || c == '&' || c == '\n':
			endSeg(i)
			if i+1 < len(r) && (c == '|' && r[i+1] == '|' || c == '&' && r[i+1] == '&') {
				i++
			}
			start = i + 1
		case c == '(' || c == ')' || (c == '{' || c == '}') && !inWord:
			return nil, false // subshells and groups
		case c == '#' && !inWord:
			for i < len(r) && r[i] != '\n' {
				i++
			}
			i--
		case c == ' ' || c == '\t':
			endWord()
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	endSeg(len(r))
	return segs, true
}
