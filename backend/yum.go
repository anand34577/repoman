package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// YUM/DNF (RPM). Hosted packages live anywhere outside repodata/ (default Packages/<letter>/);
// repodata/ (primary, filelists, other, repomd.xml + .asc) is regenerated on every change.

type rpmDep struct {
	Name  string `json:"name"`
	Flags string `json:"flags,omitempty"`
	Epoch string `json:"epoch,omitempty"`
	Ver   string `json:"ver,omitempty"`
	Rel   string `json:"rel,omitempty"`
}

type rpmPkg struct {
	Name          string   `json:"name"`
	Epoch         string   `json:"epoch"`
	Version       string   `json:"version"`
	Release       string   `json:"release"`
	Arch          string   `json:"arch"`
	Summary       string   `json:"summary"`
	Description   string   `json:"description"`
	License       string   `json:"license"`
	Vendor        string   `json:"vendor,omitempty"`
	Group         string   `json:"group,omitempty"`
	URL           string   `json:"url,omitempty"`
	Packager      string   `json:"packager,omitempty"`
	BuildHost     string   `json:"buildhost,omitempty"`
	SourceRPM     string   `json:"sourcerpm,omitempty"`
	BuildTime     int64    `json:"buildtime"`
	InstalledSize int64    `json:"installedSize"`
	ArchiveSize   int64    `json:"archiveSize"`
	HeaderStart   int64    `json:"headerStart"`
	HeaderEnd     int64    `json:"headerEnd"`
	Provides      []rpmDep `json:"provides,omitempty"`
	Requires      []rpmDep `json:"requires,omitempty"`
	Conflicts     []rpmDep `json:"conflicts,omitempty"`
	Obsoletes     []rpmDep `json:"obsoletes,omitempty"`
	Files         []string `json:"files,omitempty"`
	Dirs          []string `json:"dirs,omitempty"`
}

func (a *App) yumHandler(w http.ResponseWriter, r *http.Request, repo *Repo, p string, u *User) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if !validPath(p) {
			http.NotFound(w, r)
			return
		}
		x, err := a.resolve(r.Context(), repo, p, r.Header, 0)
		if err != nil {
			resolveError(w, err)
			return
		}
		a.serveAsset(w, r, x)
	case http.MethodPut, http.MethodPost:
		target := p
		if !strings.Contains(p, "/") {
			target = "" // root upload: path derived from the package header
		}
		if target != "" && (!validPath(target) || !strings.HasSuffix(target, ".rpm") || strings.HasPrefix(target, "repodata/")) {
			http.Error(w, "upload .rpm files to the repository root or a sub-directory (not repodata/)", http.StatusBadRequest)
			return
		}
		if _, err := a.ingestRPM(repo, r.Body, target, u); err != nil {
			resolveError(w, err)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		a.deletePath(w, r, repo, p, u)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) ingestRPM(repo *Repo, body io.Reader, target string, u *User) (*Asset, error) {
	tmp, sha, size, err := a.blobs.Write(body)
	if err != nil {
		return nil, err
	}
	pkg, err := readRPM(tmp)
	if err != nil {
		a.blobs.Discard(tmp)
		return nil, httpErr{400, "not a valid .rpm package: " + err.Error()}
	}
	if target == "" {
		target = fmt.Sprintf("Packages/%s/%s-%s-%s.%s.rpm", strings.ToLower(pkg.Name[:1]), pkg.Name, pkg.Version, pkg.Release, pkg.Arch)
	}
	if err := a.checkRedeploy(repo, target); err != nil {
		a.blobs.Discard(tmp)
		return nil, err
	}
	attrs, _ := json.Marshal(pkg)
	x, err := a.storeTemp(repo.Name, target, tmp, sha, size, assetMeta{
		ContentType: "application/x-rpm", Component: pkg.Name, Version: pkg.Version + "-" + pkg.Release,
		Uploader: u.Username, Attrs: string(attrs),
	})
	if err != nil {
		return nil, err
	}
	a.audit(u, "yum.upload", repo.Name+"/"+target)
	return x, a.yumReindex(repo)
}

// ---------- RPM header parsing ----------

type rpmHeader struct {
	idx   map[int32][3]int32 // tag -> type, offset, count
	store []byte
}

func readRPMHeader(r io.ReaderAt, off int64) (*rpmHeader, int64, error) {
	pre := make([]byte, 16)
	if _, err := r.ReadAt(pre, off); err != nil || !bytes.Equal(pre[:4], []byte{0x8e, 0xad, 0xe8, 0x01}) {
		return nil, 0, errors.New("bad header magic")
	}
	n, hsize := int64(binary.BigEndian.Uint32(pre[8:])), int64(binary.BigEndian.Uint32(pre[12:]))
	if n > 1<<16 || hsize > 64<<20 {
		return nil, 0, errors.New("header too large")
	}
	buf := make([]byte, 16*n+hsize)
	if _, err := r.ReadAt(buf, off+16); err != nil {
		return nil, 0, errors.New("truncated header")
	}
	h := &rpmHeader{idx: map[int32][3]int32{}, store: buf[16*n:]}
	for i := int64(0); i < n; i++ {
		e := buf[i*16:]
		h.idx[int32(binary.BigEndian.Uint32(e))] = [3]int32{int32(binary.BigEndian.Uint32(e[4:])), int32(binary.BigEndian.Uint32(e[8:])), int32(binary.BigEndian.Uint32(e[12:]))}
	}
	return h, 16 + 16*n + hsize, nil
}

func (h *rpmHeader) strs(tag int32) []string {
	e, ok := h.idx[tag]
	if !ok || (e[0] != 6 && e[0] != 8 && e[0] != 9) {
		return nil
	}
	count := int(e[2])
	if e[0] == 6 {
		count = 1
	}
	var out []string
	pos := int(e[1])
	for i := 0; i < count && pos >= 0 && pos < len(h.store); i++ {
		end := bytes.IndexByte(h.store[pos:], 0)
		if end < 0 {
			break
		}
		out = append(out, string(h.store[pos:pos+end]))
		pos += end + 1
	}
	return out
}

func (h *rpmHeader) str(tag int32) string {
	if s := h.strs(tag); len(s) > 0 {
		return s[0] // first locale for i18n strings
	}
	return ""
}

func (h *rpmHeader) ints(tag int32) []int64 {
	e, ok := h.idx[tag]
	if !ok {
		return nil
	}
	size := map[int32]int{3: 2, 4: 4, 5: 8}[e[0]]
	if size == 0 || int(e[1])+size*int(e[2]) > len(h.store) || e[1] < 0 {
		return nil
	}
	out := make([]int64, e[2])
	for i := range out {
		b := h.store[int(e[1])+i*size:]
		switch size {
		case 2:
			out[i] = int64(binary.BigEndian.Uint16(b))
		case 4:
			out[i] = int64(binary.BigEndian.Uint32(b))
		case 8:
			out[i] = int64(binary.BigEndian.Uint64(b))
		}
	}
	return out
}

func (h *rpmHeader) int(tag int32) int64 {
	if v := h.ints(tag); len(v) > 0 {
		return v[0]
	}
	return 0
}

func (h *rpmHeader) deps(nameTag, flagTag, verTag int32, skipRpmlib bool) []rpmDep {
	names, flags, vers := h.strs(nameTag), h.ints(flagTag), h.strs(verTag)
	var out []rpmDep
	seen := map[string]bool{}
	for i, n := range names {
		if skipRpmlib && strings.HasPrefix(n, "rpmlib(") {
			continue
		}
		d := rpmDep{Name: n}
		if i < len(vers) && vers[i] != "" && i < len(flags) {
			d.Flags = map[int64]string{2: "LT", 4: "GT", 8: "EQ", 10: "LE", 12: "GE"}[flags[i]&0xe]
			evr := vers[i]
			d.Epoch = "0"
			if e, rest, ok := strings.Cut(evr, ":"); ok {
				d.Epoch, evr = e, rest
			}
			d.Ver = evr
			if i := strings.LastIndex(evr, "-"); i >= 0 {
				d.Ver, d.Rel = evr[:i], evr[i+1:]
			}
		}
		key := fmt.Sprint(d)
		if !seen[key] {
			seen[key] = true
			out = append(out, d)
		}
	}
	return out
}

// readRPM parses the lead, signature header and main header of an .rpm file.
func readRPM(file string) (*rpmPkg, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	lead := make([]byte, 96)
	if _, err := io.ReadFull(f, lead); err != nil || !bytes.Equal(lead[:4], []byte{0xed, 0xab, 0xee, 0xdb}) {
		return nil, errors.New("missing rpm lead")
	}
	sig, sigSize, err := readRPMHeader(f, 96)
	if err != nil {
		return nil, err
	}
	start := 96 + sigSize + (8-sigSize%8)%8 // signature header is padded to 8 bytes
	h, size, err := readRPMHeader(f, start)
	if err != nil {
		return nil, err
	}
	p := &rpmPkg{
		Name: h.str(1000), Version: h.str(1001), Release: h.str(1002), Epoch: strconv.FormatInt(h.int(1003), 10),
		Summary: h.str(1004), Description: h.str(1005), BuildTime: h.int(1006), BuildHost: h.str(1007),
		Vendor: h.str(1011), License: h.str(1014), Packager: h.str(1015), Group: h.str(1016), URL: h.str(1020),
		Arch: h.str(1022), SourceRPM: h.str(1044), ArchiveSize: h.int(1046),
		InstalledSize: h.int(1009), HeaderStart: start, HeaderEnd: start + size,
		Provides:  h.deps(1047, 1112, 1113, false),
		Requires:  h.deps(1049, 1048, 1050, true),
		Conflicts: h.deps(1054, 1053, 1055, false),
		Obsoletes: h.deps(1090, 1114, 1115, false),
	}
	if p.InstalledSize == 0 {
		p.InstalledSize = h.int(5009) // RPMTAG_LONGSIZE
	}
	if p.ArchiveSize == 0 { // modern rpms keep the payload size in the signature header
		p.ArchiveSize = max(sig.int(1007), sig.int(270))
	}
	if _, ok := h.idx[1106]; ok { // RPMTAG_SOURCEPACKAGE
		p.Arch = "src"
	}
	if p.Name == "" || p.Version == "" || p.Arch == "" {
		return nil, errors.New("header lacks name, version or arch")
	}
	base, dirs, dirIdx, modes := h.strs(1117), h.strs(1118), h.ints(1116), h.ints(1030)
	files := h.strs(1027) // RPMTAG_OLDFILENAMES for ancient packages
	if len(base) > 0 && len(dirIdx) == len(base) {
		files = files[:0]
		for i, b := range base {
			if int(dirIdx[i]) < len(dirs) {
				files = append(files, dirs[dirIdx[i]]+b)
			}
		}
	}
	for i, fp := range files {
		if i < len(modes) && modes[i]&0o170000 == 0o040000 {
			p.Dirs = append(p.Dirs, fp)
		} else {
			p.Files = append(p.Files, fp)
		}
	}
	return p, nil
}

// ---------- repodata generation ----------

func xmlEsc(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

func depsXML(tag string, deps []rpmDep) string {
	if len(deps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<rpm:" + tag + ">")
	for _, d := range deps {
		fmt.Fprintf(&b, `<rpm:entry name="%s"`, xmlEsc(d.Name))
		if d.Flags != "" {
			fmt.Fprintf(&b, ` flags="%s" epoch="%s" ver="%s"`, d.Flags, xmlEsc(d.Epoch), xmlEsc(d.Ver))
			if d.Rel != "" {
				fmt.Fprintf(&b, ` rel="%s"`, xmlEsc(d.Rel))
			}
		}
		b.WriteString("/>")
	}
	b.WriteString("</rpm:" + tag + ">")
	return b.String()
}

func primaryFile(f string) bool {
	return strings.HasPrefix(f, "/etc/") || strings.Contains(f, "bin/") || f == "/usr/lib/sendmail"
}

// yumReindex regenerates repodata/ for a hosted repo and signs repomd.xml.
func (a *App) yumReindex(repo *Repo) error {
	a.indexMu.Lock()
	defer a.indexMu.Unlock()
	rpms, err := a.queryAssets(`WHERE repo=? AND path LIKE '%.rpm' AND substr(path,1,9)!='repodata/' ORDER BY path`, repo.Name)
	if err != nil {
		return err
	}
	var primary, filelists, other strings.Builder
	n := 0
	for _, x := range rpms {
		var p rpmPkg
		if json.Unmarshal(x.Attrs, &p) != nil || p.Name == "" {
			continue
		}
		n++
		ver := fmt.Sprintf(`<version epoch="%s" ver="%s" rel="%s"/>`, xmlEsc(p.Epoch), xmlEsc(p.Version), xmlEsc(p.Release))
		fmt.Fprintf(&primary, `<package type="rpm"><name>%s</name><arch>%s</arch>%s<checksum type="sha256" pkgid="YES">%s</checksum>`+
			`<summary>%s</summary><description>%s</description><packager>%s</packager><url>%s</url>`+
			`<time file="%d" build="%d"/><size package="%d" installed="%d" archive="%d"/><location href="%s"/>`+
			`<format><rpm:license>%s</rpm:license><rpm:vendor>%s</rpm:vendor><rpm:group>%s</rpm:group><rpm:buildhost>%s</rpm:buildhost>`+
			`<rpm:sourcerpm>%s</rpm:sourcerpm><rpm:header-range start="%d" end="%d"/>%s%s%s%s`,
			xmlEsc(p.Name), xmlEsc(p.Arch), ver, x.Blob, xmlEsc(p.Summary), xmlEsc(p.Description), xmlEsc(p.Packager), xmlEsc(p.URL),
			x.CreatedAt, p.BuildTime, x.Size, p.InstalledSize, p.ArchiveSize, xmlEsc(x.Path),
			xmlEsc(p.License), xmlEsc(p.Vendor), xmlEsc(p.Group), xmlEsc(p.BuildHost), xmlEsc(p.SourceRPM), p.HeaderStart, p.HeaderEnd,
			depsXML("provides", p.Provides), depsXML("requires", p.Requires), depsXML("conflicts", p.Conflicts), depsXML("obsoletes", p.Obsoletes))
		for _, f := range p.Files {
			if primaryFile(f) {
				fmt.Fprintf(&primary, "<file>%s</file>", xmlEsc(f))
			}
		}
		primary.WriteString("</format></package>\n")

		fmt.Fprintf(&filelists, `<package pkgid="%s" name="%s" arch="%s">%s`, x.Blob, xmlEsc(p.Name), xmlEsc(p.Arch), ver)
		for _, f := range p.Files {
			fmt.Fprintf(&filelists, "<file>%s</file>", xmlEsc(f))
		}
		for _, d := range p.Dirs {
			fmt.Fprintf(&filelists, `<file type="dir">%s</file>`, xmlEsc(d))
		}
		filelists.WriteString("</package>\n")
		fmt.Fprintf(&other, `<package pkgid="%s" name="%s" arch="%s">%s</package>`+"\n", x.Blob, xmlEsc(p.Name), xmlEsc(p.Arch), ver)
	}
	head := `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
	docs := []struct{ typ, body string }{
		{"primary", fmt.Sprintf(`%s<metadata xmlns="http://linux.duke.edu/metadata/common" xmlns:rpm="http://linux.duke.edu/metadata/rpm" packages="%d">`+"\n%s</metadata>\n", head, n, primary.String())},
		{"filelists", fmt.Sprintf(`%s<filelists xmlns="http://linux.duke.edu/metadata/filelists" packages="%d">`+"\n%s</filelists>\n", head, n, filelists.String())},
		{"other", fmt.Sprintf(`%s<otherdata xmlns="http://linux.duke.edu/metadata/other" packages="%d">`+"\n%s</otherdata>\n", head, n, other.String())},
	}
	now := time.Now().Unix()
	var repomd strings.Builder
	fmt.Fprintf(&repomd, `%s<repomd xmlns="http://linux.duke.edu/metadata/repo" xmlns:rpm="http://linux.duke.edu/metadata/rpm">`+"\n<revision>%d</revision>\n", head, now)
	keep := []any{"repodata/repomd.xml", "repodata/repomd.xml.asc"}
	for _, d := range docs {
		var gz bytes.Buffer
		zw := gzip.NewWriter(&gz)
		zw.Write([]byte(d.body))
		zw.Close()
		openSum, gzSum := sha256.Sum256([]byte(d.body)), sha256.Sum256(gz.Bytes())
		loc := fmt.Sprintf("repodata/%s-%s.xml.gz", hex.EncodeToString(gzSum[:]), d.typ)
		if _, err := a.putAsset(repo.Name, loc, bytes.NewReader(gz.Bytes()), assetMeta{ContentType: "application/gzip", Uploader: "system"}); err != nil {
			return err
		}
		keep = append(keep, loc)
		fmt.Fprintf(&repomd, `<data type="%s"><checksum type="sha256">%x</checksum><open-checksum type="sha256">%x</open-checksum>`+
			`<location href="%s"/><timestamp>%d</timestamp><size>%d</size><open-size>%d</open-size></data>`+"\n",
			d.typ, gzSum, openSum, loc, now, gz.Len(), len(d.body))
	}
	repomd.WriteString("</repomd>\n")
	sig, err := a.detachSign([]byte(repomd.String()))
	if err != nil {
		return err
	}
	if _, err := a.putAsset(repo.Name, "repodata/repomd.xml", strings.NewReader(repomd.String()), assetMeta{ContentType: "application/xml", Uploader: "system"}); err != nil {
		return err
	}
	if _, err := a.putAsset(repo.Name, "repodata/repomd.xml.asc", bytes.NewReader(sig), assetMeta{ContentType: "application/pgp-signature", Uploader: "system"}); err != nil {
		return err
	}
	// keep the previous generation for an hour so clients mid-refresh don't hit 404s
	args := append([]any{repo.Name, now - 3600}, keep...)
	_, err = a.deleteAssets(`repo=? AND substr(path,1,9)='repodata/' AND updated_at < ? AND path NOT IN (?`+strings.Repeat(",?", len(keep)-1)+`)`, args...)
	return err
}
