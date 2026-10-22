package service

import (
	"regexp"
	"strings"
)

// Extracted intelligenceArtifacts remain untrusted. The caller must isolate their rendering
// in a sandboxed iframe; original responses must not be retained.
var (
	intelligenceArtifactFenceStartRE = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})[ \\t]*([^\\r\\n]*)$")
	intelligenceArtifactRootStartRE  = regexp.MustCompile(`(?i)^<(?:!doctype\s+html\b|html(?:\s|>)|svg(?:\s|/?>))`)
	intelligenceArtifactTagRE        = regexp.MustCompile(`(?i)^<(/?)([a-z][a-z0-9:-]*)(?:\s|/?>)`)
	intelligenceArtifactDoctypeRE    = regexp.MustCompile(`(?i)^<!doctype\s+html(?:\s|>)`)
	intelligenceArtifactGapRE        = regexp.MustCompile(`(?s)^(?:\s|<!--.*?-->)*$`)
	intelligenceArtifactCommentRE    = regexp.MustCompile(`(?s)^(?:\s|<!--.*?-->)*`)
	intelligenceArtifactScriptEndRE  = regexp.MustCompile(`(?i)</script\s*>`)
	intelligenceArtifactStyleEndRE   = regexp.MustCompile(`(?i)</style\s*>`)
)

type intelligenceArtifactBlock struct {
	label  string
	source string
}

type intelligenceArtifactRegions struct {
	documents   []string
	svgs        []string
	invalidHTML bool
	invalidSVG  bool
}

// intelligenceExtractHTML returns one complete HTML document, an explicit HTML block with
// SVG, or a standalone SVG. Complete documents take priority over explanatory
// fragments; conflicting complete documents or incomplete roots return "".
// No script, style, attribute, or text in the chosen source is rewritten.
func intelligenceExtractHTML(text string) string {
	text = strings.TrimPrefix(strings.TrimSpace(text), "\ufeff")
	if text == "" {
		return ""
	}
	// Raw HTML may itself contain Markdown fences inside scripts or text. Do not
	// split such a document into Markdown blocks and accidentally change it.
	leading := intelligenceArtifactCommentRE.ReplaceAllString(text, "")
	if intelligenceArtifactRootStartRE.MatchString(leading) {
		return intelligenceArtifactChoose(intelligenceArtifactScan(text, true), nil)
	}
	blocks, prose := intelligenceArtifactCodeBlocks(text)
	combined := intelligenceArtifactScan(prose, false)
	var explicitHTML, fragments []string
	for _, block := range blocks {
		isHTML := block.label == "html" || block.label == "htm" || block.label == "xhtml" || block.label == "text/html"
		isSVG := block.label == "svg" || block.label == "image/svg+xml"
		if !isHTML && !isSVG && block.label != "" {
			continue // In particular, never interpret a JavaScript fence as HTML.
		}
		source := strings.TrimSpace(block.source)
		if source == "" {
			continue
		}
		leading = intelligenceArtifactCommentRE.ReplaceAllString(source, "")
		markupBlock := (isHTML || isSVG) && (intelligenceArtifactTagRE.MatchString(leading) || intelligenceArtifactDoctypeRE.MatchString(leading))
		found := intelligenceArtifactScan(source, markupBlock)
		combined.documents = append(combined.documents, found.documents...)
		combined.svgs = append(combined.svgs, found.svgs...)
		combined.invalidHTML = combined.invalidHTML || found.invalidHTML
		combined.invalidSVG = combined.invalidSVG || found.invalidSVG
		if isHTML {
			explicitHTML = append(explicitHTML, source)
			leading = intelligenceArtifactCommentRE.ReplaceAllString(source, "")
			if len(found.documents) == 0 && len(found.svgs) > 0 && intelligenceArtifactTagRE.MatchString(leading) {
				// Keep surrounding CSS and script when an explicit HTML block is
				// a fragment such as <style>...</style><svg>...</svg><script>...</script>.
				fragments = append(fragments, source)
			}
		}
	}
	if len(combined.documents) > 0 {
		// Models may follow a complete page with several short correction
		// examples. These fragments do not compete with the complete document.
		// intelligenceArtifactChoose still rejects distinct complete documents.
		return intelligenceArtifactChoose(combined, nil)
	}
	if len(intelligenceArtifactUnique(explicitHTML)) > 1 {
		return ""
	}
	return intelligenceArtifactChoose(combined, fragments)
}

func intelligenceArtifactChoose(found intelligenceArtifactRegions, fragments []string) string {
	if found.invalidHTML {
		return ""
	}
	documents := intelligenceArtifactUnique(found.documents)
	if len(documents) == 1 {
		return documents[0]
	}
	if len(documents) > 1 || found.invalidSVG {
		return ""
	}
	fragments = intelligenceArtifactUnique(fragments)
	if len(fragments) == 1 {
		return fragments[0]
	}
	if len(fragments) > 1 {
		return ""
	}
	svgs := intelligenceArtifactUnique(found.svgs)
	if len(svgs) == 1 {
		return svgs[0]
	}
	return ""
}

func intelligenceArtifactUnique(values []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func intelligenceArtifactCodeBlocks(text string) ([]intelligenceArtifactBlock, string) {
	var blocks []intelligenceArtifactBlock
	var prose, body strings.Builder
	marker, label := "", ""
	for _, line := range strings.SplitAfter(text, "\n") {
		bare := strings.TrimRight(line, "\r\n")
		if marker == "" {
			match := intelligenceArtifactFenceStartRE.FindStringSubmatch(bare)
			if match == nil {
				_, _ = prose.WriteString(line)
				continue
			}
			marker, label = match[1], strings.ToLower(strings.TrimSpace(match[2]))
			body.Reset()
		} else {
			trimmed := strings.TrimLeft(bare, " ")
			indent := len(bare) - len(trimmed)
			trimmed = strings.TrimRight(trimmed, " \t")
			if indent <= 3 && len(trimmed) >= len(marker) && strings.Trim(trimmed, marker[:1]) == "" {
				blocks = append(blocks, intelligenceArtifactBlock{label: label, source: body.String()})
				marker, label = "", ""
			} else {
				_, _ = body.WriteString(line)
			}
		}
		_ = prose.WriteByte('\n')
	}
	// Unclosed fences remain excluded instead of guessing where their code ends.
	return blocks, prose.String()
}

// intelligenceArtifactScan is a small source scanner, not a DOM parser. Quoted attributes,
// comments, and script/style raw text are skipped without changing the source.
func intelligenceArtifactScan(source string, explicit bool) intelligenceArtifactRegions {
	var result intelligenceArtifactRegions
	root, depth, start, previousEnd := "", 0, 0, 0
	doctypeStart, doctypeEnd := -1, -1
	broken := false
	for cursor := 0; cursor < len(source); {
		relative := strings.IndexByte(source[cursor:], '<')
		if relative < 0 {
			break
		}
		at := cursor + relative
		if strings.HasPrefix(source[at:], "<!--") {
			end := strings.Index(source[at+4:], "-->")
			if end < 0 {
				break
			}
			cursor = at + 4 + end + 3
			continue
		}
		end := intelligenceArtifactTagEnd(source, at)
		if end < 0 {
			break
		}
		token := source[at:end]
		cursor = end
		if root == "" && intelligenceArtifactDoctypeRE.MatchString(token) {
			doctypeStart, doctypeEnd = at, end
			continue
		}
		match := intelligenceArtifactTagRE.FindStringSubmatch(token)
		if match == nil {
			continue
		}
		closing, name := match[1] == "/", strings.ToLower(match[2])
		selfClosing := strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(token, ">")), "/")
		if !closing && (name == "script" || name == "style") {
			rawEndRE := intelligenceArtifactScriptEndRE
			if name == "style" {
				rawEndRE = intelligenceArtifactStyleEndRE
			}
			if closeAt := rawEndRE.FindStringIndex(source[cursor:]); closeAt != nil {
				cursor += closeAt[1]
			} else {
				break
			}
			continue
		}
		if root != "" {
			if name != root || selfClosing {
				continue
			}
			if !closing {
				depth++
				broken = broken || name == "html"
				continue
			}
			depth--
			if depth == 0 {
				if broken {
					result.invalidHTML = true
				} else if root == "html" {
					result.documents = append(result.documents, source[start:end])
				} else {
					result.svgs = append(result.svgs, source[start:end])
				}
				root, previousEnd = "", end
			}
			continue
		}
		if closing || (name != "html" && name != "svg") {
			doctypeStart, doctypeEnd = -1, -1
			continue
		}
		candidateStart := at
		if name == "html" && doctypeStart >= 0 && intelligenceArtifactGapRE.MatchString(source[doctypeEnd:at]) {
			candidateStart = doctypeStart
		}
		doctypeStart, doctypeEnd = -1, -1
		if !explicit && !intelligenceArtifactCanBegin(source[previousEnd:candidateStart]) {
			continue
		}
		if selfClosing {
			if name == "html" {
				result.invalidHTML = true
			} else {
				result.svgs = append(result.svgs, source[candidateStart:end])
				previousEnd = end
			}
			continue
		}
		root, depth, start, broken = name, 1, candidateStart, false
	}
	result.invalidHTML = result.invalidHTML || root == "html"
	result.invalidSVG = result.invalidSVG || root == "svg"
	return result
}

func intelligenceArtifactTagEnd(source string, start int) int {
	var quote byte
	for at := start + 1; at < len(source); at++ {
		char := source[at]
		if quote != 0 {
			if char == quote {
				quote = 0
			}
		} else if char == '\'' || char == '"' {
			quote = char
		} else if char == '>' {
			return at + 1
		}
	}
	return -1
}

func intelligenceArtifactCanBegin(before string) bool {
	if strings.Trim(before, " \t\r\n\ufeff") == "" {
		return true
	}
	lineStart := strings.LastIndexByte(before, '\n') + 1
	if strings.TrimSpace(before[lineStart:]) != "" {
		return false
	}
	// Reject a root embedded inside a multiline JavaScript template literal.
	ticks := 0
	for at := 0; at < len(before); at++ {
		switch before[at] {
		case '\\':
			at++
		case '`':
			ticks++
		}
	}
	return ticks%2 == 0
}
