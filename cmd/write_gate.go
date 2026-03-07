package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"

	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newWriteGateCmd() *cobra.Command {
	var concernsJSON string

	cmd := &cobra.Command{
		Use:   "write-gate <progress-json> <story-id> <PASS|FAIL|CONCERNS>",
		Short: "Write a QA gate file for a story",
		Long: `Create or overwrite the QA gate YAML file for a story at
<docs_folder>/qa/gates/<story-id>.yml.

The gate value must be PASS, FAIL, or CONCERNS.
Optionally supply concerns as a JSON array:

  bmad write-gate ./bmad-progress.json 2.9 PASS

  bmad write-gate ./bmad-progress.json 2.9 CONCERNS \
    '[{"severity":"high","note":"missing retry logic"}]'`,
		Args: cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			progressPath := args[0]
			storyID := args[1]
			rawResult := strings.ToUpper(strings.TrimSpace(args[2]))

			result, err := domain.ParseGateResult(rawResult)
			exitOnError(err)

			// Load progress file to resolve docs_folder and validate story exists.
			store := infrastructure.NewJSONProgressStore(log)
			progress, err := store.Load(progressPath)
			exitOnError(err)

			story := progress.FindByID(storyID)
			if story == nil {
				exitOnError(fmt.Errorf("story %q not found in progress file", storyID))
			}
			// Use the resolved (possibly prefix-matched) story ID for the gate filename.
			resolvedID := story.ID

			// Parse optional concerns.
			type rawConcern struct {
				Severity string `json:"severity"`
				Note     string `json:"note"`
			}
			var concerns []rawConcern
			if concernsJSON != "" {
				if err := json.Unmarshal([]byte(concernsJSON), &concerns); err != nil {
					exitOnError(fmt.Errorf("parsing concerns JSON: %w", err))
				}
			}

			// Build YAML structure.
			type gateFileYAML struct {
				Gate     string       `yaml:"gate"`
				Concerns []rawConcern `yaml:"concerns,omitempty"`
			}
			gf := gateFileYAML{Gate: string(result), Concerns: concerns}

			data, err := yaml.Marshal(&gf)
			exitOnError(err)

			// Write to <docs_folder>/qa/gates/<story-id>.yml.
			gatesDir := filepath.Join(progress.DocsFolder, "qa", "gates")
			if err := os.MkdirAll(gatesDir, 0755); err != nil {
				exitOnError(fmt.Errorf("creating gates directory: %w", err))
			}

			gatePath := filepath.Join(gatesDir, resolvedID+".yml")
			if err := os.WriteFile(gatePath, data, 0644); err != nil {
				exitOnError(fmt.Errorf("writing gate file: %w", err))
			}

			log.Info("gate file written",
				zap.String("story_id", resolvedID),
				zap.String("result", string(result)),
				zap.String("path", gatePath),
			)
			fmt.Printf("Wrote gate %s -> %s\n  %s\n", resolvedID, result, gatePath)
		},
	}

	cmd.Flags().StringVar(&concernsJSON, "concerns", "", `JSON array of concerns, e.g. '[{"severity":"high","note":"..."}]'`)
	return cmd
}
