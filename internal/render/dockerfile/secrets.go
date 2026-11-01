package dockerfile

import (
	"fmt"
	"strings"
)

// substituteSecrets replaces secret values in a shell script with references
// to the environment variables BuildKit sets from secret mounts, so that the
// Dockerfile never contains them. The replacement depends on the quoting
// context: "${VAR}" outside quotes, ${VAR} in double quotes, '"${VAR}"' in
// single quotes; quoted here-documents that contain a secret are turned into
// unquoted ones.
func substituteSecrets(script string, secrets []Secret) (string, []Secret, error) {
	l := &lexer{src: script, secrets: secrets, used: map[string]bool{}}
	out, err := l.run()
	if err != nil {
		return "", nil, err
	}
	var used []Secret
	for _, s := range secrets {
		if l.used[s.ID] {
			used = append(used, s)
		}
	}
	return out, used, nil
}

type heredoc struct {
	delim  string
	quoted bool
	strip  bool // <<-
	at     int  // position of the delimiter word in the output
	width  int  // length of the delimiter word as written
}

type lexer struct {
	src     string
	secrets []Secret
	used    map[string]bool
	out     strings.Builder
	pending []heredoc
}

// match returns the secret starting at i, if any.
func (l *lexer) match(i int) *Secret {
	if i > 0 && strings.ContainsRune("$_{", rune(l.src[i-1])) {
		return nil
	}
	for k := range l.secrets {
		v := l.secrets[k].Value
		if v != "" && strings.HasPrefix(l.src[i:], v) {
			return &l.secrets[k]
		}
	}
	return nil
}

func (l *lexer) run() (string, error) {
	const (
		normal = iota
		single
		double
	)
	state := normal
	src := l.src
	for i := 0; i < len(src); {
		if s := l.match(i); s != nil {
			l.used[s.ID] = true
			switch state {
			case normal:
				l.out.WriteString(`"${` + s.Env + `}"`)
			case single:
				l.out.WriteString(`'"${` + s.Env + `}"'`)
			case double:
				l.out.WriteString(`${` + s.Env + `}`)
			}
			i += len(s.Value)
			continue
		}
		c := src[i]
		switch state {
		case single:
			if c == '\'' {
				state = normal
			}
		case double:
			if c == '\\' && i+1 < len(src) {
				l.out.WriteString(src[i : i+2])
				i += 2
				continue
			}
			if c == '"' {
				state = normal
			}
		case normal:
			switch {
			case c == '\\' && i+1 < len(src):
				l.out.WriteString(src[i : i+2])
				i += 2
				continue
			case c == '\'':
				state = single
			case c == '"':
				state = double
			case c == '#' && (i == 0 || strings.ContainsRune(" \t\n;", rune(src[i-1]))):
				// comment up to the end of the line
				end := strings.IndexByte(src[i:], '\n')
				if end < 0 {
					end = len(src) - i
				}
				comment := src[i : i+end]
				for _, s := range l.secrets {
					if s.Value != "" && strings.Contains(comment, s.Value) {
						comment = strings.ReplaceAll(comment, s.Value, "${"+s.Env+"}")
						l.used[s.ID] = true
					}
				}
				l.out.WriteString(comment)
				i += end
				continue
			case c == '<' && strings.HasPrefix(src[i:], "<<") && !strings.HasPrefix(src[i:], "<<<"):
				i = l.heredocStart(i)
				continue
			case c == '\n' && len(l.pending) > 0:
				l.out.WriteByte('\n')
				var err error
				i, err = l.heredocBodies(i + 1)
				if err != nil {
					return "", err
				}
				continue
			}
		}
		l.out.WriteByte(c)
		i++
	}
	return l.out.String(), nil
}

// heredocStart parses "<<[-]WORD" at i and remembers the here-document.
func (l *lexer) heredocStart(i int) int {
	src := l.src
	j := i + 2
	h := heredoc{}
	if j < len(src) && src[j] == '-' {
		h.strip = true
		j++
	}
	for j < len(src) && (src[j] == ' ' || src[j] == '\t') {
		j++
	}
	l.out.WriteString(src[i:j])
	start := j
	var word strings.Builder
	for j < len(src) && !strings.ContainsRune(" \t\n;|&<>()", rune(src[j])) {
		switch src[j] {
		case '\'', '"':
			h.quoted = true
			q := src[j]
			j++
			for j < len(src) && src[j] != q {
				word.WriteByte(src[j])
				j++
			}
		case '\\':
			h.quoted = true
			if j+1 < len(src) {
				word.WriteByte(src[j+1])
				j++
			}
		default:
			word.WriteByte(src[j])
		}
		j++
	}
	h.delim = word.String()
	h.at = l.out.Len()
	h.width = j - start
	l.out.WriteString(src[start:j])
	l.pending = append(l.pending, h)
	return j
}

// heredocBodies copies the bodies of the pending here-documents starting at i.
func (l *lexer) heredocBodies(i int) (int, error) {
	src := l.src
	for _, h := range l.pending {
		var body []string
		end := false
		for i < len(src) {
			nl := strings.IndexByte(src[i:], '\n')
			line := src[i:]
			if nl >= 0 {
				line = src[i : i+nl]
				i += nl + 1
			} else {
				i = len(src)
			}
			check := line
			if h.strip {
				check = strings.TrimLeft(line, "\t")
			}
			if check == h.delim {
				end = true
				body = append(body, "\x00"+line)
				break
			}
			body = append(body, line)
		}
		if !end {
			return 0, fmt.Errorf("unterminated here-document %q", h.delim)
		}
		l.writeBody(h, body)
	}
	l.pending = nil
	return i, nil
}

func (l *lexer) writeBody(h heredoc, lines []string) {
	text := strings.Join(lines[:len(lines)-1], "\n")
	closing := strings.TrimPrefix(lines[len(lines)-1], "\x00")
	var hits []*Secret
	for k := range l.secrets {
		if l.secrets[k].Value != "" && strings.Contains(text, l.secrets[k].Value) {
			hits = append(hits, &l.secrets[k])
		}
	}
	if len(hits) > 0 {
		if h.quoted {
			// Make the here-document expand variables: escape what the shell
			// would interpret and unquote the delimiter.
			text = strings.NewReplacer(`\`, `\\`, `$`, `\$`, "`", "\\`").Replace(text)
			cur := l.out.String()
			l.out.Reset()
			l.out.WriteString(cur[:h.at] + h.delim + cur[h.at+h.width:])
		}
		for _, s := range hits {
			text = strings.ReplaceAll(text, s.Value, "${"+s.Env+"}")
			l.used[s.ID] = true
		}
	}
	if len(lines) > 1 {
		l.out.WriteString(text + "\n")
	}
	l.out.WriteString(closing + "\n")
}
