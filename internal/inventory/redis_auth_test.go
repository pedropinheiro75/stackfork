package inventory

import "testing"

// Only a Redis known to have no password loses the "<password>"
// placeholder; anything unknown keeps it.
func TestRedisWithoutAuth(t *testing.T) {
	type m = map[string]interface{}
	type l = []interface{}
	cases := []struct {
		name string
		raw  m
		want bool
	}{
		{"stock image", m{"image": "redis:7"}, true},
		{"stock image with registry", m{"image": "docker.io/library/redis:7-alpine"}, true},
		{"valkey", m{"image": "valkey/valkey:8"}, true},
		{"safe flags", m{"image": "redis:7", "command": l{"redis-server", "--save", "60", "1", "--loglevel", "warning"}}, true},
		{"appendonly replays an AOF", m{"image": "redis:7-alpine", "command": l{"redis-server", "--appendonly", "yes"}, "volumes": l{"./data:/data"}}, false},
		{"safe string command", m{"image": "redis:7", "command": "redis-server --maxmemory 100mb"}, true},
		{"data volume", m{"image": "redis:7", "volumes": l{"redisdata:/data"}}, true},
		{"data volume long form", m{"image": "redis:7", "volumes": l{m{"type": "volume", "source": "d", "target": "/data"}}}, true},

		{"requirepass", m{"image": "redis:7", "command": l{"redis-server", "--requirepass", "${PW}"}}, false},
		{"requirepass string", m{"image": "redis:7", "command": "redis-server --requirepass x"}, false},
		{"inline ACL user", m{"image": "redis:7", "command": l{"redis-server", "--user", "default", "on", ">secret", "~*", "+@all"}}, false},
		{"aclfile", m{"image": "redis:7", "command": l{"redis-server", "--aclfile", "/data/users.acl"}}, false},
		{"config file", m{"image": "redis:7", "command": l{"redis-server", "/usr/local/etc/redis/redis.conf"}}, false},
		{"shell", m{"image": "redis:7", "command": l{"sh", "-c", "redis-server --requirepass $PW"}}, false},
		{"entrypoint", m{"image": "redis:7", "entrypoint": l{"redis-server", "--requirepass", "secret"}}, false},
		{"env_file", m{"image": "redis:7", "env_file": l{".env"}}, false},
		{"REDIS_PASSWORD", m{"image": "redis:7", "environment": m{"REDIS_PASSWORD": "x"}}, false},
		{"password file", m{"image": "redis:7", "environment": l{"REDIS_PASSWORD_FILE=/run/secrets/pw"}}, false},
		{"REDISCLI_AUTH", m{"image": "redis:7", "environment": m{"REDISCLI_AUTH": "x"}}, false},
		{"bitnami", m{"image": "bitnami/redis:7.4"}, false},
		{"custom image", m{"image": "acme/redis-with-auth:1"}, false},
		{"redis-stack", m{"image": "redis/redis-stack:latest"}, false},
		{"mounted config", m{"image": "redis:7", "volumes": l{"./redis.conf:/usr/local/etc/redis/redis.conf"}}, false},
		{"compose config", m{"image": "redis:7", "configs": l{m{"source": "redis_conf", "target": "/etc/redis.conf"}}}, false},
		{"post_start hook", m{"image": "redis:7-alpine", "post_start": l{m{"command": l{"redis-cli", "CONFIG", "SET", "requirepass", "x"}}}}, false},
		{"build", m{"image": "redis:7", "build": m{"context": "."}}, false},
		{"compose secret", m{"image": "redis:7", "secrets": l{"redis_pw"}}, false},
		{"unknown key", m{"image": "redis:7", "x-future": true}, false},
		{"compose config nulls", m{"image": "redis:7-alpine", "command": nil, "entrypoint": nil, "labels": m{}, "networks": m{"default": nil}, "ports": l{m{"target": 6379}}}, true},
		{"healthcheck that sets a password", m{"image": "redis:7-alpine", "healthcheck": m{"test": l{"CMD", "redis-cli", "CONFIG", "SET", "requirepass", "x"}}}, false},
		{"PATH override", m{"image": "redis:7-alpine", "environment": m{"PATH": "/data:/usr/local/bin:/usr/bin:/bin"}, "volumes": l{"./bin:/data"}}, false},
		{"any variable", m{"image": "redis:7", "environment": m{"TZ": "UTC"}}, false},
		{"empty environment", m{"image": "redis:7", "environment": m{}}, true},
		{"ping healthcheck string", m{"image": "redis:7", "healthcheck": m{"test": "CMD-SHELL redis-cli ping"}}, true},
		{"usual keys", m{"image": "redis:7", "ports": l{"6379"}, "networks": m{"default": nil}, "restart": "always", "labels": m{"a": "b"}, "healthcheck": m{"test": l{"CMD", "redis-cli", "ping"}}}, true},
	}
	for _, c := range cases {
		if got := redisWithoutAuth(c.raw); got != c.want {
			t.Errorf("%s: redisWithoutAuth = %v, want %v", c.name, got, c.want)
		}
	}
}
