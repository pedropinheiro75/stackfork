package dashboard

import (
	"fmt"
	"net/http"

	"github.com/pedropinheiro75/stackfork/internal/proxy"
)

// withSecurity wraps every route with the D5 response headers and the Host
// allowlist. A disallowed Host gets 421 and nothing else - no route runs,
// no data is served (design.md D5's DNS-rebinding scenario).
func (s *Server) withSecurity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")

		if !s.hostAllowed(r.Host) {
			http.Error(w, "host not allowed", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed implements design.md D5's Host allowlist: 127.0.0.1:<port>,
// localhost:<port>, stackfork.localhost and stackfork.localhost:<proxyport>.
// The proxy port is read fresh (a cheap file read, no docker call) so a
// proxy started or reconfigured after the dashboard started is still
// recognised.
func (s *Server) hostAllowed(host string) bool {
	switch host {
	case fmt.Sprintf("127.0.0.1:%d", s.Port),
		fmt.Sprintf("localhost:%d", s.Port),
		"stackfork.localhost":
		return true
	}
	if proxyPort, err := proxy.ReadPort(s.NewStateBase); err == nil {
		if host == fmt.Sprintf("stackfork.localhost:%d", proxyPort) {
			return true
		}
	}
	return false
}

// originAllowed checks a POST's Origin header against the same allowed
// hosts (design.md D5): "http://" or "https://" followed by an allowed
// Host value. An empty Origin (not sent by browsers for a same-origin
// request in every case, but never trusted here) is rejected.
func (s *Server) originAllowed(origin string) bool {
	for _, scheme := range []string{"http://", "https://"} {
		if len(origin) > len(scheme) && origin[:len(scheme)] == scheme {
			if s.hostAllowed(origin[len(scheme):]) {
				return true
			}
		}
	}
	return false
}
