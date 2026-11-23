package rewrite

import (
	"regexp"
	"strings"
)

var markdownHeading = regexp.MustCompile(`^#{1,6}[ \t]+`)

func promptHeadingLevel(line string) int {
	return len(strings.TrimRight(markdownHeading.FindString(line), " \t"))
}

func geminiTraceSystemEdits(text string, _ promptProtection) ([]promptEdit, []textRange) {
	if !strings.Contains(text, geminiHelperAnchor) {
		return nil, nil
	}
	var keep []textRange
	for _, line := range tracePromptLines(text) {
		if strings.HasPrefix(strings.TrimSpace(line.text), geminiHelperAnchor) {
			keep = append(keep, textRange{line.start, line.end})
		}
	}
	return nil, keep
}

func claudeTraceSystemEdits(text string, protection promptProtection) ([]promptEdit, []textRange) {
	if !strings.Contains(text, "# Environment") && !strings.Contains(text, "# Doing tasks") {
		return nil, nil
	}
	help := strings.Split(traceFragment("claude", "help"), "\n")
	environment := strings.Split(traceFragment("claude", "environment"), "\n")[1:]
	lines := tracePromptLines(text)
	var edits []promptEdit
	var keep []textRange
	for i := 0; i < len(lines); i++ {
		heading := lines[i]
		title := strings.TrimRight(heading.text, " \t")
		if title != "# Doing tasks" && title != "# Environment" || protection.protectedBlock(heading.start, heading.end) {
			continue
		}
		end := i + 1
		subheading := -1
		for end < len(lines) {
			level := promptHeadingLevel(lines[end].text)
			if level > 0 && !protection.protectedBlock(lines[end].start, lines[end].end) {
				if level == 1 {
					break
				}
				if subheading < 0 {
					subheading = end
				}
			}
			end++
		}
		bodyEnd := end
		if subheading >= 0 {
			bodyEnd = subheading
		}
		sectionEnd := lines[end-1].end
		switch title {
		case "# Doing tasks":
			for j := i + 1; j < bodyEnd; j++ {
				line := lines[j]
				entry := strings.TrimSpace(line.text)
				if !strings.HasPrefix(entry, "- If the user asks for help or wants to give feedback") {
					if strings.HasPrefix(entry, "- /help:") {
						keep = append(keep, textRange{line.start, line.end})
					}
					continue
				}
				stop := promptListItemEnd(lines, j, bodyEnd)
				item := textRange{line.start, lines[stop-1].end}
				keep = append(keep, item)
				if sameTraceLines(lines[j:stop], help) && !protection.protectedBlock(item.start, item.end) {
					edits = append(edits, promptEdit{item.start, item.end, ""})
				}
				j = stop - 1
			}
		case "# Environment":
			// Preserve drift here so a product capability cannot be reattributed
			// by the residual brand pass.
			keep = append(keep, textRange{heading.start, sectionEnd})
			empty := bodyEnd == end
			var entries []promptEdit
			for j := i + 1; j < bodyEnd; j++ {
				line := lines[j]
				entry := strings.TrimSpace(line.text)
				if entry == "" {
					continue
				}
				removed := false
				for _, accepted := range environment {
					if entry == strings.TrimSpace(accepted) && promptListItemEnd(lines, j, bodyEnd) == j+1 &&
						!protection.protectedBlock(line.start, line.end) {
						entries = append(entries, promptEdit{line.start, line.end, ""})
						removed = true
						break
					}
				}
				empty = empty && removed
			}
			if empty && len(entries) > 0 {
				edits = append(edits, promptEdit{heading.start, sectionEnd, ""})
			} else {
				edits = append(edits, entries...)
			}
		}
		i = end - 1
	}
	return edits, keep
}

func promptListItemEnd(lines []tracePromptLine, start, limit int) int {
	indent := promptIndent(lines[start].text)
	end := start + 1
	for end < limit {
		text := strings.TrimSpace(lines[end].text)
		if text == "" || promptIndent(lines[end].text) <= indent && (strings.HasPrefix(text, "- ") ||
			strings.HasPrefix(text, "* ") || strings.HasPrefix(text, "+ ")) {
			break
		}
		end++
	}
	return end
}

func promptIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func sameTraceLines(lines []tracePromptLine, expected []string) bool {
	if len(lines) != len(expected) {
		return false
	}
	for i, line := range lines {
		if strings.TrimSpace(line.text) != strings.TrimSpace(expected[i]) {
			return false
		}
	}
	return true
}
