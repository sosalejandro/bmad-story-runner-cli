package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sosalejandro/bmad-story-runner-cli/application"
	"github.com/sosalejandro/bmad-story-runner-cli/domain"
	"github.com/sosalejandro/bmad-story-runner-cli/infrastructure"
)

func newAddConcernsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   `add-concerns <progress-json> <story-id> <concerns-json>`,
		Short: "Append QA concerns to a story",
		Long:  `concerns-json must be a JSON array, e.g. '[{"severity":"high","note":"missing test"}]'`,
		Args:  cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			var concerns []domain.QAConcern
			if err := json.Unmarshal([]byte(args[2]), &concerns); err != nil {
				exitOnError(fmt.Errorf("parsing concerns JSON: %w", err))
			}

			store := infrastructure.NewJSONProgressStore(log)
			uc := application.NewAddConcernsUseCase(store, log)
			exitOnError(uc.Execute(args[0], args[1], concerns))
			fmt.Printf("Added %d concern(s) to %s\n", len(concerns), args[1])
		},
	}
}
