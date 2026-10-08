package dashboard

import "github.com/pedropinheiro75/stackfork/internal/inventory"

// dotClass picks a service's status colour from its state and health:
// green running (healthy, or no healthcheck), amber unhealthy or still
// starting, red exited/dead/not started, grey only for an optional service.
// app.js's dotClass must match.
func dotClass(s inventory.Service) string {
	if s.State == "running" {
		switch s.Health {
		case "unhealthy":
			return "unhealthy"
		case "health: starting":
			return "starting"
		}
	}
	return s.State
}

func dotTitle(s inventory.Service) string {
	switch {
	case s.State == inventory.ServiceOptional:
		return "not started (optional)"
	case s.State == inventory.StateNotStarted:
		return "not started"
	case s.State == "running" && s.Health == "unhealthy":
		return "running, unhealthy"
	case s.State == "running" && s.Health == "health: starting":
		return "running, health check starting"
	}
	return s.State
}
