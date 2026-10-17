package stdlib_test

import "testing"

// Messages with their level and values, as text and as JSON, caught by a
// test instead of the error output.
func TestLog(t *testing.T) {
	expect(t, "import std.log;", `
		let lines: [String] = [];
		log.writeTo(line -> lines.push(line));

		log.info("list saved", {"items": 12, "file": "todo.json"});
		log.debug("not written at Info");
		log.setLevel(log.Level.Debug);
		log.debug("written", {"note": "two words", "empty": "", "ratio": 0.5, "eq": "a=b", "q": "say \"hi\""});
		let request = log.with({"request": "a1f3", "path": "/notes"});
		request.warn("slow answer", {"ms": 950, "path": "/notes/7"});
		request.with({"user": "ada"}).info("seen");
		log.setLevel(log.Level.Error);
		request.warn("dropped");
		log.useJson();
		request.error("failed", {"reason": "the \"db\" is down", "retry": true, "n": null, "score": 2.5});
		log.useText();
		log.error("back to text");

		for line in lines {
			// The moment first, then the rest
			let moment = line.split(" ")[0];
			if line.startsWith("{") {
				println(line.contains("\"time\":\"20"), line.from(line.indexOf("\"level\"")!));
			} else {
				println(moment.len(), moment.endsWith("Z"), moment[10], line.from(moment.len() + 1));
			}
		}
		println(log.Level.Warn.name());
		`,
		"24 true T INFO list saved items=12 file=todo.json",
		`24 true T DEBUG written note="two words" empty="" ratio=0.5 eq="a=b" q="say \"hi\""`,
		"24 true T WARN slow answer request=a1f3 ms=950 path=/notes/7",
		"24 true T INFO seen request=a1f3 path=/notes user=ada",
		`true "level":"error","message":"failed","request":"a1f3","path":"/notes","reason":"the \"db\" is down","retry":true,"n":null,"score":2.5}`,
		"24 true T ERROR back to text",
		"WARN",
	)
}
