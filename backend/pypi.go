package main

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

// pypi layout: uploaded/cached files at "packages/<name>/<file>",
// proxied simple pages cached verbatim at "simple/<name>/index.html".

var (
	pypiNormRe = regexp.MustCompile(`[-_.]+`)
	anchorRe   = regexp.MustCompile(`(?is)<a\s([^>]*)>`)
	hrefRe     = regexp.MustCompile(`(?i)href\s*=\s*"([^"]*)"`)
	dropAttrRe = regexp.MustCompile(`(?i)\s*(href|data-dist-info-metadata|data-core-metadata)\s*=\s*"[^"]*"`)
	acceptHTML = http.Header{"Accept": {"text/html"}}
	sdistExtRe = regexp.MustCompile(`\.(tar\.gz|tar\.bz2|tgz|zip)$`)
)

func pypiNormalize(name string) string {
	return strings.ToLower(pypiNormRe.ReplaceAllString(name, "-"))
}

func pypiVersion(file string) string {
	if strings.HasSuffix(file, ".whl") || strings.HasSuffix(file, ".egg") {
		if parts := strings.Split(file, "-"); len(parts) >= 2 {
			return parts[1]
		}
		return ""
	}
	base := sdistExtRe.ReplaceAllString(file, "")
	if i := strings.LastIndex(base, "-"); i >= 0 {
		return base[i+1:]
	}
	return ""
}

type pypiLink struct{ File, URL, Frag, Attrs string }

func (a *App) pypiHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	switch {
	case r.Method == http.MethodPost:
		a.pypiUpload(w, r, repo, u)
	case p == "simple" || p == "simple/":
		names := a.pypiProjects(repo, 0)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!DOCTYPE html>\n<html><head><title>Simple index</title></head><body>\n")
		for _, n := range names {
			fmt.Fprintf(w, "<a href=\"%s/\">%s</a><br/>\n", html.EscapeString(n), html.EscapeString(n))
		}
		fmt.Fprint(w, "</body></html>\n")
	case strings.HasPrefix(p, "simple/"):
		if !strings.HasSuffix(r.URL.Path, "/") { // links are relative to the trailing-slash URL
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		name := pypiNormalize(strings.Trim(strings.TrimPrefix(p, "simple/"), "/"))
		links, err := a.pypiLinks(r.Context(), repo, name, 0)
		if err != nil || len(links) == 0 {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<!DOCTYPE html>\n<html><head><title>Links for %[1]s</title></head><body>\n<h1>Links for %[1]s</h1>\n", html.EscapeString(name))
		for _, l := range links {
			href := "../../packages/" + url.PathEscape(name) + "/" + url.PathEscape(l.File)
			if l.Frag != "" {
				href += "#" + l.Frag
			}
			fmt.Fprintf(w, "<a href=\"%s\" %s>%s</a><br/>\n", html.EscapeString(href), l.Attrs, html.EscapeString(l.File))
		}
		fmt.Fprint(w, "</body></html>\n")
	case strings.HasPrefix(p, "packages/") && validPath(p):
		x, err := a.resolve(r.Context(), repo, p, nil, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	default:
		http.NotFound(w, r)
	}
}

// pypiLinks gathers distribution files for a project across the repository (and group members).
func (a *App) pypiLinks(ctx context.Context, repo *Repo, name string, depth int) ([]pypiLink, error) {
	switch repo.Type {
	case "hosted":
		list, err := a.assetsUnder(repo.Name, "packages/"+name+"/")
		var out []pypiLink
		for _, x := range list {
			out = append(out, pypiLink{File: path.Base(x.Path), Frag: "sha256=" + x.Blob})
		}
		return out, err
	case "proxy":
		return a.pypiRemoteLinks(ctx, repo, name)
	case "group":
		var out []pypiLink
		seen := map[string]bool{}
		if depth > 5 {
			return nil, nil
		}
		for _, m := range a.members(repo) {
			links, _ := a.pypiLinks(ctx, m, name, depth+1)
			for _, l := range links {
				if !seen[l.File] {
					seen[l.File] = true
					out = append(out, l)
				}
			}
		}
		return out, nil
	}
	return nil, errNotFound
}

func (a *App) pypiRemoteLinks(ctx context.Context, repo *Repo, name string) ([]pypiLink, error) {
	x, err := a.proxyGet(ctx, repo, "simple/"+name+"/index.html", acceptHTML)
	if err != nil {
		return nil, err
	}
	data, err := a.readAsset(x)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimSuffix(repo.RemoteURL, "/") + "/simple/" + name + "/")
	if err != nil {
		return nil, err
	}
	var out []pypiLink
	for _, m := range anchorRe.FindAllStringSubmatch(string(data), -1) {
		h := hrefRe.FindStringSubmatch(m[1])
		if h == nil {
			continue
		}
		u, err := base.Parse(html.UnescapeString(h[1]))
		if err != nil {
			continue
		}
		frag := u.Fragment
		u.Fragment = ""
		out = append(out, pypiLink{
			File: path.Base(u.Path), URL: u.String(), Frag: frag,
			Attrs: strings.TrimSpace(dropAttrRe.ReplaceAllString(m[1], "")),
		})
	}
	return out, nil
}

// pypiFileURL finds the upstream URL for a file via the cached project page,
// so the proxy only ever fetches URLs the upstream index advertised.
func (a *App) pypiFileURL(ctx context.Context, repo *Repo, name, file string) (string, error) {
	links, err := a.pypiRemoteLinks(ctx, repo, name)
	if err != nil {
		return "", err
	}
	for _, l := range links {
		if l.File == file {
			return l.URL, nil
		}
	}
	return "", errNotFound
}

func (a *App) pypiProjects(repo *Repo, depth int) []string {
	set := map[string]bool{}
	if repo.Type == "group" && depth <= 5 {
		for _, m := range a.members(repo) {
			for _, n := range a.pypiProjects(m, depth+1) {
				set[n] = true
			}
		}
	} else {
		rows, err := a.db.Query(`SELECT DISTINCT component FROM assets WHERE repo=? AND component!=''`, repo.Name)
		if err == nil {
			for rows.Next() {
				var n string
				rows.Scan(&n)
				set[n] = true
			}
			rows.Close()
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// pypiUpload implements the legacy upload API used by twine.
func (a *App) pypiUpload(w http.ResponseWriter, r *http.Request, repo *Repo, u *User) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "invalid upload", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll()
	if r.FormValue(":action") != "file_upload" {
		http.Error(w, "unsupported action", http.StatusBadRequest)
		return
	}
	f, fh, err := r.FormFile("content")
	if err != nil {
		http.Error(w, "missing content", http.StatusBadRequest)
		return
	}
	defer f.Close()
	name, version := pypiNormalize(r.FormValue("name")), r.FormValue("version")
	p := "packages/" + name + "/" + path.Base(fh.Filename)
	if name == "" || !validPath(p) {
		http.Error(w, "invalid package name or filename", http.StatusBadRequest)
		return
	}
	if err := a.checkRedeploy(repo, p); err != nil {
		resolveError(w, err)
		return
	}
	if _, err := a.putAsset(repo.Name, p, f, assetMeta{ContentType: "application/octet-stream", Component: name, Version: version, Uploader: u.Username}); err != nil {
		resolveError(w, err)
		return
	}
	a.audit(u, "pypi.upload", repo.Name+"/"+name, version)
	w.WriteHeader(http.StatusOK)
}
