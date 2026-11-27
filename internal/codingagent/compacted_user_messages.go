// SPDX-License-Identifier: MIT

package codingagent

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// µPi (d8371227, 0abf563e, 4aab2a69): a summary drops requirements and constraints of the task, so the user's own messages
// from before a compaction's kept range go with the summary into the model's context, verbatim and oldest first
// (session-manager.ts userMessagesBefore, compactedUserMessages; messages.ts compactionSummaryText).

// CompactedUserMessagesMaxChars is the most characters (UTF-16 units) of the user's compacted messages that a compaction
// summary carries verbatim (session-manager.ts COMPACTED_USER_MESSAGES_MAX_CHARS).
const CompactedUserMessagesMaxChars = 16_000

// CompactedUserMessageCut is how a message cut at CompactedUserMessagesMaxChars ends (session-manager.ts COMPACTED_USER_MESSAGE_CUT).
const CompactedUserMessageCut = "\n[cut here: the rest is in the summary]"

// compactionUserMessagesOff turns the feature off (compaction/features.ts compactionFeatures.userMessages): tests of Pi's own
// compaction (its requests, its projections) turn it off; nothing else does.
var compactionUserMessagesOff atomic.Bool

// CompactionUserMessagesEnabled reports whether the user's compacted messages stay verbatim beside the summary.
func CompactionUserMessagesEnabled() bool { return !compactionUserMessagesOff.Load() }

// SetCompactionUserMessages turns the feature on or off and returns the function that restores the previous state. For tests of
// Pi's own compaction (compactionFeatures.userMessages = false).
func SetCompactionUserMessages(on bool) (restore func()) {
	previous := compactionUserMessagesOff.Swap(!on)
	return func() { compactionUserMessagesOff.Store(previous) }
}

// userText is the trimmed text of a user message entry, "" for any other entry (session-manager.ts userText): text parts joined
// with newlines, a non-text part joining as an empty string. Harness and extension messages are custom entries, not the user's.
func userText(entry SessionEntry, decode messageDecoder) string {
	if entry == nil || entry.Base().Type != "message" {
		return ""
	}
	message, ok := decode(entry)
	if !ok || message.Message.User == nil {
		return ""
	}
	return userMessageText(message.Message.User)
}

func userMessageText(user *agent.UserMessage) string {
	var text string
	switch content := user.Content.(type) {
	case ai.UserText:
		text = string(content)
	case ai.UserContentBlocks:
		parts := make([]string, len(content))
		for i, block := range content {
			switch block := block.(type) {
			case ai.TextContent:
				parts[i] = block.Text
			case *ai.TextContent:
				if block != nil {
					parts[i] = block.Text
				}
			}
		}
		text = strings.Join(parts, "\n")
	}
	return jsstring.Trim(text)
}

// UserMessagesBefore is the text of the user's own messages in path before keptIndex, oldest first, within
// CompactedUserMessagesMaxChars: the first (the task, mostly; cut there when it is longer, which it then says), then the newest that
// fit whole. Those in between that do not fit are left to the summary; complete says whether all stayed, uncut. A message that a later
// one of path contains whole is left out: a run that continues a session is often sent its task again with notes added
// (session-manager.ts userMessagesBefore).
func UserMessagesBefore(path []SessionEntry, keptIndex int) (kept []string, complete bool) {
	texts := make([]string, 0, 16)
	before := 0
	for i, entry := range path {
		text := userText(entry, asMessage)
		if text == "" {
			continue
		}
		if i < keptIndex {
			before++
		}
		texts = append(texts, text)
	}
	return userMessagesBeforeTexts(texts, before)
}

// userMessagesBeforeTexts is UserMessagesBefore of the non-empty user texts of a path, in path order, of which the first before
// precede the kept index.
func userMessagesBeforeTexts(all []string, before int) (kept []string, complete bool) {
	var texts []string
	for i := range before {
		text := all[i]
		contained := false
		for _, later := range all[i+1:] {
			if len(later) >= len(text) && strings.Contains(later, text) {
				contained = true
				break
			}
		}
		if !contained {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return []string{}, true
	}
	first, rest := texts[0], texts[1:]
	firstLength := jsstring.Length(first)
	room := CompactedUserMessagesMaxChars - firstLength
	newest := 0
	for i := len(rest) - 1; i >= 0; i-- {
		length := jsstring.Length(rest[i])
		if length > room {
			break
		}
		newest++
		room -= length
	}
	cut := firstLength > CompactedUserMessagesMaxChars
	head := first
	if cut {
		head = jsstring.Slice(first, 0, CompactedUserMessagesMaxChars) + CompactedUserMessageCut
	}
	kept = make([]string, 0, 1+newest)
	kept = append(kept, head)
	kept = append(kept, rest[len(rest)-newest:]...)
	return kept, !cut && newest == len(rest)
}

// compactedUserMessagesOfPath is the user's messages that the compaction at path[at] carries (session-manager.ts
// compactedUserMessages): those of the path before it, before its first kept entry.
func compactedUserMessagesOfPath(path []SessionEntry, at int, firstKeptEntryID string, decode messageDecoder) []string {
	before := path[:at]
	texts := make([]string, 0, 16)
	keptAt := len(before)
	count := 0
	for i, entry := range before {
		if entry.Base().ID == firstKeptEntryID && keptAt == len(before) {
			keptAt = i
			count = len(texts)
		}
		if text := userText(entry, decode); text != "" {
			texts = append(texts, text)
		}
	}
	if keptAt == len(before) {
		count = len(texts)
	}
	kept, _ := userMessagesBeforeTexts(texts, count)
	return kept
}

// carriedUserMessages supplies the user's messages a compaction entry carries; nil computes them from the projected path itself.
type carriedUserMessages func(compaction CompactionEntry) []string

// withUserRequests sets userRequests on the compaction summary message of messages (session-manager.ts projectSessionPath):
// `{ ...message, userRequests }`, so the summary message is a new object and the entry's is left as it was.
func withUserRequests(messages []agent.AgentMessage, requests []string) {
	for i, message := range messages {
		if message.Custom == nil {
			continue
		}
		if role, _ := message.Custom["role"].(string); role != agent.RoleCompactionSummary {
			continue
		}
		custom := make(map[string]any, len(message.Custom)+1)
		for key, value := range message.Custom {
			custom[key] = value
		}
		custom["userRequests"] = requests
		messages[i] = agent.AgentMessage{Custom: custom}
	}
}

// carriedCache holds, by compaction slot, the user's messages the compaction carries: a path's entries before a compaction do not
// change, so neither do they (session-manager.ts compactedUserMessagesCache).
type carriedCache struct {
	mu     sync.Mutex
	bySlot map[int]carriedEntry
}

type carriedEntry struct {
	id    string
	texts []string
}

// carriedUserMessagesLocked returns the user's messages that compaction carries, from the whole path before it: the context path
// keeps none of the messages before the latest compaction's kept range, so they are read here, only the user's, once per compaction.
// The caller holds s.mu.
func (s *Session) carriedUserMessagesLocked(compaction CompactionEntry) []string {
	idx, ok := s.slotOf(compaction.ID)
	if !ok {
		return nil
	}
	s.carried.mu.Lock()
	if cached, ok := s.carried.bySlot[idx]; ok && cached.id == compaction.ID {
		s.carried.mu.Unlock()
		return cached.texts
	}
	s.carried.mu.Unlock()
	path := s.walkPathLocked(compaction.ID)
	kept, _ := userMessagesBeforeTexts(s.userTextsLocked(path[:max(0, len(path)-1)], compaction.FirstKeptEntryID))
	s.carried.mu.Lock()
	if s.carried.bySlot == nil {
		s.carried.bySlot = map[int]carriedEntry{}
	}
	s.carried.bySlot[idx] = carriedEntry{id: compaction.ID, texts: kept}
	s.carried.mu.Unlock()
	return kept
}

// userTextsLocked returns the non-empty user texts of the slot path, in path order, and how many of them precede the entry
// firstKeptEntryID (all of them when it is not on the path). Only the user's message entries are read. The caller holds s.mu.
func (s *Session) userTextsLocked(path []int32, firstKeptEntryID string) (texts []string, before int) {
	var users []int32
	keptAt := -1
	for _, i := range path {
		if keptAt < 0 && s.idAt(int(i)) == firstKeptEntryID {
			keptAt = len(users)
		}
		if slot := &s.slots[i]; slot.typ() == typeMessage && slot.role() == agent.RoleUser {
			users = append(users, i)
		}
	}
	texts = make([]string, 0, len(users))
	for k, entry := range s.entriesAtLocked(users) {
		text := userText(entry, asMessage)
		if text == "" {
			continue
		}
		if keptAt < 0 || k < keptAt {
			before++
		}
		texts = append(texts, text)
	}
	return texts, before
}

// UserMessagesBeforeEntry is UserMessagesBefore of the current branch, before the entry firstKeptEntryID (the branch's length when it
// is not on it), read from the whole branch: the context branch that compaction preparation reads keeps none of the messages before the
// latest compaction's kept range (compaction.ts prepareCompaction passes getBranch()).
func (s *Session) UserMessagesBeforeEntry(firstKeptEntryID string) (kept []string, complete bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.leafID == nil {
		return []string{}, true
	}
	return userMessagesBeforeTexts(s.userTextsLocked(s.pathIndexesLocked(*s.leafID), firstKeptEntryID))
}
