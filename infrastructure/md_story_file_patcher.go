package infrastructure

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

var statusLineRe = regexp.MustCompile(`(?m)\*\*Status:\*\*[^\n]*`)

// MDStoryFilePatcher patches the **Status:** line in a story .md file.
type MDStoryFilePatcher struct {
	log *zap.Logger
}

func NewMDStoryFilePatcher(log *zap.Logger) *MDStoryFilePatcher {
	return &MDStoryFilePatcher{log: log}
}

func (p *MDStoryFilePatcher) PatchStatus(filePath string, status string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("reading story file %q: %w", filePath, err)
	}

	newLine := fmt.Sprintf("**Status:** %s", status)
	text := string(content)

	if statusLineRe.MatchString(text) {
		text = statusLineRe.ReplaceAllString(text, newLine)
	} else {
		// Insert after first heading, or prepend if no heading found.
		if m := headingRe.FindStringIndex(text); m != nil {
			insertAt := m[1]
			text = text[:insertAt] + "\n\n" + newLine + text[insertAt:]
		} else {
			text = newLine + "\n\n" + text
		}
	}

	// Ensure single trailing newline.
	text = strings.TrimRight(text, "\n") + "\n"

	if err := os.WriteFile(filePath, []byte(text), 0644); err != nil {
		return fmt.Errorf("writing story file %q: %w", filePath, err)
	}

	p.log.Info("patched story file status", zap.String("file", filePath), zap.String("status", status))
	return nil
}
