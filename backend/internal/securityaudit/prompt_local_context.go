package securityaudit

import "regexp"

// Only a direct prohibition immediately before a built-in advisory match is
// ignored. An unrelated disclaimer never exempts the rest of the request.
var localDirectProhibition = regexp.MustCompile(`(?i)(\b(do\s+not|don't|never|must\s+not|should\s+not|cannot|avoid)\s+((ever|directly|automatically|again)\s+)*|(不要|不得|请勿|切勿|禁止|不能|不应|不允许|避免|勿|不)(擅自|随意|直接|再次|自动)*)$`)

func localRuleMatches(entry compiledLocalRule, text string) bool {
	if !entry.defaultAdvisory {
		return entry.pattern.MatchString(text)
	}
	for _, span := range entry.pattern.FindAllStringIndex(text, -1) {
		start := span[0] - 160
		if start < 0 {
			start = 0
		}
		if !localDirectProhibition.MatchString(text[start:span[0]]) {
			return true
		}
	}
	return false
}
