package dash

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPredicates(t *testing.T) {
	cases := []struct {
		remote, host   string
		local, private bool
	}{
		{"127.0.0.1:5555", "localhost:8080", true, true},
		{"[::1]:5555", "[::1]:8080", true, true},
		{"127.0.0.1:5555", "evil.example", false, true},    // DNS rebinding
		{"172.17.0.1:5555", "localhost:8080", false, true}, // Docker bridge
		{"10.1.2.3:5555", "svc:8080", false, true},
		{"192.168.1.5:5555", "192.168.1.2:8080", false, true},
		{"[fd00::1]:5555", "svc:8080", false, true},
		{"8.8.8.8:5555", "localhost:8080", false, false},
		{"[2001:db8::1]:5555", "localhost:8080", false, false},
		{"[::ffff:127.0.0.1]:5555", "localhost:8080", true, true},
		{"[::ffff:10.1.2.3]:5555", "localhost:8080", false, true},
		{"", "localhost:8080", false, false}, // unix socket listener
		{"@", "localhost:8080", false, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr, r.Host = c.remote, c.host
		if got := Localhost(r); got != c.local {
			t.Errorf("Localhost(%s, %s) = %v", c.remote, c.host, got)
		}
		if got := PrivateNetwork(r); got != c.private {
			t.Errorf("PrivateNetwork(%s, %s) = %v", c.remote, c.host, got)
		}
		if !Anyone(r) {
			t.Errorf("Anyone(%s) = false", c.remote)
		}
	}
}
