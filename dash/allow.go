package dash

import (
	"net/http"
	"net/netip"

	"github.com/igorrius/promtop/internal/web"
)

// Localhost admits clients on a loopback address whose request also names
// a loopback host (the latter stops DNS-rebinding pages). It is the default:
// a dev machine and kubectl port-forward get in, ingress and other pods get
// 403. A reverse proxy on localhost looks local too.
func Localhost(r *http.Request) bool {
	a, ok := remote(r)
	return ok && a.IsLoopback() && web.LoopbackHost(r.Host)
}

// PrivateNetwork admits clients on loopback or private addresses
// (10/8, 172.16/12, 192.168/16, fc00::/7): what a browser on the host looks
// like to a service in Docker, docker-compose, WSL2 or a local VM. In
// Kubernetes it opens the dashboard to the whole cluster network.
func PrivateNetwork(r *http.Request) bool {
	a, ok := remote(r)
	return ok && (a.IsLoopback() || a.IsPrivate())
}

// Anyone admits every client.
func Anyone(*http.Request) bool { return true }

// remote is the client's address; false when it is not an IP (a unix socket).
func remote(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr().Unmap(), true
}
