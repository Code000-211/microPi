// SPDX-License-Identifier: MIT

package compaction

import "sync/atomic"

// µPi's additions to compaction, each on (compaction/features.ts compactionFeatures). Tests of Pi's own compaction (its requests, its
// thresholds) turn them off; nothing else does. The third, the user's compacted messages kept verbatim, is
// codingagent.SetCompactionUserMessages.
var (
	// aheadOff turns off the summary made in the background before the threshold is reached (compactionFeatures.ahead, CompactionAhead).
	aheadOff atomic.Bool
	// cachedPrefixOff turns off the summary asked as the continuation of the session's own request (compactionFeatures.cachedPrefix,
	// CompactOnCachedPrefix).
	cachedPrefixOff atomic.Bool
)

// AheadEnabled reports whether the summary is made ahead of the threshold.
func AheadEnabled() bool { return !aheadOff.Load() }

// CachedPrefixEnabled reports whether the summary is asked on the session's cached prefix.
func CachedPrefixEnabled() bool { return !cachedPrefixOff.Load() }

// SetAhead turns the summary made ahead on or off and returns the function that restores the previous state. For tests of Pi's own
// compaction (compactionFeatures.ahead = false).
func SetAhead(on bool) (restore func()) {
	previous := aheadOff.Swap(!on)
	return func() { aheadOff.Store(previous) }
}

// SetCachedPrefix turns the summary on the cached prefix on or off and returns the function that restores the previous state. For tests
// of Pi's own compaction (compactionFeatures.cachedPrefix = false).
func SetCachedPrefix(on bool) (restore func()) {
	previous := cachedPrefixOff.Swap(!on)
	return func() { cachedPrefixOff.Store(previous) }
}
