package stdlib_test

import "testing"

// Arrays of Floats have what arrays of Ints have: a sum, the smallest and
// the largest, and their numbers in order.
func TestFloatArrays(t *testing.T) {
	expect(t, "struct Item { name: String; price: Float; }", `
		let prices = [2.5, 0.75, 10.0, 0.75];
		println(prices.sum(), prices.smallest(), prices.largest());
		println(prices.sortedNumbers(), prices.sorted(), prices);
		let none: [Float] = [];
		println(none.sum(), none.smallest(), none.largest(), none.sorted());

		let items = [Item { name: "tea", price: 3.5 }, Item { name: "cake", price: 4.25 }];
		let total = items.map(i -> i.price).sum();
		println(total, total * 2);
		println(items.map(i -> i.price * 1.2).largest()! > 5.0);
		`,
		"14 0.75 10",
		"[0.75, 0.75, 2.5, 10] [0.75, 0.75, 2.5, 10] [2.5, 0.75, 10, 0.75]",
		"0 null null []",
		"7.75 15.5",
		"true",
	)
}

// Numbers and texts are sorted without a function, in their own order, and
// with one like any array. A generic function that sorts what it is given
// takes the function too, whatever the array turns out to hold.
func TestSortedInTheirOwnOrder(t *testing.T) {
	expect(t, `
		struct Runner { name: String; seconds: Int; }
		fn best<T>(items: [T], before: fn(T, T) -> Bool) -> T? {
			return items.sorted(before).first();
		}
		fn ranked(runners: [Runner]) -> [String] {
			return runners.sortedBy(r -> r.seconds).map(r -> r.name);
		}`, `
		let times = [31, 7, 19, 7];
		println(times.sorted(), times.sorted((a, b) -> a > b), times);
		println(["pear", "Fig", "apple"].sorted(), ["pear", "fig", "apple"].sorted((a, b) -> a.len() < b.len()));
		println([0.5, -1.5].sorted((a, b) -> a > b));
		println(best(times, (a, b) -> a < b), best(["b", "a"], (a, b) -> a < b), best([2.5, 1.5], (a, b) -> a > b));

		let runners = [Runner { name: "ada", seconds: 70 }, Runner { name: "bo", seconds: 55 }];
		println(ranked(runners), runners.map(r -> r.seconds).sorted().first());
		println([].sum(), [].len());
		`,
		"[7, 7, 19, 31] [31, 19, 7, 7] [31, 7, 19, 7]",
		`["Fig", "apple", "pear"] ["fig", "pear", "apple"]`,
		"[0.5, -1.5]",
		`7 a 2.5`,
		`["bo", "ada"] 55`,
		"0 0",
	)
}

// JSON values are made one by one as well as in arrays.
func TestJsonValues(t *testing.T) {
	expect(t, "import std.json;", `
		println(json.text("a \"b\"").stringify(), json.number(0.5).stringify(), json.bool(false).stringify());
		println(json.array([1.5, 2.0], json.number).stringify(), json.array([true], json.bool).stringify());
		let back = json.parse(json.object().set("name", json.text("ada")).set("ok", json.bool(true)).done().stringify()).expect("valid");
		println(back.get("name")?.text(), back.get("ok")?.bool(), json.number(2.25).number());
		`,
		`"a \"b\"" 0.5 false`,
		"[1.5,2] [true]",
		"ada true 2.25",
	)
}
