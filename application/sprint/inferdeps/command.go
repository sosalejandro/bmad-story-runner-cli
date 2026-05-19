package inferdeps

// RunOptions parameterises the end-to-end "parse → resolve → emit" flow.
// Kept separate from the cobra command so tests can drive the pipeline
// without cobra mechanics.
type RunOptions struct {
	EpicsPath         string // required — path to epics.md
	ScopeEpic         string // optional — restrict suggestions to one epic id
	Apply             bool   // when true, rewrite the file in place
	Backup            bool   // only meaningful with Apply=true; writes .bak
	IntraEpicFallback bool   // when true, enable LOW-confidence X.Y → X.(Y-1)
}

// Run executes the pipeline and returns a populated PatchResult. The
// cobra adapter wraps this in either text-mode or json-mode emission.
func Run(opts RunOptions) (*PatchResult, []string, error) {
	parsed, err := ParseEpics(opts.EpicsPath)
	if err != nil {
		return nil, nil, err
	}
	sugs := Resolve(parsed, opts.IntraEpicFallback)
	sugs = FilterByEpic(sugs, opts.ScopeEpic)

	withCues := 0
	withNew := 0
	for _, s := range sugs {
		if len(s.InferredDeps) > 0 {
			withCues++
		}
		if len(s.NewDeps()) > 0 {
			withNew++
		}
	}
	agg := ComputeAgreement(sugs)

	result := &PatchResult{
		EpicsFile:         opts.EpicsPath,
		TotalStories:      len(sugs),
		StoriesWithCues:   withCues,
		StoriesWithNew:    withNew,
		Suggestions:       sugs,
		ScopeEpic:         opts.ScopeEpic,
		IntraEpicFallback: opts.IntraEpicFallback,
		Agreement:         &agg,
	}

	var warnings []string
	if opts.Apply {
		patched, backupPath, skipped, aerr := ApplyPatches(opts.EpicsPath, sugs, opts.Backup)
		if aerr != nil {
			return result, nil, aerr
		}
		result.Applied = true
		result.StoriesPatched = patched
		result.BackupPath = backupPath
		for _, sid := range skipped {
			warnings = append(warnings, "skipped story "+sid+" — no existing frontmatter to patch (insert one first)")
		}
	}
	return result, warnings, nil
}
