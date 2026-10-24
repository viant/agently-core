package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// A browser cookie is ambient authority. An unsafe request authenticated by
// that cookie must originate from this host; a verified Bearer-only request is
// not subject to this browser check. Authentication is classified by the
// session ID returned from the runtime, never by a supplied Bearer header.
func cookieUnsafeRequestAllowed(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return true
	}
	if site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))); site != "" && site != "same-origin" {
		return false
	}
	origins := r.Header.Values("Origin")
	if len(origins) > 1 {
		return false
	}
	if len(origins) == 1 {
		return sameRequestOrigin(r, origins[0])
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return sameRequestOrigin(r, referer)
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "same-origin")
}

func sameRequestOrigin(r *http.Request, raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.User != nil || u.Opaque != "" || u.Host == "" || !strings.EqualFold(u.Host, r.Host) || u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if r.TLS != nil {
		return u.Scheme == "https"
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded == "http" || forwarded == "https" {
		return u.Scheme == forwarded
	}
	return u.Scheme == "http"
}
