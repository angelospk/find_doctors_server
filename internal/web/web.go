// The one screen this API was missing.
//
// The aggregator has been able to answer "where is the soonest dermatologist I
// can get to" for months, and the only way to ask it was curl. This package
// embeds a single HTML file and serves it, which is the whole of the frontend
// build system: no bundler, no node_modules, no second deployment. One person
// maintains this, and a build step that rots is worse than markup that is a bit
// long.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"
	"time"
)

//go:embed app.html
var files embed.FS

// Handler serves the app at "/" and 404s everything else it is given, so it can
// be mounted on the catch-all pattern without swallowing typos as the homepage.
func Handler() http.Handler {
	page, err := files.ReadFile("app.html")
	if err != nil {
		// Unreachable: the file is embedded at compile time. Panicking beats a
		// server that starts and serves an empty page.
		panic(err)
	}
	// The ETag is the content, hashed. A fixed modtime was tried first and was
	// wrong in the way that costs an afternoon: it never changes, so a rebuilt
	// binary with a new page still answers 304 and the browser keeps showing
	// the old one. Content-addressed, a redeploy invalidates itself and an
	// unchanged one still gets its 304.
	sum := sha256.Sum256(page)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The page talks only to this origin and loads nothing from anywhere
		// else, so it can say so.
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'none'; connect-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src 'self' data:; form-action 'none'; base-uri 'none'",
		)
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.ServeContent(w, r, "app.html", time.Time{}, newReader(page))
	})
}
