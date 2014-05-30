/*
 * Copyright (c) 2013 Matt Jibson <matt.jibson@gmail.com>
 *
 * Permission to use, copy, modify, and distribute this software for any
 * purpose with or without fee is hereby granted, provided that the above
 * copyright notice and this permission notice appear in all copies.
 *
 * THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
 * WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
 * MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
 * ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
 * WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
 * ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
 * OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
 */

package appstats

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"appengine"
	"appengine/memcache"
	"appengine/user"
)

var templates *template.Template
var staticFiles map[string][]byte

func init() {
	templates = template.New("appstats").Funcs(funcs)
	templates.Parse(htmlBase)
	templates.Parse(htmlMain)
	templates.Parse(htmlDetails)
	templates.Parse(htmlFile)

	staticFiles = map[string][]byte{
		"app_engine_logo_sm.gif": app_engine_logo_sm_gif,
		"appstats_css.css":       appstats_css_css,
		"appstats_js.js":         appstats_js_js,
		"gantt.js":               gantt_js,
		"minus.gif":              minus_gif,
		"pix.gif":                pix_gif,
		"plus.gif":               plus_gif,
	}
}

func serveError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func AppstatsHandler(w http.ResponseWriter, r *http.Request) {
	c := appengine.NewContext(r)
	if appengine.IsDevAppServer() {
		// noop
	} else if u := user.Current(c); u == nil {
		if loginURL, err := user.LoginURL(c, r.URL.String()); err == nil {
			http.Redirect(w, r, loginURL, http.StatusTemporaryRedirect)
		} else {
			serveError(w, err)
		}
		return
	} else if !u.Admin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if detailsURL == r.URL.Path {
		Details(w, r)
	} else if fileURL == r.URL.Path {
		File(w, r)
	} else if strings.HasPrefix(r.URL.Path, staticURL) {
		Static(w, r)
	} else {
		http.NotFound(w, r)
	}
}

func Details(w http.ResponseWriter, r *http.Request) {
	key := fmt.Sprintf(keyFull, r.FormValue("rid"))

	c := context(r)

	v := struct {
		Env             map[string]string
		Record          *RequestStats
		Header          http.Header
		AllStatsByCount StatsByName
		Real            time.Duration
	}{
		Env: map[string]string{
			"APPLICATION_ID": appengine.AppID(c),
		},
	}

	item, err := memcache.Get(c, key)
	if err != nil {
		templates.ExecuteTemplate(w, "details", v)
		return
	}

	full := stats_full{}
	err = gob.NewDecoder(bytes.NewBuffer(item.Value)).Decode(&full)
	if err != nil {
		templates.ExecuteTemplate(w, "details", v)
		return
	}

	byCount := make(map[string]cVal)
	durationCount := make(map[string]time.Duration)
	var _real time.Duration
	for _, r := range full.Stats.RPCStats {
		rpc := r.Name()

		// byCount
		if _, present := byCount[rpc]; !present {
			durationCount[rpc] = 0
		}
		v := byCount[rpc]
		v.count++
		v.cost += r.Cost
		byCount[rpc] = v
		durationCount[rpc] += r.Duration
		_real += r.Duration
	}

	allStatsByCount := StatsByName{}
	for k, v := range byCount {
		allStatsByCount = append(allStatsByCount, &StatByName{
			Name:     k,
			Count:    v.count,
			Cost:     v.cost,
			Duration: durationCount[k],
		})
	}
	sort.Sort(allStatsByCount)

	v.Record = full.Stats
	v.Header = full.Header
	v.AllStatsByCount = allStatsByCount
	v.Real = _real

	_ = templates.ExecuteTemplate(w, "details", v)
}

func File(w http.ResponseWriter, r *http.Request) {
	fname := r.URL.Query().Get("f")
	n := r.URL.Query().Get("n")
	lineno, _ := strconv.Atoi(n)
	c := context(r)

	f, err := ioutil.ReadFile(fname)
	if err != nil {
		serveError(w, err)
		return
	}

	fp := make(map[int]string)
	for k, v := range strings.Split(string(f), "\n") {
		fp[k+1] = v
	}

	v := struct {
		Env      map[string]string
		Filename string
		Lineno   int
		Fp       map[int]string
	}{
		Env: map[string]string{
			"APPLICATION_ID": appengine.AppID(c),
		},
		Filename: fname,
		Lineno:   lineno,
		Fp:       fp,
	}

	_ = templates.ExecuteTemplate(w, "file", v)
}

func Static(w http.ResponseWriter, r *http.Request) {
	fname := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if v, present := staticFiles[fname]; present {
		h := w.Header()

		if strings.HasSuffix(r.URL.Path, ".css") {
			h.Set("Content-type", "text/css")
		} else if strings.HasSuffix(r.URL.Path, ".js") {
			h.Set("Content-type", "text/javascript")
		}

		h.Set("Cache-Control", "public, max-age=expiry")
		expires := time.Now().Add(time.Hour)
		h.Set("Expires", expires.Format(time.RFC1123))

		w.Write(v)
	}
}
