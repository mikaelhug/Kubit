package api

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
)

func (s *Server) authorized(r *http.Request) bool {
	if s.token == "" {
		return loopbackPeer(r)
	}
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		bearer = r.URL.Query().Get("token")
	}
	return bearer != "" && subtle.ConstantTimeCompare([]byte(bearer), []byte(s.token)) == 1
}

func loopbackPeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func openPath(p string) bool {
	return p == "/api/v1/version"
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") && !openPath(r.URL.Path) && !s.authorized(r) {
		writeErr(w, &statusError{Status: http.StatusUnauthorized, Msg: "unauthorized", Code: "unauthorized"})
		return
	}
	s.mux.ServeHTTP(w, r)
}
