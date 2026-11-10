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
		{"safe flags", m{"image": "redis:7", "command": l{"redis-server", "--appendonly", "yes", "--save", "60", "1"}}, true},
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
	}
	for _, c := range cases {
		if got := redisWithoutAuth(c.raw); got != c.want {
			t.Errorf("%s: redisWithoutAuth = %v, want %v", c.name, got, c.want)
		}
	}
}
