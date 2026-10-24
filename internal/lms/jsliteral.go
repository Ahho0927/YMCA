package lms

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// decodeJSLiteral decodes a quoted constant, never evaluates JavaScript. Report
// instructions are stored as an escaped HTML string in subjectCon by CoursePower.
func decodeJSLiteral(raw string) (string, error) {
	if len(raw) < 2 || (raw[0] != '\'' && raw[0] != '"') || raw[len(raw)-1] != raw[0] {
		return "", errors.New("invalid JavaScript string constant")
	}
	input := raw[1 : len(raw)-1]
	units := []uint16{}
	appendRune := func(r rune) { units = append(units, utf16.Encode([]rune{r})...) }
	for len(input) > 0 {
		if input[0] != '\\' {
			r, n := utf8.DecodeRuneInString(input)
			appendRune(r)
			input = input[n:]
			continue
		}
		input = input[1:]
		if len(input) == 0 {
			return "", errors.New("incomplete string escape")
		}
		c := input[0]
		input = input[1:]
		switch c {
		case '\n':
			continue
		case '\r':
			if strings.HasPrefix(input, "\n") {
				input = input[1:]
			}
			continue
		case 'u', 'x':
			n := 4
			if c == 'x' {
				n = 2
			}
			if len(input) < n {
				return "", errors.New("incomplete Unicode escape")
			}
			v, err := strconv.ParseUint(input[:n], 16, 16)
			if err != nil {
				return "", errors.New("invalid Unicode escape")
			}
			units = append(units, uint16(v))
			input = input[n:]
		case 'n':
			appendRune('\n')
		case 'r':
			appendRune('\r')
		case 't':
			appendRune('\t')
		case 'b':
			appendRune('\b')
		case 'f':
			appendRune('\f')
		case 'v':
			appendRune('\v')
		default:
			appendRune(rune(c))
		}
	}
	return string(utf16.Decode(units)), nil
}
