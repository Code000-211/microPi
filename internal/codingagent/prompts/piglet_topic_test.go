//go:build !pig_strip_docs

package prompts

import (
	"strings"
	"testing"
)

// A Piglet adds one topic after the last one and changes nothing else in the docs section; without a Piglet the section is
// microPi's text.
func TestPigletDocsTopicIsAdditive(t *testing.T) {
	stock := docsSectionAt("/docs", "", "")
	t.Cleanup(func() { SetPigletDocsTopic(false) })
	SetPigletDocsTopic(true)
	withPiglet := docsSectionAt("/docs", "", "")
	// The Piglet topic is PiG's addition after microPi's topics (D23), not a change to them.
	const topic = ", Piglets"
	if strings.Contains(stock, "Piglet") {
		t.Errorf("the stock docs section names the Piglet docs:\n%s", stock)
	}
	if got := strings.Replace(withPiglet, topic, "", 1); got != stock || !strings.Contains(withPiglet, "MCP"+topic+")") {
		t.Errorf("with a Piglet the section differs from stock by more than the topic:\n%s", withPiglet)
	}
	SetPigletDocsTopic(false)
	if again := docsSectionAt("/docs", "", ""); again != stock {
		t.Errorf("section after the Piglet topic is off differs from stock:\n%s", again)
	}
}
