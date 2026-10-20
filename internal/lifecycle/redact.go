package lifecycle

import (
	"io"

	"github.com/pedropinheiro75/stackfork/internal/compose"
	"github.com/pedropinheiro75/stackfork/internal/redact"
	"github.com/pedropinheiro75/stackfork/internal/state"
)

// SecretsForRedaction resolves an instance's named secret references into
// their current values (design.md D6/D5), for internal/redact. A reference
// that fails to resolve right now (an env var since unset, say) is skipped
// rather than failing the log request - log redaction is best-effort, never
// a reason logs can't be viewed at all.
func SecretsForRedaction(info *state.Manifest) []redact.Secret {
	var secrets []redact.Secret
	mapped, _ := MappedSecrets(info.Root)
	refs, _, _ := InstanceSecrets(info, mapped)
	for name, ref := range refs {
		if value, err := compose.ResolveSecret(name, ref); err == nil {
			secrets = append(secrets, redact.Secret{Name: name, Value: value})
		}
	}
	return secrets
}

// NewRedactedWriter wraps dst with a redact.Writer built from info's
// resolved secrets, for `stackfork logs` and the dashboard's log view alike
// (task 4.1a).
func NewRedactedWriter(dst io.Writer, info *state.Manifest) *redact.Writer {
	return redact.NewWriter(dst, SecretsForRedaction(info))
}
