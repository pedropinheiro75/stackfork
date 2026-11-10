package inventory

import "testing"

// Only a Redis that plainly has no password loses the "<password>"
// placeholder; every sign of a password keeps it.
func TestRedisWithoutAuth(t *testing.T) {
	type m = map[string]interface{}
	cases := []struct {
		name string
		raw  m
		want bool
	}{
		{"stock image", m{"image": "redis:7"}, true},
		{"plain command", m{"image": "redis:7", "command": []interface{}{"redis-server", "--appendonly", "yes"}}, true},
		{"requirepass in command", m{"image": "redis:7", "command": []interface{}{"redis-server", "--requirepass", "${PW}"}}, false},
		{"requirepass in string command", m{"image": "redis:7", "command": "redis-server --requirepass x"}, false},
		{"entrypoint", m{"image": "redis:7", "entrypoint": []interface{}{"redis-server", "--requirepass", "secret"}}, false},
		{"env_file", m{"image": "redis:7", "env_file": []interface{}{".env"}}, false},
		{"REDIS_PASSWORD", m{"image": "redis:7", "environment": m{"REDIS_PASSWORD": "x"}}, false},
		{"password file variable", m{"image": "redis:7", "environment": []interface{}{"REDIS_PASSWORD_FILE=/run/secrets/pw"}}, false},
		{"bitnami", m{"image": "bitnami/redis:7.4"}, false},
		{"config file in command", m{"image": "redis:7", "command": []interface{}{"redis-server", "/usr/local/etc/redis/redis.conf"}}, false},
		{"mounted config", m{"image": "redis:7", "volumes": []interface{}{"./redis.conf:/usr/local/etc/redis/redis.conf"}}, false},
		{"aclfile", m{"image": "redis:7", "command": []interface{}{"redis-server", "--aclfile", "/data/users.acl"}}, false},
		{"compose config", m{"image": "redis:7", "configs": []interface{}{m{"source": "redis_conf", "target": "/etc/redis.conf"}}}, false},
	}
	for _, c := range cases {
		if got := redisWithoutAuth(c.raw); got != c.want {
			t.Errorf("%s: redisWithoutAuth = %v, want %v", c.name, got, c.want)
		}
	}
}
