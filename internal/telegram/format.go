package telegram

import (
	"html"
	"regexp"
	"strings"
)

// Escape makes text safe inside Telegram HTML.
func Escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

var (
	tagRe    = regexp.MustCompile(`<[^>]+>`)
	boldRe   = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	italicRe = regexp.MustCompile(`(^|[\s(])_([^_\n]+)_([\s).,!?:;]|$)`)
	codeRe   = regexp.MustCompile("`([^`\n]+)`")
	linkRe   = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	headRe   = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	bulletRe = regexp.MustCompile(`^(\s*)[-*]\s+`)
)

// StripTags turns Telegram HTML back into plain text.
func StripTags(s string) string {
	return html.UnescapeString(tagRe.ReplaceAllString(s, ""))
}

// HTML renders the Markdown an LLM writes as Telegram HTML: fenced code
// blocks, inline code, bold, italics, links, headings (bold) and bullets.
// Anything else stays as text, escaped.
func HTML(md string) string {
	var b strings.Builder
	inFence := false
	for i, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inFence {
				b.WriteString("</pre>")
			} else {
				if i > 0 {
					b.WriteString("\n")
				}
				b.WriteString("<pre>")
			}
			inFence = !inFence
			continue
		}
		if i > 0 && !(inFence && strings.HasSuffix(b.String(), "<pre>")) {
			b.WriteString("\n")
		}
		if inFence {
			b.WriteString(Escape(line))
			continue
		}
		b.WriteString(inline(line))
	}
	if inFence {
		b.WriteString("</pre>")
	}
	return strings.TrimSpace(b.String())
}

func inline(line string) string {
	if m := headRe.FindStringSubmatch(line); m != nil {
		return "<b>" + Escape(m[1]) + "</b>"
	}
	line = bulletRe.ReplaceAllString(line, "$1• ")
	// Inline code first, kept out of the other rules by placeholders.
	var codes []string
	line = codeRe.ReplaceAllStringFunc(line, func(m string) string {
		codes = append(codes, "<code>"+Escape(m[1:len(m)-1])+"</code>")
		return "\x00" + string(rune('A'+len(codes)-1)) + "\x00"
	})
	var links []string
	line = linkRe.ReplaceAllStringFunc(line, func(m string) string {
		sm := linkRe.FindStringSubmatch(m)
		links = append(links, `<a href="`+Escape(sm[2])+`">`+Escape(sm[1])+"</a>")
		return "\x01" + string(rune('A'+len(links)-1)) + "\x01"
	})
	line = Escape(line)
	line = boldRe.ReplaceAllString(line, "<b>$1</b>")
	line = italicRe.ReplaceAllString(line, "$1<i>$2</i>$3")
	for i, c := range codes {
		line = strings.Replace(line, "\x00"+string(rune('A'+i))+"\x00", c, 1)
	}
	for i, l := range links {
		line = strings.Replace(line, "\x01"+string(rune('A'+i))+"\x01", l, 1)
	}
	return line
}

// Split cuts long Markdown into pieces of at most max characters, at blank
// lines (else line ends, else hard), so each piece renders on its own.
func Split(text string, max int) []string {
	text = strings.TrimSpace(text)
	var out []string
	for len(text) > max {
		cut := strings.LastIndex(text[:max], "\n\n")
		if cut < max/3 {
			cut = strings.LastIndex(text[:max], "\n")
		}
		if cut < max/3 {
			cut = max
		}
		out = append(out, strings.TrimSpace(text[:cut]))
		text = strings.TrimSpace(text[cut:])
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

// Clip shortens text to max characters (on a rune boundary), with "…".
func Clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
