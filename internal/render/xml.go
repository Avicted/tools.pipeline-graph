package render

import (
	"html"
	"strings"
)

// xmlEscape makes s safe as XML character data or as the value of a double-quoted attribute.
// Text from analysed repositories can hold control characters and invalid UTF-8, which XML 1.0
// does not allow at all, not even escaped: draw.io and Visio refuse the whole file for one
// NUL. Those are dropped, and each invalid UTF-8 byte becomes U+FFFD, which is allowed.
func xmlEscape(s string) string {
	return html.EscapeString(strings.Map(func(r rune) rune {
		if xmlChar(r) {
			return r
		}
		return -1
	}, s))
}

// xmlChar is the Char production of XML 1.0.
func xmlChar(r rune) bool {
	return r == '\t' || r == '\n' || r == '\r' ||
		(r >= 0x20 && r <= 0xD7FF) || (r >= 0xE000 && r <= 0xFFFD) || (r >= 0x10000 && r <= 0x10FFFF)
}
