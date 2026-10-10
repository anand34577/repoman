package main

import (
	"bytes"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"
	"unicode"
)

// Maven is path-based and served by rawHandler; this file adds the one thing a group needs beyond
// "first member wins": artifact-level maven-metadata.xml merged across members, so version ranges,
// LATEST/RELEASE and `mvn versions:*` see releases, snapshots and proxied versions together.

type mavenVersioning struct {
	Latest      string    `xml:"latest,omitempty"`
	Release     string    `xml:"release,omitempty"`
	Snapshot    *struct{} `xml:"snapshot"`
	Versions    []string  `xml:"versions>version"`
	LastUpdated string    `xml:"lastUpdated,omitempty"`
}

type mavenMeta struct {
	XMLName      xml.Name         `xml:"metadata"`
	ModelVersion string           `xml:"modelVersion,attr,omitempty"`
	GroupID      string           `xml:"groupId,omitempty"`
	ArtifactID   string           `xml:"artifactId,omitempty"`
	Version      string           `xml:"version,omitempty"` // set only in per-version (snapshot) metadata
	Versioning   *mavenVersioning `xml:"versioning"`
	Plugins      []struct {
		Name       string `xml:"name"`
		Prefix     string `xml:"prefix"`
		ArtifactID string `xml:"artifactId"`
	} `xml:"plugins>plugin"`
}

var mavenChecksums = map[string]func([]byte) string{
	".md5":    func(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) },
	".sha1":   func(b []byte) string { s := sha1.Sum(b); return hex.EncodeToString(s[:]) },
	".sha256": func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) },
	".sha512": func(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) },
}

// mavenGroupMetadata answers a group request for maven-metadata.xml (or its checksum) with the
// merge of all members' copies. It reports false — leaving the normal first-member lookup to
// answer — when the path is something else, fewer than two members have the file, or it is
// snapshot metadata, which describes one build and must not be mixed.
func (a *App) mavenGroupMetadata(w http.ResponseWriter, r *http.Request, repo *Repo, p string) bool {
	metaPath, sum := p, ""
	if ext := path.Ext(p); mavenChecksums[ext] != nil {
		metaPath, sum = strings.TrimSuffix(p, ext), ext
	}
	if path.Base(metaPath) != "maven-metadata.xml" {
		return false
	}
	var docs []mavenMeta
	var newest int64
	for _, m := range a.members(repo) {
		x, err := a.resolve(r.Context(), m, metaPath, r.Header, 1)
		if err != nil {
			continue
		}
		data, err := a.readAsset(x)
		var d mavenMeta
		if err != nil || xml.Unmarshal(data, &d) != nil || d.Version != "" || (d.Versioning != nil && d.Versioning.Snapshot != nil) {
			return false
		}
		docs = append(docs, d)
		newest = max(newest, x.UpdatedAt)
	}
	if len(docs) < 2 {
		return false
	}
	out := docs[0]
	var versions []string
	lastUpdated := ""
	prefixes := map[string]bool{}
	out.Plugins = nil
	for _, d := range docs {
		for _, pl := range d.Plugins {
			if !prefixes[pl.Prefix] {
				prefixes[pl.Prefix] = true
				out.Plugins = append(out.Plugins, pl)
			}
		}
		if d.Versioning == nil {
			continue
		}
		lastUpdated = max(lastUpdated, d.Versioning.LastUpdated)
		for _, v := range d.Versioning.Versions {
			if !slices.Contains(versions, v) {
				versions = append(versions, v)
			}
		}
	}
	if len(versions) > 0 {
		slices.SortStableFunc(versions, mavenCompare)
		v := mavenVersioning{Versions: versions, LastUpdated: lastUpdated, Latest: versions[len(versions)-1]}
		for _, ver := range versions {
			if !strings.HasSuffix(ver, "-SNAPSHOT") {
				v.Release = ver
			}
		}
		out.Versioning = &v
	}
	body, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return false
	}
	body = append([]byte(xml.Header), append(body, '\n')...)
	w.Header().Set("Content-Type", "application/xml")
	if sum != "" {
		body = []byte(mavenChecksums[sum](body))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	}
	http.ServeContent(w, r, "", time.Unix(newest, 0), bytes.NewReader(body))
	return true
}

// mavenQualifiers orders the well-known qualifiers; a plain release ("") sorts after snapshots
// and release candidates but before service packs.
var mavenQualifiers = map[string]int{
	"alpha": 1, "a": 1, "beta": 2, "b": 2, "milestone": 3, "m": 3, "rc": 4, "cr": 4, "snapshot": 5,
	"": 6, "ga": 6, "final": 6, "release": 6, "sp": 7,
}

// mavenTokens splits "1.2.0-RC1" into ["1" "2" "0" "rc" "1"]: on dots, dashes and digit/letter boundaries.
func mavenTokens(v string) []string {
	var out []string
	start := 0
	flush := func(end int) {
		if end > start {
			out = append(out, strings.ToLower(v[start:end]))
		}
	}
	for i, c := range v {
		switch {
		case c == '.' || c == '-' || c == '_':
			flush(i)
			start = i + 1
		case i > start && unicode.IsDigit(c) != unicode.IsDigit(rune(v[i-1])):
			flush(i)
			start = i
		}
	}
	flush(len(v))
	return out
}

func isNumber(s string) bool {
	return s != "" && strings.IndexFunc(s, func(c rune) bool { return c < '0' || c > '9' }) < 0
}

// mavenCompare orders versions the way Maven does for the common cases: numeric segments by value,
// numbers above qualifiers, and alpha < beta < milestone < rc < snapshot < release < sp.
func mavenCompare(a, b string) int {
	x, y := mavenTokens(a), mavenTokens(b)
	for i := 0; i < max(len(x), len(y)); i++ {
		var s, t string
		if i < len(x) {
			s = x[i]
		}
		if i < len(y) {
			t = y[i]
		}
		sn, tn := isNumber(s), isNumber(t)
		var c int
		switch {
		case sn && tn:
			s, t = strings.TrimLeft(s, "0"), strings.TrimLeft(t, "0")
			if c = len(s) - len(t); c == 0 {
				c = strings.Compare(s, t)
			}
		case sn:
			if c = 1; t == "" && strings.Trim(s, "0") == "" {
				c = 0 // 1.0 == 1
			}
		case tn:
			if c = -1; s == "" && strings.Trim(t, "0") == "" {
				c = 0
			}
		default:
			rs, okS := mavenQualifiers[s]
			rt, okT := mavenQualifiers[t]
			if !okS {
				rs = 8
			}
			if !okT {
				rt = 8
			}
			if c = rs - rt; c == 0 && !okS && !okT {
				c = strings.Compare(s, t)
			}
		}
		if c != 0 {
			return c
		}
	}
	return 0
}
