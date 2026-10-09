package jsonc

import "strings"

// StripComments removes comments from text, line breaks inside them too.
// ReplaceComments keeps offsets and lines.
func StripComments(text string) string {
	return stripOrReplace(text, 0, false)
}

// ReplaceComments replaces each comment byte except line breaks with ch. Offsets
// stay valid as long as ch is a single byte.
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
			continue
		}
		for i := start; i < end; i++ {
			if isLineBreak(text[i]) {
				b.WriteByte(text[i])
			} else {
				b.WriteRune(ch)
			}
		}
	}
	return b.String()
}
