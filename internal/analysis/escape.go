package analysis

// performEscapeAnalysis determines which variables need heap allocation
func (a *Analyzer) performEscapeAnalysis() {
	// A variable escapes if it's captured by any closure
	for _, captures := range a.info.Captures {
		for _, cv := range captures.CapturedVars {
			a.info.Escapes[cv.Symbol] = true
		}
	}
}
