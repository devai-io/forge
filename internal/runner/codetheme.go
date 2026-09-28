package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// VS Code's theme follows Forge's. The workbench boots from a JSON blob the
// server embeds in the page (`data-settings` on #vscode-workbench-web-
// configuration, HTML-escaped) — the same options an embedder like vscode.dev
// passes to `create()`. The gateway adds two of them when the page is asked
// for with ?forge_theme=dark|light:
//
//   configurationDefaults.workbench.colorTheme  the default theme (a theme the
//                                               user picks inside VS Code is a
//                                               user setting and still wins)
//   initialColorTheme.themeType                 the colour of the very first
//                                               paint, before settings load
//
// Only the root document is touched; assets and WebSockets pass through.

const themeParam = "forge_theme"

var themeIDs = map[string]string{"dark": "Dark 2026", "light": "Light 2026"}

// stripThemeParam removes forge_theme from the query so VS Code never sees it.
func stripThemeParam(r *http.Request) string {
	q := r.URL.Query()
	theme := q.Get(themeParam)
	if theme == "" {
		return ""
	}
	q.Del(themeParam)
	r.URL.RawQuery = q.Encode()
	if _, ok := themeIDs[theme]; !ok {
		return ""
	}
	return theme
}

// rewriteTheme edits the served HTML in place.
func rewriteTheme(resp *http.Response, theme string) error {
	if theme == "" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || resp.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	resp.Body.Close()
	if err != nil {
		return err
	}
	out, err := injectTheme(body, theme)
	if err != nil {
		out = body // serve it untouched rather than break the editor
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Set("Content-Length", strconv.Itoa(len(out)))
	resp.Header.Del("Content-Encoding")
	return nil
}

var (
	cfgMarker = []byte(`id="vscode-workbench-web-configuration"`)
	attrStart = []byte(`data-settings="`)
)

func injectTheme(page []byte, theme string) ([]byte, error) {
	at := bytes.Index(page, cfgMarker)
	if at < 0 {
		return nil, errors.New("no workbench configuration element")
	}
	start := bytes.Index(page[at:], attrStart)
	if start < 0 {
		return nil, errors.New("no data-settings attribute")
	}
	start += at + len(attrStart)
	end := bytes.IndexByte(page[start:], '"')
	if end < 0 {
		return nil, errors.New("unterminated data-settings")
	}
	end += start

	var cfg map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(string(page[start:end]))), &cfg); err != nil {
		return nil, err
	}
	defaults, _ := cfg["configurationDefaults"].(map[string]any)
	if defaults == nil {
		defaults = map[string]any{}
	}
	defaults["workbench.colorTheme"] = themeIDs[theme]
	cfg["configurationDefaults"] = defaults
	cfg["initialColorTheme"] = map[string]any{"themeType": theme}

	// Plain JSON, not Go's HTML-safe variant (\u0026 for &): the attribute
	// escaping below is what keeps the markup safe, and the result should be
	// byte-for-byte what VS Code itself would have written.
	var raw bytes.Buffer
	enc := json.NewEncoder(&raw)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(page[:start])
	out.WriteString(escapeAttr(strings.TrimRight(raw.String(), "\n")))
	out.Write(page[end:])
	return out.Bytes(), nil
}

// escapeAttr matches VS Code's own attribute escaping.
func escapeAttr(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
