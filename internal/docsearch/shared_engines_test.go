package docsearch

// closeSharedEngines closes and forgets the engines OpenProject shares, so a test's background rebuild does not write into a
// temporary directory the test is removing.
func closeSharedEngines() {
	enginesMu.Lock()
	open := make([]*Engine, 0, len(engines))
	for key, eng := range engines {
		open = append(open, eng)
		delete(engines, key)
	}
	enginesMu.Unlock()
	for _, eng := range open {
		_ = eng.Close()
	}
}
