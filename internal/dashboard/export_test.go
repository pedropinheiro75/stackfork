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
