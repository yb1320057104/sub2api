package service

import (
	"strings"
)

type contentModerationKeywordMatcher struct {
	nodes              []contentModerationKeywordNode
	edges              []contentModerationKeywordEdge
	rootTransitions    [256]int32
	keywords           []string
	normalizedKeywords []string
}

type contentModerationKeywordNode struct {
	failure         int32
	bestKeyword     int32
	terminalKeyword int32
	outputLink      int32
	edgeStart       uint32
	edgeCount       uint16
}

type contentModerationKeywordEdge struct {
	target int32
	label  byte
}

type contentModerationKeywordBuildEdge struct {
	target      int32
	nextSibling int32
	label       byte
}

func newContentModerationKeywordMatcher(keywords []string) *contentModerationKeywordMatcher {
	if len(keywords) == 0 {
		return nil
	}

	buildNodes := []contentModerationKeywordNode{newContentModerationKeywordNode()}
	buildEdges := make([]contentModerationKeywordBuildEdge, 0)
	originalKeywords := append([]string(nil), keywords...)
	normalizedKeywords := make([]string, len(keywords))

	for keywordIndex, keyword := range keywords {
		if keyword == "" {
			continue
		}
		state := int32(0)
		normalizedKeywords[keywordIndex] = strings.ToLower(keyword)
		for _, label := range []byte(normalizedKeywords[keywordIndex]) {
			next := contentModerationKeywordBuildTransition(buildNodes, buildEdges, state, label)
			if next < 0 {
				next = int32(len(buildNodes))
				buildNodes = append(buildNodes, newContentModerationKeywordNode())
				buildEdges = append(buildEdges, contentModerationKeywordBuildEdge{
					target:      next,
					nextSibling: contentModerationKeywordBuildFirstEdge(buildNodes[state]),
					label:       label,
				})
				buildNodes[state].edgeStart = uint32(len(buildEdges))
			}
			state = next
		}
		if current := buildNodes[state].bestKeyword; current < 0 || int32(keywordIndex) < current {
			buildNodes[state].bestKeyword = int32(keywordIndex)
			buildNodes[state].terminalKeyword = int32(keywordIndex)
		}
	}

	if len(buildNodes) == 1 {
		return nil
	}

	queue := make([]int32, 0, len(buildNodes)-1)
	var rootTransitions [256]int32
	for edgeIndex := contentModerationKeywordBuildFirstEdge(buildNodes[0]); edgeIndex >= 0; edgeIndex = buildEdges[edgeIndex].nextSibling {
		edge := buildEdges[edgeIndex]
		rootTransitions[edge.label] = edge.target
		queue = append(queue, edge.target)
	}

	for queueIndex := 0; queueIndex < len(queue); queueIndex++ {
		state := queue[queueIndex]
		for edgeIndex := contentModerationKeywordBuildFirstEdge(buildNodes[state]); edgeIndex >= 0; edgeIndex = buildEdges[edgeIndex].nextSibling {
			edge := buildEdges[edgeIndex]
			failure := buildNodes[state].failure
			fallback := contentModerationKeywordBuildTransition(buildNodes, buildEdges, failure, edge.label)
			for fallback < 0 && failure != 0 {
				failure = buildNodes[failure].failure
				fallback = contentModerationKeywordBuildTransition(buildNodes, buildEdges, failure, edge.label)
			}
			if fallback >= 0 {
				buildNodes[edge.target].failure = fallback
			}
			failureNode := buildNodes[edge.target].failure
			if buildNodes[failureNode].terminalKeyword >= 0 {
				buildNodes[edge.target].outputLink = failureNode
			} else {
				buildNodes[edge.target].outputLink = buildNodes[failureNode].outputLink
			}
			buildNodes[edge.target].bestKeyword = minKeywordIndex(
				buildNodes[edge.target].bestKeyword,
				buildNodes[buildNodes[edge.target].failure].bestKeyword,
			)
			queue = append(queue, edge.target)
		}
	}

	edges := make([]contentModerationKeywordEdge, 0, len(buildEdges))
	var outgoing [256]contentModerationKeywordEdge
	for nodeIndex := range buildNodes {
		count := 0
		for edgeIndex := contentModerationKeywordBuildFirstEdge(buildNodes[nodeIndex]); edgeIndex >= 0; edgeIndex = buildEdges[edgeIndex].nextSibling {
			edge := buildEdges[edgeIndex]
			outgoing[count] = contentModerationKeywordEdge{target: edge.target, label: edge.label}
			count++
		}
		for index := 1; index < count; index++ {
			current := outgoing[index]
			insertAt := index
			for insertAt > 0 && current.label < outgoing[insertAt-1].label {
				outgoing[insertAt] = outgoing[insertAt-1]
				insertAt--
			}
			outgoing[insertAt] = current
		}
		buildNodes[nodeIndex].edgeStart = uint32(len(edges))
		buildNodes[nodeIndex].edgeCount = uint16(count)
		edges = append(edges, outgoing[:count]...)
	}

	return &contentModerationKeywordMatcher{
		nodes:              buildNodes,
		edges:              edges,
		rootTransitions:    rootTransitions,
		keywords:           originalKeywords,
		normalizedKeywords: normalizedKeywords,
	}
}

func newContentModerationKeywordNode() contentModerationKeywordNode {
	return contentModerationKeywordNode{bestKeyword: -1, terminalKeyword: -1}
}

func contentModerationKeywordBuildFirstEdge(node contentModerationKeywordNode) int32 {
	if node.edgeStart == 0 {
		return -1
	}
	return int32(node.edgeStart - 1)
}

func contentModerationKeywordBuildTransition(
	nodes []contentModerationKeywordNode,
	edges []contentModerationKeywordBuildEdge,
	state int32,
	label byte,
) int32 {
	if state < 0 || int(state) >= len(nodes) {
		return -1
	}
	for edgeIndex := contentModerationKeywordBuildFirstEdge(nodes[state]); edgeIndex >= 0; edgeIndex = edges[edgeIndex].nextSibling {
		if edges[edgeIndex].label == label {
			return edges[edgeIndex].target
		}
	}
	return -1
}

func minKeywordIndex(left, right int32) int32 {
	if left < 0 {
		return right
	}
	if right < 0 || left < right {
		return left
	}
	return right
}

func (m *contentModerationKeywordMatcher) Match(text string) (string, bool) {
	if m == nil || text == "" || len(m.nodes) == 0 || len(m.keywords) == 0 {
		return "", false
	}
	lower := strings.ToLower(text)
	state := int32(0)
	bestKeyword := int32(-1)
	for index := 0; index < len(lower); index++ {
		label := lower[index]
		for {
			next := m.next(state, label)
			if next != 0 {
				state = next
				break
			}
			if state == 0 {
				break
			}
			state = m.nodes[state].failure
		}
		bestKeyword = minKeywordIndex(bestKeyword, m.nodes[state].bestKeyword)
		if bestKeyword == 0 {
			return m.keywords[0], true
		}
	}
	if bestKeyword < 0 || int(bestKeyword) >= len(m.keywords) {
		return "", false
	}
	return m.keywords[bestKeyword], true
}

// MatchRisk checks every terminal output, including suffix matches, because the
// old first-keyword priority cannot select the highest configured score. ASCII
// word boundaries apply only here; legacy Match deliberately keeps its behavior.
func (m *contentModerationKeywordMatcher) MatchRisk(text string, scores map[string]int) (string, int, bool) {
	if m == nil || text == "" || len(m.nodes) == 0 {
		return "", 0, false
	}
	lower := strings.ToLower(text)
	state, best := int32(0), int32(-1)
	bestScore := -1
	for index := 0; index < len(lower); index++ {
		for {
			next := m.next(state, lower[index])
			if next != 0 {
				state = next
				break
			}
			if state == 0 {
				break
			}
			state = m.nodes[state].failure
		}
		for output := state; output != 0; output = m.nodes[output].outputLink {
			candidate := m.nodes[output].terminalKeyword
			if candidate < 0 {
				continue
			}
			keyword := m.normalizedKeywords[candidate]
			start, end := index+1-len(keyword), index+1
			if !contentModerationRiskKeywordBoundary(lower, keyword, start, end) {
				continue
			}
			score, configured := scores[keyword]
			if !configured {
				score = 100
			}
			if score > bestScore || (score == bestScore && (best < 0 || candidate < best)) {
				best, bestScore = candidate, score
			}
		}
	}
	if best < 0 {
		return "", 0, false
	}
	return m.keywords[best], bestScore, true
}

func contentModerationRiskKeywordBoundary(text, keyword string, start, end int) bool {
	if start < 0 || len(keyword) == 0 {
		return false
	}
	isWord := func(value byte) bool {
		return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
	}
	if isWord(keyword[0]) && start > 0 && isWord(text[start-1]) {
		return false
	}
	if isWord(keyword[len(keyword)-1]) && end < len(text) && isWord(text[end]) {
		return false
	}
	return true
}

func (m *contentModerationKeywordMatcher) next(state int32, label byte) int32 {
	if state == 0 {
		return m.rootTransitions[label]
	}
	if state < 0 || int(state) >= len(m.nodes) {
		return 0
	}
	node := m.nodes[state]
	left := int(node.edgeStart)
	right := left + int(node.edgeCount)
	for left < right {
		middle := left + (right-left)/2
		edge := m.edges[middle]
		if edge.label < label {
			left = middle + 1
			continue
		}
		right = middle
	}
	end := int(node.edgeStart) + int(node.edgeCount)
	if left < end && m.edges[left].label == label {
		return m.edges[left].target
	}
	return 0
}
