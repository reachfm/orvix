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
	// Active-content element families with no legitimate place in an
	// email reading pane: iframe/object/embed can all load and
	// execute arbitrary remote or plugin content just like a script
	// tag can. <iframe>/<object> may carry a closing tag with content
	// in between (or appear self-closing/void in malformed HTML);
	// <embed> is always a void element.
	iframeTagRE = regexp.MustCompile(`(?is)<iframe\b[^>]*>.*?</iframe\s*>|<iframe\b[^>]*/?>`)
	objectTagRE = regexp.MustCompile(`(?is)<object\b[^>]*>.*?</object\s*>|<object\b[^>]*/?>`)
	embedTagRE  = regexp.MustCompile(`(?i)<embed\b[^>]*/?>`)
)

// SanitizeHTML strips dangerous content from HTML email bodies.
// Removes: script/iframe/object/embed tags, event handlers,
// javascript: URLs. Blocks: remote images by replacing src with
// data-remote-src. Preserves: safe HTML structure (divs, spans,
// tables, links, basic formatting).
func SanitizeHTML(html string) string {
	if html == "" {
		return ""
	}

	// Remove <script> tags and their content.
	html = scriptTagRE.ReplaceAllString(html, "")

	// Remove <iframe>/<object>/<embed> — same threat class as script:
	// all three can load and execute active remote/plugin content.
	html = iframeTagRE.ReplaceAllString(html, "")
	html = objectTagRE.ReplaceAllString(html, "")
	html = embedTagRE.ReplaceAllString(html, "")

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
