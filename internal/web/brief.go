package web

import (
	"bufio"
	"regexp"
	"strings"
)

// ExtractReadingList pulls bullet items from the "Reading list" section
// of a brief's markdown output. Tolerant of variant headings.
var readingHeading = regexp.MustCompile(`(?i)^#{2,3}\s+(reading list|bibliography|further reading)\s*$`)
var nextHeading = regexp.MustCompile(`^#{1,3}\s+\S`)
var bulletItem = regexp.MustCompile(`^\s*(?:[-*+]|\d+\.)\s+(.+?)\s*$`)

func ExtractReadingList(md string) []string {
	scanner := bufio.NewScanner(strings.NewReader(md))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	inSection := false
	var items []string
	for scanner.Scan() {
		line := scanner.Text()
		if readingHeading.MatchString(strings.TrimSpace(line)) {
			inSection = true
			continue
		}
		if !inSection {
			continue
		}
		if nextHeading.MatchString(line) {
			break
		}
		if m := bulletItem.FindStringSubmatch(line); m != nil {
			items = append(items, strings.TrimSpace(m[1]))
		}
	}
	return items
}
