package compose

import "testing"

func TestWebServerServiceIsNotAUtility(t *testing.T) {
	for _, image := range []string{"nginx", "nginx:1.27", "caddy:2", "traefik:v3", "httpd:2.4", "haproxy:3", "envoyproxy/envoy:v1", "openresty/openresty:alpine"} {
		svc := map[string]interface{}{"image": image}
		if !WebServerService(svc) || UtilityService(svc) {
			t.Errorf("%s: web server %v, utility %v", image, WebServerService(svc), UtilityService(svc))
		}
	}
	if WebServerService(map[string]interface{}{"image": "postgres:17"}) {
		t.Error("postgres is no web server")
	}
}
