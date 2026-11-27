package tools

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// µPi: a background request through an alias (run_in_background, is_background) keeps bash from starting early, as background does: the
// run applies the alias before it starts.
func TestBashDoesNotStartEarlyForABackgroundAlias(t *testing.T) {
	tool := &BashTool{}
	for _, args := range []string{`{"command":"cat big.log","run_in_background":true}`, `{"command":"cat big.log","is_background":true}`, `{"command":"cat big.log","background":true}`} {
		if tool.StartsEarly([]byte(args)) {
			t.Errorf("StartsEarly(%s) = true", args)
		}
	}
	if !tool.StartsEarly([]byte(`{"command":"cat big.log"}`)) {
		t.Error("a foreground read did not start early")
	}
	if agent.StartedEarly(context.Background()) {
		t.Error("a plain context reads as started early")
	}
}
