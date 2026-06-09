package mime

import (
	"regexp"
	"strings"
)

var (
	scriptTagRE      = regexp.MustCompile(`(?i)<script[\s>][^<]*(?:</script>)?`)
	scriptInlineRE   = regexp.MustCompile(`(?i)<[^>]*\bon\w+\s*=\s*['"][^'"]*['"][^>]*>`)
	javascriptURLRE  = regexp.MustCompile(`(?i)\bjavascript\s*:`)
	remoteImgSrcRE   = regexp.MustCompile(`(?i)<img[^>]+src\s*=\s*['"]https?://[^'"]*['"]`)
	remoteImgSrcAttr = regexp.MustCompile(`(?i)(src\s*=\s*)['"]https?://[^'"]*['"]`)
)

// SanitizeHTML strips dangerous content from HTML email bodies.
// Removes: script tags, event handlers, javascript: URLs.
// Blocks: remote images by replacing src with data-original-src.
// Preserves: safe HTML structure (divs, spans, tables, links, basic formatting).
func SanitizeHTML(html string) string {
	if html == "" {
		return ""
	}

	// Remove <script> tags and their content.
	html = scriptTagRE.ReplaceAllString(html, "")

	// Remove event handler attributes (onclick, onload, onerror, etc.).
	html = scriptInlineRE.ReplaceAllStringFunc(html, func(match string) string {
		// Remove all on* attributes.
		re := regexp.MustCompile(`(?i)\s+\bon\w+\s*=\s*['"][^'"]*['"]`)
		return re.ReplaceAllString(match, "")
	})

	// Replace javascript: URLs in href attributes.
	html = javascriptURLRE.ReplaceAllString(html, "blocked:")

	// Block remote images: replace src="http..." with data-remote-src="..."
	html = remoteImgSrcAttr.ReplaceAllStringFunc(html, func(match string) string {
		if strings.HasPrefix(strings.ToLower(match), "src=") && (strings.Contains(match, "http://") || strings.Contains(match, "https://")) {
			return "data-remote-src=" + match[4:]
		}
		return match
	})

	return html
}

// HasRemoteImages checks if HTML content references remote images.
func HasRemoteImages(html string) bool {
	return remoteImgSrcRE.MatchString(html)
}

// IsProbablyHTML returns true if the content appears to be HTML.
func IsProbablyHTML(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "<html") ||
		strings.Contains(lower, "<!doctype") ||
		strings.Contains(lower, "<div") ||
		strings.Contains(lower, "<p>") ||
		strings.Contains(lower, "<br") ||
		strings.Contains(lower, "<table")
}
