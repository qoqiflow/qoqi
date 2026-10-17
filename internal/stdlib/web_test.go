package stdlib_test

import "testing"

// Addresses taken apart, changed, resolved and written out again.
func TestUrl(t *testing.T) {
	expect(t, "import std.url;", `
		let page = url.parse("https://ada@shop.example:8443/a%20b/search?q=red+shoes&page=2&tag=x&tag=y#top").expect("an address");
		println(page.scheme, page.user, page.host, page.port, page.path, page.fragment);
		println(page.query, page.param("q"), page.params("tag"), page.param("none"), page.params("none"));
		println(page.withParam("page", "3").withoutParam("tag"), page.withParam("sort", "new").queryText());
		println(page.withPath("/a/b c"), page.origin(), page.isAbsolute());

		println(page.resolve("../help?x=1"), page.resolve("//cdn.example/app.js"), page.resolve("#end"));
		let relative = url.parse("/docs/start?lang=fr").expect("a relative address");
		println(relative.scheme == "", relative.host, relative.path, relative.param("lang"), relative.isAbsolute(), relative);

		let made = url.Url { scheme: "https", host: "api.example", path: "/v1/user list", query: [("name", "Zoë & co"), ("n", "1")] };
		println(made, "${made}", url.parse(made.toString()).expect("its own text").param("name"));

		println(url.queryOf("a=1&b=x+y&&c=%26&d"), url.queryOf(""));
		for bad in ["http://[::1", "http://host:99x/", " ht tp://x"] {
			match url.parse(bad) {
				case Ok(u): { println("accepted", u); }
				case Err(e): { println(e.describe()); }
			}
		}
		`,
		"https ada shop.example 8443 /a b/search top",
		`[("q", "red shoes"), ("page", "2"), ("tag", "x"), ("tag", "y")] red shoes ["x", "y"] null []`,
		"https://ada@shop.example:8443/a%20b/search?q=red+shoes&page=3#top q=red+shoes&page=2&tag=x&tag=y&sort=new",
		"https://ada@shop.example:8443/a/b%20c?q=red+shoes&page=2&tag=x&tag=y#top https://shop.example:8443 true",
		"Ok(https://ada@shop.example:8443/help?x=1) Ok(https://cdn.example/app.js) Ok(https://ada@shop.example:8443/a%20b/search?q=red+shoes&page=2&tag=x&tag=y#end)",
		"true  /docs/start fr false /docs/start?lang=fr",
		"https://api.example/v1/user%20list?name=Zo%C3%AB+%26+co&n=1 https://api.example/v1/user%20list?name=Zo%C3%AB+%26+co&n=1 Zoë & co",
		`[("a", "1"), ("b", "x y"), ("c", "&"), ("d", "")] []`,
		`"http://[::1" is not an address: missing ']' in host`,
		`"http://host:99x/" is not an address: invalid port ":99x" after host`,
		`" ht tp://x" is not an address: first path segment in URL cannot contain colon`,
	)
}

// What a page shows of what a visitor typed, and the characters a lexer
// tells apart.
func TestHtmlAndCharacters(t *testing.T) {
	expect(t, "import std.encoding;", `
		let typed = "<script>alert('hi')</script> & \"you\"";
		let safe = encoding.toHtml(typed);
		println(safe, safe.contains("<"), encoding.fromHtml(safe) == typed, encoding.fromHtml("caf&eacute; &#233; &amp;amp;"));

		println("été".isLetters(), "日本".isLetters(), "a1".isLetters(), "".isLetters(), "_".isLetters());
		println("a1é".isAlphanumeric(), "a-1".isAlphanumeric(), "٣".isAlphanumeric());
		println(" \t\n".isSpaces(), "".isSpaces(), "".isBlank(), " x ".isSpaces());
		println("HTTP/2".isUpper(), "Http".isUpper(), "123".isUpper(), "x2-y".isLower(), "ÉTÉ".isUpper(), "été".isLower());

		let kinds: [String] = [];
		for c in "x1 =é" {
			kinds.push(if c.isLetters() { "letter" } else if c.isDigits() { "digit" } else if c.isSpaces() { "space" } else { "other" });
		}
		println(kinds);
		`,
		"&lt;script&gt;alert(&#39;hi&#39;)&lt;/script&gt; &amp; &#34;you&#34; false true café é &amp;",
		"true true false false false",
		"true false true",
		"true false true false",
		"true false false true true true",
		`["letter", "digit", "space", "other", "letter"]`,
	)
}

// A site's files served from a folder by a route with a *: their content
// and its type, a folder's index, and nothing outside the folder.
func TestServingFiles(t *testing.T) {
	expect(t, `
		import std.http;
		import std.http.{Method, Response};
		import std.io;
		import std.fs;
		import std.path;`, `
		let site = fs.makeTempDir("site").expect("a folder");
		defer fs.removeAll(site);
		fs.makeDir(path.inside(site, "css")).expect("css");
		io.writeFile(path.inside(site, "index.html"), "<h1>home</h1>").expect("index");
		io.writeFile(path.join([site, "css", "site.css"]), "h1 { color: red; }").expect("css");
		io.writeFile(path.inside(site, "logo.png"), "\u{89}PNG").expect("png");
		io.writeFile(path.inside(site, "notes.zzz"), "?").expect("unknown");
		let secret = fs.makeTempDir("secret").expect("another folder");
		defer fs.removeAll(secret);
		io.writeFile(path.inside(secret, "key"), "hidden").expect("key");

		let app = http.router();
		app.get("/static/*", http.files(site));
		let show = fn(address: String) {
			let r = app.answer(http.request(Method.Get, address));
			println(address.replace(path.base(secret), "SECRET"), r.status, r.header("content-type"), r.body.len());
		};
		show("/static/index.html");
		show("/static/");
		show("/static/css/site.css");
		show("/static/./css//site.css");
		show("/static/logo.png");
		show("/static/notes.zzz");
		show("/static/missing.js");
		show("/static/../" + path.base(secret) + "/key");
		show("/static/css/../../" + path.base(secret) + "/key");
		let head = app.answer(http.request(Method.Head, "/static/css/site.css"));
		println(head.status, head.header("content-type"), head.body == "");
		`,
		"/static/index.html 200 text/html; charset=utf-8 13",
		"/static/ 200 text/html; charset=utf-8 13",
		"/static/css/site.css 200 text/css; charset=utf-8 18",
		"/static/./css//site.css 200 text/css; charset=utf-8 18",
		"/static/logo.png 200 image/png 4",
		"/static/notes.zzz 200 application/octet-stream 1",
		"/static/missing.js 404 text/plain; charset=utf-8 9",
		"/static/../SECRET/key 404 text/plain; charset=utf-8 9",
		"/static/css/../../SECRET/key 404 text/plain; charset=utf-8 9",
		"200 text/css; charset=utf-8 true",
	)
}
