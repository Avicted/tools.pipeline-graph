// Package kts statically reads TeamCity Kotlin DSL (.teamcity/*.kts, *.kt)
// without a Kotlin compiler: a tolerant tokenizer, a generic expression tree
// and pattern extraction of the DSL constructs that define pipelines.
package kts

import (
	"strconv"
	"strings"
)

type TokKind int

const (
	TEOF TokKind = iota
	TIdent
	TString // Val holds the decoded content; templates ($x, ${...}) kept verbatim
	TNumber
	TChar
	TPunct
)

type Token struct {
	Kind TokKind
	Val  string
	Line int
	NL   bool // a newline precedes this token
}

// Lex tokenizes Kotlin source. It never fails: unknown bytes become punctuation.
func Lex(src string) []Token {
	l := &lexer{src: src, line: 1}
	l.run()
	return l.toks
}

type lexer struct {
	src  string
	pos  int
	line int
	nl   bool
	toks []Token
}

func (l *lexer) emit(k TokKind, v string, line int) {
	l.toks = append(l.toks, Token{Kind: k, Val: v, Line: line, NL: l.nl})
	l.nl = false
}

func (l *lexer) peek(off int) byte {
	if l.pos+off < len(l.src) {
		return l.src[l.pos+off]
	}
	return 0
}

var puncts3 = []string{"===", "!==", "..<"}
var puncts2 = []string{"->", "?.", "?:", "::", "==", "!=", "<=", ">=", "&&", "||", "+=", "-=", "*=", "/=", "++", "--", "!!", ".."}

func (l *lexer) run() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\n':
			l.line++
			l.nl = true
			l.pos++
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			l.pos++
		case c == '/' && l.peek(1) == '/':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
		case c == '/' && l.peek(1) == '*':
			l.blockComment()
		case c == '"' && l.peek(1) == '"' && l.peek(2) == '"':
			l.rawString()
		case c == '"':
			l.string()
		case c == '\'':
			l.char()
		case c == '`':
			start, line := l.pos+1, l.line
			l.pos++
			for l.pos < len(l.src) && l.src[l.pos] != '`' && l.src[l.pos] != '\n' {
				l.pos++
			}
			l.emit(TIdent, l.src[start:l.pos], line)
			l.pos++
		case isIdentStart(c):
			start := l.pos
			for l.pos < len(l.src) && isIdentPart(l.src[l.pos]) {
				l.pos++
			}
			l.emit(TIdent, l.src[start:l.pos], l.line)
		case c >= '0' && c <= '9':
			start := l.pos
			for l.pos < len(l.src) && (isIdentPart(l.src[l.pos]) || l.src[l.pos] == '.' && l.peek(1) >= '0' && l.peek(1) <= '9') {
				l.pos++
			}
			l.emit(TNumber, l.src[start:l.pos], l.line)
		default:
			l.punct()
		}
	}
	l.emit(TEOF, "", l.line)
}

func (l *lexer) punct() {
	for _, group := range [][]string{puncts3, puncts2} {
		for _, p := range group {
			if strings.HasPrefix(l.src[l.pos:], p) {
				l.emit(TPunct, p, l.line)
				l.pos += len(p)
				return
			}
		}
	}
	l.emit(TPunct, string(l.src[l.pos]), l.line)
	l.pos++
}

func (l *lexer) blockComment() {
	depth := 0
	for l.pos < len(l.src) {
		switch {
		case strings.HasPrefix(l.src[l.pos:], "/*"):
			depth++
			l.pos += 2
		case strings.HasPrefix(l.src[l.pos:], "*/"):
			depth--
			l.pos += 2
			if depth == 0 {
				return
			}
		default:
			if l.src[l.pos] == '\n' {
				l.line++
			}
			l.pos++
		}
	}
}

// template copies a ${...} expression verbatim (including nested strings and braces).
func (l *lexer) template(b *strings.Builder) {
	depth := 0
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '\n':
			l.line++
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				b.WriteByte(c)
				l.pos++
				return
			}
		case c == '"' && depth > 0:
			// nested string inside the template expression: copy through it
			b.WriteByte(c)
			l.pos++
			for l.pos < len(l.src) && l.src[l.pos] != '"' && l.src[l.pos] != '\n' {
				if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
					b.WriteByte(l.src[l.pos])
					l.pos++
				}
				b.WriteByte(l.src[l.pos])
				l.pos++
			}
			if l.pos < len(l.src) && l.src[l.pos] == '"' {
				b.WriteByte('"')
				l.pos++
			}
			continue
		}
		b.WriteByte(c)
		l.pos++
	}
}

func (l *lexer) string() {
	line := l.line
	l.pos++ // opening quote
	var b strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch {
		case c == '"':
			l.pos++
			l.emit(TString, b.String(), line)
			return
		case c == '\n': // unterminated; stop at end of line
			l.emit(TString, b.String(), line)
			return
		case c == '\\' && l.pos+1 < len(l.src):
			l.pos += 2
			switch e := l.src[l.pos-1]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'u':
				if l.pos+4 <= len(l.src) {
					if r, err := strconv.ParseUint(l.src[l.pos:l.pos+4], 16, 32); err == nil {
						b.WriteRune(rune(r))
					}
					l.pos += 4
				}
			case '$':
				b.WriteString(`\$`) // keep escaped dollar distinguishable from a template
			default:
				b.WriteByte(e)
			}
		case c == '$' && l.peek(1) == '{':
			l.template(&b)
		default:
			b.WriteByte(c)
			l.pos++
		}
	}
	l.emit(TString, b.String(), line)
}

func (l *lexer) rawString() {
	line := l.line
	l.pos += 3
	var b strings.Builder
	for l.pos < len(l.src) {
		if strings.HasPrefix(l.src[l.pos:], `"""`) {
			// Kotlin allows extra quotes before the closing delimiter
			for strings.HasPrefix(l.src[l.pos+1:], `"""`) {
				b.WriteByte('"')
				l.pos++
			}
			l.pos += 3
			l.emit(TString, b.String(), line)
			return
		}
		c := l.src[l.pos]
		if c == '$' && l.peek(1) == '{' {
			l.template(&b)
			continue
		}
		if c == '\n' {
			l.line++
		}
		b.WriteByte(c)
		l.pos++
	}
	l.emit(TString, b.String(), line)
}

func (l *lexer) char() {
	start, line := l.pos, l.line
	l.pos++
	for l.pos < len(l.src) && l.src[l.pos] != '\'' && l.src[l.pos] != '\n' {
		if l.src[l.pos] == '\\' {
			l.pos++
		}
		l.pos++
	}
	l.pos++
	if l.pos > len(l.src) {
		l.pos = len(l.src)
	}
	l.emit(TChar, l.src[start:l.pos], line)
}

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func isIdentPart(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' }
