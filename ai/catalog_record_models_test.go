package ai

// recordModels is the built-in catalog without µPi's Melious models, which are added without a catalog record (their encoding falls back to
// member-by-member): the tests of catalog records walk only the rows that have one.
func recordModels() []GeneratedModel {
	out := make([]GeneratedModel, 0, len(GeneratedModels))
	for _, model := range GeneratedModels {
		if model.Record != "" {
			out = append(out, model)
		}
	}
	return out
}
