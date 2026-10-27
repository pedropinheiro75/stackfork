package dashboard

// Test seams for the external dashboard_test package.
const SecretDialogScript = secretDialogScript

var AskSecretValueNative = askSecretValueNative

// SetOsascriptForTest points the dialog at a stub and returns the undo.
func SetOsascriptForTest(path string) func() {
	old := osascriptPath
	osascriptPath = path
	return func() { osascriptPath = old }
}

// ResumeUp runs the up a resume runs for one instance.
func (s *Server) ResumeUp(project string) (bool, string) {
	r := s.runAction(actionRequest{Action: "up", Project: project, trigger: TriggerResume})
	return r.OK, r.Message
}

// ResumeBlock exposes resumeBlock.
var ResumeBlock = resumeBlock
