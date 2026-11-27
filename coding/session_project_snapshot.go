package coding

import (
	"os"

	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// projectSnapshot is the listing of the working folder the prompt carries in its project section (µPi
// agent-session.ts _getProjectSnapshot): none in remote mode.
func (s *Session) projectSnapshot() string {
	remote := (s.remoteMgr != nil && s.remoteMgr.Active()) || os.Getenv("PI_REMOTE") != ""
	return prompts.SessionProjectSnapshot(s.services.CWD(), remote)
}
