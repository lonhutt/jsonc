package jsonc

import "strings"

// StripComments removes JavaScript-style comments from text, leaving
// everything else — including line breaks inside block comments — untouched,
// so the result stays line-comparable with the input.
//
// Comment syntax inside a string literal is not a comment and is preserved.
func StripComments(text string) string {
	return stripOrReplace(text, 0, false)
}

// ReplaceComments replaces every character of every comment with ch, keeping
// the text exactly the same length so all byte offsets remain valid. Line
// breaks inside block comments are preserved rather than replaced.
//
// Offsets are preserved only when ch encodes to a single byte; pass a
// multi-byte rune and the result will be longer than the input.
func ReplaceComments(text string, ch rune) string {
	return stripOrReplace(text, ch, true)
}

func stripOrReplace(text string, ch rune, replace bool) string {
	s := NewScanner(text)
	var b strings.Builder
	b.Grow(len(text))

	for {
		k := s.Scan()
		if k == EOF {
			break
		}
		start, end := s.Offset(), s.Offset()+s.Length()
		if k != LineComment && k != BlockComment {
			b.WriteString(text[start:end])
			continue
		}
		if !replace {
			// Keep the line breaks a block comment spanned, so line numbers
			// after the comment do not shift.
			for i := start; i < end; i++ {
				if text[i] == '\n' || text[i] == '\r' {
					b.WriteByte(text[i])
				}
			}
			continue
		}
		for i := start; i < end; i++ {
			if text[i] == '\n' || text[i] == '\r' {
				b.WriteByte(text[i])
				continue
			}
			b.WriteRune(ch)
		}
	}
	return b.String()
}
