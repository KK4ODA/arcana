package apps

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// ServiceHandler serves a native app's origin: it starts the program on
// the first request and forwards every request to it. Who may use the app
// is the caller's decision.
func (m *Manager) ServiceHandler(spec *Spec) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !spec.Service.CrossOrigin && !sameOriginWrite(r) {
			http.Error(w, "Cross-site request refused.", http.StatusForbidden)
			return
		}
		sv, err := m.service(spec.ID)
		if err != nil {
			_, st, _ := m.ready(spec.ID)
			if errors.Is(err, ErrNotReady) && st.State == "ready" {
				st.State, st.Problem = "failed", "Arcana is closing."
			}
			NotReady(w, st)
			return
		}
		sv.Ensure()
		base, ok := sv.Base()
		if !ok {
			ss := sv.Status()
			st := Status{Title: spec.Title, State: ss.State}
			switch ss.State {
			case "starting":
				if ss.Activity != "" {
					st.State, st.Problem = "starting", ss.Activity
				}
			case "failed":
				st.Problem = spec.Title + " could not start: " + ss.LastError + " Arcana tries again a little later; its Apps page shows the state."
			default:
				st.State = "starting"
			}
			NotReady(w, st)
			return
		}
		sv.begin()
		defer sv.end()
		target, _ := url.Parse(base)
		tls := r.TLS != nil
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				// Programs build links from the Host the browser used.
				pr.Out.Host = pr.In.Host
				pr.SetXForwarded()
			},
			// Video, music and event streams are passed on as they come.
			FlushInterval: -1,
			ModifyResponse: func(resp *http.Response) error {
				resp.Header.Add("Content-Security-Policy", ServiceCSP)
				if loc := resp.Header.Get("Location"); tls && strings.HasPrefix(loc, "http://"+r.Host+"/") {
					resp.Header.Set("Location", "https://"+strings.TrimPrefix(loc, "http://"))
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				if r.Context().Err() != nil {
					return // the browser went away
				}
				NotReady(w, Status{Title: spec.Title, State: "failed", Problem: spec.Title + " did not answer: " + err.Error()})
			},
		}
		rp.ServeHTTP(w, r)
	})
}

// sameOriginWrite refuses changes requested by other origins, such as an
// archive page on the content origin posting a form to an app. The apps
// check their own sessions; this keeps a page elsewhere from riding on
// them.
func sameOriginWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		scheme := "http://"
		if r.TLS != nil {
			scheme = "https://"
		}
		return origin == scheme+r.Host
	}
	return true
}
