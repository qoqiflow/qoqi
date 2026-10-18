// The site of qoqi.dev: the landing page, and the documentation made from
// the Markdown of docs/, as it is in the repository. From website/:
//
//	go run .                  # into dist/
//	go run . -serve :8080     # built again at each page asked for
//
// Links between the pages of docs/ become links between the pages of the
// site; a link to anything else of the repository goes to it on GitHub. A
// link to a page or a heading that does not exist stops the build.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

const (
	site   = "https://qoqi.dev"
	github = "https://github.com/qoqiflow/qoqi"
)

func main() {
	root := flag.String("root", "..", "the repository")
	out := flag.String("out", "dist", "where the site is written")
	serve := flag.String("serve", "", "serve the site at this address, built again at each page")
	flag.Parse()

	if *serve != "" {
		log.Printf("http://%s", strings.TrimPrefix(*serve, ":"))
		log.Fatal(http.ListenAndServe(*serve, devServer(*root, *out)))
	}
	start := time.Now()
	n, err := build(*root, *out)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%d pages in %s, in %s", n, *out, time.Since(start).Round(time.Millisecond))
}

// A page of the documentation.
type page struct {
	Src     string // docs/concepts/flows.md
	URL     string // /docs/concepts/flows
	Title   string
	Body    template.HTML
	TOC     []heading
	Section string // of the navigation
	Prev    *page
	Next    *page
	Mermaid bool
	Date    time.Time // of a post of the blog

	doc    ast.Node
	source []byte
	ids    map[string]bool
}

type heading struct {
	Level int
	ID    string
	Text  string
}

// The navigation, as docs/README.md lists it.
type navSection struct {
	Title string
	Items []navItem
}

type navItem struct {
	Title string
	URL   string // empty for a label
	Depth int
}

type searchEntry struct {
	Page    string `json:"p"`
	Heading string `json:"h,omitempty"`
	URL     string `json:"u"`
	Text    string `json:"t"`
}

func markdown() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			highlighting.NewHighlighting(
				highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
				highlighting.WithWrapperRenderer(wrapCode),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
}

// Each block of code is in a frame that says its language, with a button
// that copies it (site.js).
func wrapCode(w util.BufWriter, ctx highlighting.CodeBlockContext, entering bool) {
	lang, _ := ctx.Language()
	if entering {
		label := string(lang)
		switch label {
		case "qoqi", "qi", "rust":
			label = "qoqi"
		case "console", "bash", "sh":
			label = "shell"
		}
		fmt.Fprintf(w, `<div class="code" data-lang="%s">`, template.HTMLEscapeString(label))
		if ctx.Highlighted() {
			return
		}
		w.WriteString(`<pre class="chroma"><code>`)
		return
	}
	if !ctx.Highlighted() {
		w.WriteString(`</code></pre>`)
	}
	w.WriteString(`</div>`)
}

var (
	fenceRust    = regexp.MustCompile("(?m)^(\\s*)```rust\\s*$")
	fenceMermaid = regexp.MustCompile("(?ms)^```mermaid\\s*\n(.*?)^```\\s*$")
)

// The documentation writes qoqi in blocks marked rust, for GitHub to colour
// them; here they are coloured as qoqi.
func prepare(source []byte) ([]byte, bool) {
	source = fenceRust.ReplaceAll(source, []byte("$1```qoqi"))
	mermaid := false
	source = fenceMermaid.ReplaceAllFunc(source, func(m []byte) []byte {
		mermaid = true
		body := fenceMermaid.FindSubmatch(m)[1]
		return []byte(`<pre class="mermaid">` + template.HTMLEscapeString(string(body)) + "</pre>\n")
	})
	return source, mermaid
}

func build(root, out string) (int, error) {
	md := markdown()

	// Every page first, so that links between them can be checked
	var pages []*page
	byPath := map[string]*page{}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		source, mermaid := prepare(raw)
		pg := &page{Src: rel, URL: urlOf(rel), source: source, Mermaid: mermaid, ids: map[string]bool{}}
		pg.doc = md.Parser().Parse(text.NewReader(source), parser.WithContext(parser.NewContext(parser.WithIDs(newIDs()))))
		pg.Title, pg.TOC = headings(pg)
		pages = append(pages, pg)
		byPath[rel] = pg
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Src < pages[j].Src })

	nav, err := navigation(root, md, byPath)
	if err != nil {
		return 0, err
	}
	order(pages, nav, byPath)

	// The links, made to point within the site, and checked
	var broken []string
	for _, pg := range pages {
		broken = append(broken, relink(root, pg, byPath)...)
	}
	if len(broken) > 0 {
		return 0, fmt.Errorf("links that lead nowhere:\n  %s", strings.Join(broken, "\n  "))
	}

	tmpl, err := templates()
	if err != nil {
		return 0, err
	}
	if err := os.RemoveAll(out); err != nil {
		return 0, err
	}

	var index []searchEntry
	for _, pg := range pages {
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, pg.source, pg.doc); err != nil {
			return 0, fmt.Errorf("%s: %w", pg.Src, err)
		}
		pg.Body = template.HTML(buf.String())
		index = append(index, searchEntries(pg)...)
		if err := render(tmpl, "doc.html", filepath.Join(out, fileOf(pg.URL)), map[string]any{
			"Page": pg, "Nav": nav, "Title": pg.Title + " · Qoqi Flow", "Description": describe(pg), "URL": site + pg.URL,
			"Edit": github + "/edit/main/" + pg.Src,
		}); err != nil {
			return 0, err
		}
	}

	if others, err := comparisons(); err == nil {
		hasCompare = len(others) > 0
	}
	posts, err := blog(md, byPath)
	if err != nil {
		return 0, err
	}
	for _, pg := range posts {
		if err := render(tmpl, "post.html", filepath.Join(out, fileOf(pg.URL)), map[string]any{
			"Page": pg, "Title": pg.Title + " · Qoqi Flow", "Description": describe(pg), "URL": site + pg.URL,
		}); err != nil {
			return 0, err
		}
	}
	if len(posts) > 0 {
		if err := render(tmpl, "blog.html", filepath.Join(out, "blog", "index.html"), map[string]any{
			"Posts": posts, "Title": "Blog · Qoqi Flow", "Description": "Writing about Qoqi Flow: how it works, and why.", "URL": site + "/blog/",
		}); err != nil {
			return 0, err
		}
	}

	landing, err := landingData(root)
	if err != nil {
		return 0, err
	}
	landing["URL"] = site + "/"
	if err := render(tmpl, "landing.html", filepath.Join(out, "index.html"), landing); err != nil {
		return 0, err
	}
	others, err := comparisons()
	if err != nil {
		return 0, err
	}
	hasCompare = len(others) > 0
	if hasCompare {
		for _, v := range others {
			if err := render(tmpl, "vs.html", filepath.Join(out, "vs", v.ID+".html"), map[string]any{
				"Title": v.Title + " · Qoqi Flow", "Description": v.Lede, "URL": site + "/vs/" + v.ID, "V": v, "Others": others,
			}); err != nil {
				return 0, err
			}
		}
		hub, err := comparisonHub()
		if err != nil {
			return 0, err
		}
		if err := render(tmpl, "vs-index.html", filepath.Join(out, "vs", "index.html"), map[string]any{
			"Title": hub.Title + " · Qoqi Flow", "Description": hub.Lede, "URL": site + "/vs/", "H": hub, "Others": others,
		}); err != nil {
			return 0, err
		}
	}

	if err := render(tmpl, "404.html", filepath.Join(out, "404.html"), map[string]any{"Title": "Not found · Qoqi Flow", "Nav": nav}); err != nil {
		return 0, err
	}

	if err := writeJSON(filepath.Join(out, "search.json"), index); err != nil {
		return 0, err
	}
	if err := sitemap(filepath.Join(out, "sitemap.xml"), append(pages, posts...), others); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(out, "robots.txt"), []byte("User-agent: *\nAllow: /\nSitemap: "+site+"/sitemap.xml\n"), 0o644); err != nil {
		return 0, err
	}

	// What is served as it is
	copies := map[string]string{
		"install.sh":                                    "install.sh",
		"llms.txt":                                      "llms.txt",
		"assets/qoqi.svg":                               "assets/qoqi.svg",
		"assets/qoqi-wordmark.svg":                      "assets/qoqi-wordmark.svg",
		"assets/qoqi-wordmark-white.svg":                "assets/qoqi-wordmark-white.svg",
		"assets/qoqi-128.png":                           "assets/qoqi-128.png",
		"assets/qoqi-512.png":                           "assets/qoqi-512.png",
		"website/static/site.css":                       "assets/site.css",
		"website/static/site.js":                        "assets/site.js",
		"website/static/fonts/Geist-Variable.woff2":     "assets/fonts/Geist-Variable.woff2",
		"website/static/fonts/GeistMono-Variable.woff2": "assets/fonts/GeistMono-Variable.woff2",
		"website/static/fonts/OFL.txt":                  "assets/fonts/OFL.txt",
	}
	for _, media := range []string{"console.mp4", "console.jpg"} {
		if _, err := os.Stat(filepath.Join(root, "website/static", media)); err == nil {
			copies["website/static/"+media] = "assets/" + media
		}
	}
	for from, to := range copies {
		if err := copyFile(filepath.Join(root, from), filepath.Join(out, to)); err != nil {
			return 0, err
		}
	}
	return len(pages) + 1, nil
}

// docs/README.md is /docs/, docs/compare/README.md /docs/compare/,
// docs/concepts/flows.md /docs/concepts/flows.
func urlOf(src string) string {
	p := strings.TrimSuffix(src, ".md")
	if path.Base(p) == "README" {
		return "/" + path.Dir(p) + "/"
	}
	return "/" + p
}

func fileOf(url string) string {
	if strings.HasSuffix(url, "/") {
		return strings.TrimPrefix(url, "/") + "index.html"
	}
	return strings.TrimPrefix(url, "/") + ".html"
}

// The title, the first heading; and the headings a page lists at its side.
func headings(pg *page) (string, []heading) {
	title := ""
	var toc []heading
	ast.Walk(pg.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		h, ok := n.(*ast.Heading)
		if !ok || !entering {
			return ast.WalkContinue, nil
		}
		id, _ := h.AttributeString("id")
		ids, _ := id.([]byte)
		pg.ids[string(ids)] = true
		t := plain(h, pg.source)
		if h.Level == 1 && title == "" {
			title = t
		} else if h.Level == 2 || h.Level == 3 {
			toc = append(toc, heading{Level: h.Level, ID: string(ids), Text: t})
		}
		return ast.WalkSkipChildren, nil
	})
	if title == "" {
		title = strings.TrimSuffix(path.Base(pg.Src), ".md")
	}
	return title, toc
}

// The text of a node, without its marks.
func plain(n ast.Node, source []byte) string {
	var b strings.Builder
	ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch c := c.(type) {
		case *ast.Text:
			b.Write(c.Segment.Value(source))
			if c.SoftLineBreak() || c.HardLineBreak() {
				b.WriteByte(' ')
			}
		case *ast.String:
			b.Write(c.Value)
		case *ast.CodeBlock, *ast.FencedCodeBlock, *ast.HTMLBlock:
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return strings.TrimSpace(b.String())
}

// The sections of docs/README.md, and the links of each.
func navigation(root string, md goldmark.Markdown, byPath map[string]*page) ([]navSection, error) {
	readme := byPath["docs/README.md"]
	if readme == nil {
		return nil, errors.New("docs/README.md is missing")
	}
	var nav []navSection
	for n := readme.doc.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level == 2 {
			nav = append(nav, navSection{Title: plain(h, readme.source)})
			continue
		}
		list, ok := n.(*ast.List)
		if !ok || len(nav) == 0 {
			continue
		}
		sec := &nav[len(nav)-1]
		var items func(l *ast.List, depth int)
		items = func(l *ast.List, depth int) {
			for li := l.FirstChild(); li != nil; li = li.NextSibling() {
				for c := li.FirstChild(); c != nil; c = c.NextSibling() {
					if sub, ok := c.(*ast.List); ok {
						items(sub, depth+1)
						continue
					}
					found := false
					for x := c.FirstChild(); x != nil; x = x.NextSibling() {
						if link, ok := x.(*ast.Link); ok {
							target := path.Clean(path.Join("docs", string(link.Destination)))
							pg := byPath[target]
							if pg == nil {
								continue
							}
							sec.Items = append(sec.Items, navItem{Title: plain(link, readme.source), URL: pg.URL, Depth: depth})
							pg.Section = sec.Title
							found = true
						}
					}
					if !found {
						sec.Items = append(sec.Items, navItem{Title: strings.TrimSuffix(plain(c, readme.source), ":"), Depth: depth})
					}
				}
			}
		}
		items(list, 0)
	}

	// The pages it does not list are under the one that links to them, as
	// the stages of the compiler under its pipeline: kept at the end of
	// their section, by their folder
	listed := map[string]bool{"/docs/": true}
	for _, s := range nav {
		for _, it := range s.Items {
			listed[it.URL] = true
		}
	}
	var rest []*page
	for _, pg := range byPath {
		if !listed[pg.URL] {
			rest = append(rest, pg)
		}
	}
	// In the order the pages of the navigation link to them
	cited := map[string]int{}
	for _, s := range nav {
		for _, it := range s.Items {
			for _, pg := range byPath {
				if pg.URL != it.URL || it.URL == "" {
					continue
				}
				ast.Walk(pg.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
					if l, ok := n.(*ast.Link); ok && entering {
						target, _, _ := strings.Cut(string(l.Destination), "#")
						f := path.Clean(path.Join(path.Dir(pg.Src), target))
						if _, seen := cited[f]; !seen {
							cited[f] = len(cited)
						}
					}
					return ast.WalkContinue, nil
				})
			}
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		a, okA := cited[rest[i].Src]
		b, okB := cited[rest[j].Src]
		if okA != okB {
			return okA
		}
		if a != b {
			return a < b
		}
		return rest[i].Src < rest[j].Src
	})
	added := map[string]bool{}
	for _, pg := range rest {
		dir := path.Dir(pg.Src)
		for si := range nav {
			s := &nav[si]
			at := -1
			for i, it := range s.Items {
				if it.URL != "" && path.Dir(strings.TrimPrefix(it.URL, "/")+".md") == dir {
					at = i
				}
			}
			if at < 0 {
				continue
			}
			depth := s.Items[at].Depth
			if !added[s.Items[at].URL] {
				depth++
			}
			item := navItem{Title: pg.Title, URL: pg.URL, Depth: depth}
			s.Items = append(s.Items[:at+1], append([]navItem{item}, s.Items[at+1:]...)...)
			added[pg.URL] = true
			pg.Section = s.Title
			break
		}
	}
	return nav, nil
}

// The pages in the order the navigation gives them, each with the one
// before and the one after it.
func order(pages []*page, nav []navSection, byPath map[string]*page) {
	byURL := map[string]*page{}
	for _, pg := range pages {
		byURL[pg.URL] = pg
	}
	var seq []*page
	if r := byPath["docs/README.md"]; r != nil {
		seq = append(seq, r)
	}
	for _, s := range nav {
		for _, it := range s.Items {
			if pg := byURL[it.URL]; pg != nil {
				seq = append(seq, pg)
			}
		}
	}
	for i, pg := range seq {
		if i > 0 {
			pg.Prev = seq[i-1]
		}
		if i+1 < len(seq) {
			pg.Next = seq[i+1]
		}
	}
}

// Points the links of a page within the site, or at GitHub, and gives
// the ones that lead nowhere.
func relink(root string, pg *page, byPath map[string]*page) []string {
	var broken []string
	ast.Walk(pg.doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		var dest *[]byte
		switch l := n.(type) {
		case *ast.Link:
			dest = &l.Destination
		case *ast.Image:
			dest = &l.Destination
		default:
			return ast.WalkContinue, nil
		}
		d := string(*dest)
		if d == "" || strings.Contains(d, "://") || strings.HasPrefix(d, "mailto:") || strings.HasPrefix(d, "/") {
			return ast.WalkContinue, nil
		}
		target, fragment, _ := strings.Cut(d, "#")
		if target == "" {
			if fragment != "" && !pg.ids[fragment] {
				broken = append(broken, fmt.Sprintf("%s: #%s", pg.Src, fragment))
			}
			return ast.WalkContinue, nil
		}
		file := path.Clean(path.Join(path.Dir(pg.Src), target))
		suffix := ""
		if fragment != "" {
			suffix = "#" + fragment
		}
		if to := byPath[file]; to != nil {
			if fragment != "" && !to.ids[fragment] {
				broken = append(broken, fmt.Sprintf("%s: %s", pg.Src, d))
			}
			*dest = []byte(to.URL + suffix)
			return ast.WalkContinue, nil
		}
		if file == "llms.txt" {
			*dest = []byte("/llms.txt")
			return ast.WalkContinue, nil
		}
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(file)))
		switch {
		case err != nil || strings.HasPrefix(file, "../"):
			broken = append(broken, fmt.Sprintf("%s: %s", pg.Src, d))
		case info.IsDir():
			*dest = []byte(github + "/tree/main/" + file)
		default:
			*dest = []byte(github + "/blob/main/" + file + suffix)
		}
		return ast.WalkContinue, nil
	})
	return broken
}

// What a search finds: each part of a page under its heading.
func searchEntries(pg *page) []searchEntry {
	var entries []searchEntry
	cur := searchEntry{Page: pg.Title, URL: pg.URL}
	var b strings.Builder
	flush := func() {
		cur.Text = squeeze(b.String(), 600)
		if cur.Text != "" || cur.Heading != "" {
			entries = append(entries, cur)
		}
		b.Reset()
	}
	for n := pg.doc.FirstChild(); n != nil; n = n.NextSibling() {
		if h, ok := n.(*ast.Heading); ok && h.Level >= 2 {
			flush()
			id, _ := h.AttributeString("id")
			ids, _ := id.([]byte)
			cur = searchEntry{Page: pg.Title, Heading: plain(h, pg.source), URL: pg.URL + "#" + string(ids)}
			continue
		}
		if _, ok := n.(*ast.Heading); ok {
			continue
		}
		b.WriteString(plain(n, pg.source))
		b.WriteByte(' ')
	}
	flush()
	return entries
}

func squeeze(s string, max int) string {
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	if len(s) > max {
		s = s[:max]
		if i := strings.LastIndexByte(s, ' '); i > 0 {
			s = s[:i]
		}
	}
	return s
}

// The first paragraph of a page, for its description.
func describe(pg *page) string {
	for n := pg.doc.FirstChild(); n != nil; n = n.NextSibling() {
		if _, ok := n.(*ast.Paragraph); ok {
			return squeeze(plain(n, pg.source), 200)
		}
	}
	return "Qoqi Flow: durable workflows as plain programs."
}

// Whether the site has its comparisons, website/vs/: linked from the top
// of each page, and from the landing page.
var hasCompare bool

func templates() (*template.Template, error) {
	return template.New("").Funcs(template.FuncMap{
		"year":    func() int { return time.Now().Year() },
		"compare": func() bool { return hasCompare },
		"dict": func(kv ...any) map[string]any {
			m := map[string]any{}
			for i := 0; i+1 < len(kv); i += 2 {
				m[kv[i].(string)] = kv[i+1]
			}
			return m
		},
	}).ParseGlob("templates/*.html")
}

func render(t *template.Template, name, file string, data any) error {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, buf.Bytes(), 0o644)
}

// The landing page: its examples are files of landing/, coloured as the
// documentation is. The flows it shows running are landing/flows/*.qi,
// whole programs (qoqi check passes on them) of which the page shows what
// is above the line "// ----", and landing/runs.json says what happens
// to each as it runs.
type run struct {
	ID       string  `json:"id"`
	Label    string  `json:"label"`
	File     string  `json:"file"`
	Instance string  `json:"instance"`
	Note     string  `json:"note"`
	Events   []event `json:"events"`
	Code     template.HTML
}

type event struct {
	Line  int    `json:"line"`
	Kind  string `json:"kind"` // ok, ice, crash, deploy, done
	What  string `json:"what"`
	Code  string `json:"code"`
	Res   string `json:"res"`
	Clock string `json:"clock"`
}

func landingData(root string) (map[string]any, error) {
	code := map[string]template.HTML{}
	files, err := filepath.Glob("landing/*.*")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		lang := ""
		switch filepath.Ext(f) {
		case ".qi":
			lang = "qoqi"
		case ".console":
			lang = "console"
		default:
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		h, err := highlight(lang, string(raw))
		if err != nil {
			return nil, err
		}
		code[filepath.Base(f)] = h
	}

	raw, err := os.ReadFile("landing/runs.json")
	if err != nil {
		return nil, err
	}
	var runs []run
	if err := json.Unmarshal(raw, &runs); err != nil {
		return nil, fmt.Errorf("landing/runs.json: %w", err)
	}
	for i := range runs {
		r := &runs[i]
		src, err := os.ReadFile(filepath.Join("landing/flows", r.ID+".qi"))
		if err != nil {
			return nil, err
		}
		shown, _, _ := strings.Cut(string(src), "\n// ----")
		lines := strings.Count(strings.TrimRight(shown, "\n"), "\n") + 1
		for _, e := range r.Events {
			if e.Line < 1 || e.Line > lines {
				return nil, fmt.Errorf("landing/runs.json: %s: line %d is not shown", r.ID, e.Line)
			}
		}
		if r.Code, err = highlight("qoqi", shown); err != nil {
			return nil, err
		}
	}

	// The video of the console, when there is one: website/static/console.mp4
	// (see website/demo/record.sh), and its first frame as a still
	video := ""
	if _, err := os.Stat("static/console.mp4"); err == nil {
		video = "/assets/console.mp4"
	}

	return map[string]any{
		"Video":       video,
		"Title":       "Qoqi Flow: durable workflows as plain programs",
		"Description": "Write the process from top to bottom: wait three days, ask a manager, charge a card, retry, give up. Qoqi Flow keeps it alive through crashes, restarts and new versions of its own code.",
		"Code":        code,
		"Runs":        runs,
	}, nil
}

// Each line in a span of its own, so that the page can mark one.
func highlight(lang, source string) (template.HTML, error) {
	lexer := lexers.Get(lang)
	it, err := lexer.Tokenise(nil, strings.TrimRight(source, "\n"))
	if err != nil {
		return "", err
	}
	f := chromahtml.New(chromahtml.WithClasses(true), chromahtml.PreventSurroundingPre(true))
	var buf bytes.Buffer
	for _, line := range chroma.SplitTokensIntoLines(it.Tokens()) {
		if n := len(line); n > 0 {
			line[n-1].Value = strings.TrimSuffix(line[n-1].Value, "\n")
		}
		buf.WriteString(`<span class="line">`)
		if err := f.Format(&buf, styles.Fallback, chroma.Literator(line...)); err != nil {
			return "", err
		}
		buf.WriteString("</span>")
	}
	return template.HTML(buf.String()), nil
}

// The pages that set Qoqi Flow beside another tool, /vs/temporal and the
// others: each is website/vs/<id>.json, and the code it shows side by side
// is in files of website/vs/<id>/, coloured as the documentation is.
type versus struct {
	ID      string   `json:"id"`
	Order   int      `json:"order"`
	Name    string   `json:"name"`
	Title   string   `json:"title"`
	Tagline string   `json:"tagline"`
	Lede    string   `json:"lede"`
	Cards   []vsCard `json:"cards"`
	Code    []vsCode `json:"code"`
	Rows    []vsRow  `json:"rows"`
	FAQ     []vsQA   `json:"faq"`
}

type vsCard struct {
	Title string `json:"title"`
	Text  string `json:"text"`
}

type vsCode struct {
	Title    string `json:"title"`
	Text     string `json:"text"`
	ThemFile string `json:"them"`
	UsFile   string `json:"us"`
	Them     template.HTML
	Us       template.HTML
	ThemName string
	UsName   string
	ThemLen  int
	UsLen    int
}

type vsRow struct {
	Label string `json:"label"`
	Them  string `json:"them"`
	Us    string `json:"us"`
}

type vsQA struct {
	Q string `json:"q"`
	A string `json:"a"`
}

// The page that lists them: a table of all, and a card for each.
type vsHub struct {
	Title   string   `json:"title"`
	Tagline string   `json:"tagline"`
	Lede    string   `json:"lede"`
	Columns []string `json:"columns"`
	Rows    []struct {
		Label  string   `json:"label"`
		Us     string   `json:"us"`
		Others []string `json:"others"`
	} `json:"rows"`
}

func comparisons() ([]versus, error) {
	files, err := filepath.Glob("vs/*.json")
	if err != nil {
		return nil, err
	}
	var all []versus
	for _, f := range files {
		if filepath.Base(f) == "index.json" {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var v versus
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		for i := range v.Code {
			c := &v.Code[i]
			for _, side := range []struct {
				file string
				html *template.HTML
				name *string
				n    *int
			}{{c.ThemFile, &c.Them, &c.ThemName, &c.ThemLen}, {c.UsFile, &c.Us, &c.UsName, &c.UsLen}} {
				src, err := os.ReadFile(filepath.Join("vs", side.file))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", f, err)
				}
				lang := map[string]string{".golang": "go", ".py": "python", ".ts": "typescript", ".qi": "qoqi", ".console": "console"}[filepath.Ext(side.file)]
				if *side.html, err = highlight(lang, string(src)); err != nil {
					return nil, err
				}
				*side.name = filepath.Base(side.file)
				*side.n = strings.Count(strings.TrimRight(string(src), "\n"), "\n") + 1
			}
		}
		all = append(all, v)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Order < all[j].Order })
	return all, nil
}

func comparisonHub() (vsHub, error) {
	var h vsHub
	raw, err := os.ReadFile("vs/index.json")
	if err != nil {
		return h, err
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return h, fmt.Errorf("vs/index.json: %w", err)
	}
	return h, nil
}

// The blog: website/blog/2026-10-18-a-title.md is /blog/a-title, dated
// by its name, newest first.
func blog(md goldmark.Markdown, byPath map[string]*page) ([]*page, error) {
	files, err := filepath.Glob("blog/*.md")
	if err != nil {
		return nil, err
	}
	var posts []*page
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		name := strings.TrimSuffix(filepath.Base(f), ".md")
		if len(name) < 12 || name[10] != '-' {
			return nil, fmt.Errorf("%s: a post is named date-slug.md", f)
		}
		date, err := time.Parse("2006-01-02", name[:10])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		source, mermaid := prepare(raw)
		pg := &page{Src: "website/" + filepath.ToSlash(f), URL: "/blog/" + name[11:], source: source, Mermaid: mermaid, ids: map[string]bool{}, Date: date}
		pg.doc = md.Parser().Parse(text.NewReader(source), parser.WithContext(parser.NewContext(parser.WithIDs(newIDs()))))
		pg.Title, pg.TOC = headings(pg)
		if broken := relink("..", pg, byPath); len(broken) > 0 {
			return nil, fmt.Errorf("links that lead nowhere:\n  %s", strings.Join(broken, "\n  "))
		}
		var buf bytes.Buffer
		if err := md.Renderer().Render(&buf, pg.source, pg.doc); err != nil {
			return nil, err
		}
		pg.Body = template.HTML(buf.String())
		posts = append(posts, pg)
	}
	sort.Slice(posts, func(i, j int) bool { return posts[i].Date.After(posts[j].Date) })
	return posts, nil
}

func writeJSON(file string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o644)
}

func sitemap(file string, pages []*page, others []versus) error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	b.WriteString("  <url><loc>" + site + "/</loc></url>\n")
	for _, pg := range pages {
		b.WriteString("  <url><loc>" + site + pg.URL + "</loc></url>\n")
	}
	if len(others) > 0 {
		b.WriteString("  <url><loc>" + site + "/vs/</loc></url>\n")
	}
	for _, v := range others {
		b.WriteString("  <url><loc>" + site + "/vs/" + v.ID + "</loc></url>\n")
	}
	b.WriteString("</urlset>\n")
	return os.WriteFile(file, []byte(b.String()), 0o644)
}

func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o644)
}

// While the site is written: built again for each page asked for, so that
// a change to the documentation or the templates shows at once.
func devServer(root, out string) http.Handler {
	files := http.FileServer(http.Dir(out))
	var mu sync.Mutex // a build at a time, and no file served while one runs
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		p := r.URL.Path
		if !strings.HasPrefix(p, "/assets/") && !strings.HasSuffix(p, ".json") {
			if _, err := build(root, out); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else if strings.HasPrefix(p, "/assets/site.") {
			copyFile(filepath.Join(root, "website/static", path.Base(p)), filepath.Join(out, p))
		}
		// As Caddy serves it: /docs/concepts/flows is flows.html
		if !strings.HasSuffix(p, "/") && path.Ext(p) == "" {
			if _, err := os.Stat(filepath.Join(out, p+".html")); err == nil {
				r.URL.Path = p + ".html"
			}
		}
		if _, err := os.Stat(filepath.Join(out, r.URL.Path)); err != nil && !strings.HasSuffix(r.URL.Path, "/") {
			b, _ := os.ReadFile(filepath.Join(out, "404.html"))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			w.Write(b)
			return
		}
		files.ServeHTTP(w, r)
	})
}
