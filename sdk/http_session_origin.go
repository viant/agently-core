package sdk

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Session cookies acquired by native clients still use the server's same-origin
// protection. Derive their origin from the configured API, never a redirect.
func (c *HTTPClient) applySessionOrigin(req *http.Request) {
	if req == nil || len(req.Header.Values("Origin")) != 0 || req.Header.Get("Authorization") != "" {
		return
	}
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || !sameHTTPOrigin(base, req.URL) || c.client.Jar == nil || len(c.client.Jar.Cookies(req.URL)) == 0 {
		return
	}
	req.Header.Set("Origin", base.Scheme+"://"+base.Host)
}

func sameHTTPOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && (a.Scheme == "http" || a.Scheme == "https") &&
		a.User == nil && b.User == nil && a.Host != "" && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (c *HTTPClient) configureSessionRedirects() {
	// Do not mutate a caller-owned client or its existing redirect policy.
	client := *c.client
	previous := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		for _, source := range via {
			if (len(source.Cookies()) > 0 || source.Header.Get("Authorization") != "") && !sameHTTPOrigin(source.URL, req.URL) {
				return fmt.Errorf("authenticated API request cannot redirect across origins")
			}
		}
		if previous != nil {
			return previous(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("stopped after 10 redirects")
		}
		return nil
	}
	c.client = &client
}
