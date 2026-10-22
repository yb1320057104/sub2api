package service

import (
	"regexp"
	"strconv"
	"strings"
)

var intelligenceFinalTag = regexp.MustCompile(`(?is)<final_answer>\s*([0-9]+)\s*</final_answer>`)
var intelligenceFinalMarker = regexp.MustCompile(`(?i)<\s*/?\s*final_answer\b`)
var intelligenceTrailingNoise = regexp.MustCompile("^[\\s`*_~#>\\-]*$")
var intelligenceFinalLabel = regexp.MustCompile(`(?i)^(?:最终答案|最终结果|答案|final\s+answer)\s*(?:[:：=]|为|是)\s*(.+)$`)

func intelligenceParseAnswerNumber(text string) *int {
	text = strings.TrimSpace(text)
	for i := 0; i < 12; i++ {
		previous := text
		for _, pair := range [][2]string{{"**", "**"}, {"__", "__"}, {"`", "`"}, {"$", "$"}, {`\(`, `\)`}, {`\[`, `\]`}, {`\boxed{`, `}`}} {
			if len(text) >= len(pair[0])+len(pair[1]) && strings.HasPrefix(text, pair[0]) && strings.HasSuffix(text, pair[1]) {
				text = strings.TrimSpace(text[len(pair[0]) : len(text)-len(pair[1])])
				break
			}
		}
		if previous == text {
			break
		}
	}
	text = strings.TrimSpace(strings.TrimRight(text, "。.!！"))
	text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(text, "个糖果"), "个"))
	if regexp.MustCompile(`^[0-9]+$`).MatchString(text) {
		significant := strings.TrimLeft(text, "0")
		if significant == "" {
			significant = "0"
		}
		if len(significant) > 7 {
			return nil
		}
		number, err := strconv.Atoi(significant)
		if err == nil && number <= 1000000 {
			return &number
		}
		return nil
	}
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	runes := []rune(text)
	if len(runes) == 1 {
		if number, ok := digits[runes[0]]; ok {
			return &number
		}
	}
	chunks := strings.Split(text, "十")
	if len(chunks) == 2 {
		tens, units := 1, 0
		if chunks[0] != "" {
			r := []rune(chunks[0])
			if len(r) != 1 {
				return nil
			}
			n, ok := digits[r[0]]
			if !ok || n == 0 {
				return nil
			}
			tens = n
		}
		if chunks[1] != "" {
			r := []rune(chunks[1])
			if len(r) != 1 {
				return nil
			}
			n, ok := digits[r[0]]
			if !ok || n == 0 {
				return nil
			}
			units = n
		}
		number := tens*10 + units
		return &number
	}
	return nil
}
func intelligenceExtractAnswer(text string) *int {
	if intelligenceFinalMarker.MatchString(text) {
		matches := intelligenceFinalTag.FindAllStringSubmatchIndex(text, -1)
		if len(matches) == 0 || intelligenceFinalMarker.MatchString(intelligenceFinalTag.ReplaceAllString(text, "")) {
			return nil
		}
		var answer *int
		for _, match := range matches {
			number := intelligenceParseAnswerNumber(text[match[2]:match[3]])
			if number == nil || (answer != nil && *number != *answer) {
				return nil
			}
			answer = number
		}
		if !intelligenceTrailingNoise.MatchString(text[matches[len(matches)-1][1]:]) {
			return nil
		}
		return answer
	}
	if number := intelligenceParseAnswerNumber(text); number != nil {
		return number
	}
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for len(lines) > 0 && intelligenceTrailingNoise.MatchString(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}
	var answer *int
	for i, line := range lines {
		clean := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(line, "**", ""), "__", ""))
		match := intelligenceFinalLabel.FindStringSubmatch(clean)
		if match == nil {
			if i == len(lines)-1 {
				return nil
			}
			continue
		}
		number := intelligenceParseAnswerNumber(match[1])
		if number == nil {
			if i == len(lines)-1 {
				return nil
			}
			continue
		}
		if answer != nil && *answer != *number {
			return nil
		}
		answer = number
	}
	return answer
}
