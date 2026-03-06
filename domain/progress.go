package domain

import "time"

type ProgressFile struct {
	Version     int       `json:"version"`
	DocsFolder  string    `json:"docs_folder"`
	LastUpdated time.Time `json:"last_updated"`
	Stories     []*Story  `json:"stories"`
}

// CompleteIDs returns a set of story IDs that are in complete status.
func (p *ProgressFile) CompleteIDs() map[string]bool {
	ids := make(map[string]bool, len(p.Stories))
	for _, s := range p.Stories {
		if s.Status == StatusComplete {
			ids[s.ID] = true
		}
	}
	return ids
}

// FindByID returns the story with the given ID, or nil.
func (p *ProgressFile) FindByID(id string) *Story {
	for _, s := range p.Stories {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// EligibleStories returns pending stories with all blockers complete, optionally filtered by group.
func (p *ProgressFile) EligibleStories(group *int) []*Story {
	complete := p.CompleteIDs()
	var eligible []*Story
	for _, s := range p.Stories {
		if group != nil && (s.ParallelGroup == nil || *s.ParallelGroup != *group) {
			continue
		}
		if s.IsEligible(complete) {
			eligible = append(eligible, s)
		}
	}
	return eligible
}

// StatusCounts returns a map of status -> count.
func (p *ProgressFile) StatusCounts() map[Status]int {
	counts := make(map[Status]int)
	for _, s := range p.Stories {
		counts[s.Status]++
	}
	return counts
}
