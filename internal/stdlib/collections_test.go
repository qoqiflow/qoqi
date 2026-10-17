package stdlib_test

import "testing"

// A Map keeps its keys in the order they were first set, even when two of
// them are written the same and live under one text: 1 and "1" of a
// Map<Int | String, V> are two keys, listed where each was set.
func TestMapKeepsTheOrderKeysWereSet(t *testing.T) {
	expect(t, "import std.map;\nimport std.map.{Map};\nstruct Badge { name: String; level: Int; }\nimpl Show for Badge { fn show() -> String { return self.name; } }", `
		let m = map.empty<Int | String, String>();
		m.set(1, "one");
		m.set("x", "ex");
		m.set("1", "text one");
		m.set(2, "two");
		println(m.keys(), m.values(), m.len());
		println(m.get(1), m[ "1" ], m.has(3), m.getOr(3, "none"));

		// Taken out and set again, a key goes last
		println(m.remove(1), m.remove(1), m.keys());
		m.set(1, "back");
		println(m.entries());
		println(m);

		// Keys written the same, told apart by ==, each in its place
		let badges = map.empty<Badge, Int>();
		badges[Badge { name: "gold", level: 1 }] = 10;
		badges[Badge { name: "silver", level: 1 }] = 5;
		badges[Badge { name: "gold", level: 2 }] = 20;
		badges.update(Badge { name: "gold", level: 1 }, 0, n -> n + 1);
		let order: [String] = [];
		for badge in badges {
			order.push("${badge.name}/${badge.level}=${badges[badge] ?? 0}");
		}
		println(order, badges.len());

		// Many keys, most of them taken out: the order holds, the rest is found
		let squares = map.empty<(Int, Int), Int>();
		for i in 0..200 { squares.set((i, i % 3), i * i); }
		for i in 0..190 { squares.remove((i, i % 3)); }
		squares.set((7, 1), 49);
		println(squares.keys().map(k -> k.0), squares.get((195, 0)), squares.has((5, 2)), squares.len());

		// A copy has the same order, and is its own
		let copy = squares.copy();
		copy.remove((190, 1));
		println(copy.keys().first(), squares.keys().first(), copy == squares, squares.copy() == squares);
	`,
		`[1, "x", "1", 2] ["one", "ex", "text one", "two"] 4`,
		`one text one false none`,
		`true false ["x", "1", 2]`,
		`[("x", "ex"), ("1", "text one"), (2, "two"), (1, "back")]`,
		`{"x": "ex", "1": "text one", 2: "two", 1: "back"}`,
		`["gold/1=11", "silver/1=5", "gold/2=20"] 3`,
		`[190, 191, 192, 193, 194, 195, 196, 197, 198, 199, 7] 38025 false 11`,
		`(191, 2) (190, 1) false true`,
	)
}

// getOrSet gives the value of a key, made and set first when it has none:
// grouping in one line, on a Dict and on a Map.
func TestGetOrSet(t *testing.T) {
	expect(t, "import std.dict;\nimport std.map;\nenum Suit { Hearts, Spades }\nstruct Card { rank: Int; suit: Suit; }", `
		let groups = dict.empty<[String]>();
		for word in "the quick brown fox jumps over the lazy dog".words() {
			groups.getOrSet(word.first(1), () -> []).push(word);
		}
		println(groups);

		// make runs only for a key that is missing
		let made = 0;
		let counts = {"a": 1};
		let a = counts.getOrSet("a", fn() -> Int { made++; return 100; });
		let b = counts.getOrSet("b", fn() -> Int { made++; return 7; });
		println(a, b, made, counts);

		let hands = map.empty<Suit, [Card]>();
		let deal = [Card { rank: 3, suit: Spades }, Card { rank: 12, suit: Hearts }, Card { rank: 1, suit: Spades }];
		for card in deal {
			hands.getOrSet(card.suit, () -> []).push(card);
		}
		for (suit, cards) in hands.entries() {
			println(suit, cards.map(c -> c.rank).sorted());
		}
	`,
		`{"t": ["the", "the"], "q": ["quick"], "b": ["brown"], "f": ["fox"], "j": ["jumps"], "o": ["over"], "l": ["lazy"], "d": ["dog"]}`,
		`1 7 1 {"a": 1, "b": 7}`,
		`Spades [1, 3]`,
		`Hearts [12]`,
	)
}

// A copy of a Dict is its own: keys set or taken out of one are not in the
// other, and it keeps the order of the keys, past the few that are looked
// through as well.
func TestDictCopy(t *testing.T) {
	expect(t, "import std.dict;\nimport std.dict.{Dict};", `
		let stock = {"apple": 3, "pear": 0, "plum": 12};
		let saved = stock.copy();
		stock["apple"] = 2;
		stock.remove("pear");
		saved["kiwi"] = 5;
		println(stock, saved, saved == stock);

		let many = dict.empty<Int>();
		for i in 0..20 { many.set("k${i}", i); }
		for i in 0..15 { many.remove("k${i}"); }
		let again: Dict<Int> = many.copy();
		again.set("k0", 0);
		println(again.keys(), many.has("k0"), again.get("k19"), many.copy() == many);
	`,
		`{"apple": 2, "plum": 12} {"apple": 3, "pear": 0, "plum": 12, "kiwi": 5} false`,
		`["k15", "k16", "k17", "k18", "k19", "k0"] false 19 true`,
	)
}

// A set tells its items apart as a Map tells its keys: written the same
// and ==. One made with set.by goes by the text alone.
func TestSetTellsItemsApartAsMapDoes(t *testing.T) {
	expect(t, "import std.set;\nimport std.set.{Set};\nstruct Tag { name: String; weight: Int; }\nimpl Show for Tag { fn show() -> String { return self.name; } }", `
		// Written the same, not equal: both kept
		let tags = set.of([Tag { name: "red", weight: 1 }, Tag { name: "red", weight: 2 }, Tag { name: "red", weight: 1 }]);
		println(tags.len(), tags, tags.has(Tag { name: "red", weight: 2 }), tags.has(Tag { name: "red", weight: 3 }));
		println(tags.remove(Tag { name: "red", weight: 1 }), tags.items().map(t -> t.weight));

		let mixed: Set<Int | String> = set.empty<Int | String>();
		println(mixed.add(1), mixed.add("1"), mixed.add(1), mixed.len(), mixed.has("1"), mixed);

		// By a text alone: names whatever their case
		let names = set.by(fn(name: String) -> String { return name.lower(); });
		names.addAll(["Ada", "ada", "Grace", "ADA", "grace", "Linus"]);
		println(names, names.has("aDa"), names.copy().add("LINUS"), names.filter(n -> n.len() > 3).add("GRACE"));
		println(names.union(set.of(["linus", "Alan"])), names.difference(set.of(["Ada"])));

		// The order of first adding, through removals
		let seen = set.numbers();
		for n in [5, 3, 5, 9, 1, 3] { seen.add(n); }
		seen.remove(3);
		seen.add(3);
		println(seen.items(), seen == set.of([1, 3, 5, 9]));
	`,
		`2 {red, red} true false`,
		`true [2]`,
		`true true false 2 true {1, "1"}`,
		`{"Ada", "Grace", "Linus"} true false false`,
		`{"Ada", "Grace", "Linus", "Alan"} {"Grace", "Linus"}`,
		`[5, 9, 1, 3] true`,
	)
}

// sortedBy, maxBy and minBy rank by anything Ordered: a number, a Float, a
// text, a type of the program's own. The first of several ranked the same
// is the one kept.
func TestRankByAnythingOrdered(t *testing.T) {
	expect(t, "import std.ops.{Ordered};\nstruct Runner { name: String; time: Float; bib: Int; }\nstruct Grade { letter: String; }\nimpl Ordered for Grade { fn compare(other: Grade) -> Int { return other.letter.compare(self.letter); } }\nfn bib(r: Runner) -> Int { return r.bib; }", `
		let runners = [
			Runner { name: "kip", time: 12.5, bib: 7 },
			Runner { name: "ada", time: 11.75, bib: 3 },
			Runner { name: "zoe", time: 12.5, bib: 1 },
			Runner { name: "bo", time: 13.0, bib: 9 },
		];
		let names = fn(rs: [Runner]) -> [String] { return rs.map(r -> r.name); };
		println(names(runners.sortedBy(r -> r.time)), names(runners.sortedBy(r -> r.name)), names(runners.sortedBy(bib)));
		println(runners.minBy(r -> r.time)?.name, runners.maxBy(r -> r.time)?.name, runners.minBy(r -> r.name)?.name, runners.maxBy(r -> r.name)?.name);

		// Ties keep the first: kip and zoe both ran 12.5
		println(runners.filter(r -> r.time > 12.0).minBy(r -> r.time)?.name, runners.sortedBy(r -> r.time < 12.6 ? 0 : 1).map(r -> r.bib));
		println(runners.maxBy(bib)?.name, runners.minBy(r -> -r.bib)?.name, runners.sortedByText(r -> r.name.last(1)).map(r -> r.name));

		// A type of the program's own: A comes before B, so A ranks highest
		let grades = ["B", "A", "C"].map(l -> Grade { letter: l });
		println(grades.maxBy(g -> g)?.letter, grades.sortedBy(g -> g).map(g -> g.letter));

		let none: [Runner] = [];
		println(none.maxBy(r -> r.time), none.minBy(r -> r.name), none.sortedBy(r -> r.time).len());
	`,
		`["ada", "kip", "zoe", "bo"] ["ada", "bo", "kip", "zoe"] ["zoe", "ada", "kip", "bo"]`,
		`ada bo ada zoe`,
		`kip [7, 3, 1, 9]`,
		`bo bo ["ada", "zoe", "bo", "kip"]`,
		`A ["C", "B", "A"]`,
		`null null 0`,
	)
}

// unique for numbers, as for texts: the first of each kept, in order.
func TestUniqueNumbers(t *testing.T) {
	expect(t, "", `
		let rolls = [4, 2, 4, 6, 2, 1];
		let readings = [0.5, 1.25, 0.5, -0.0, 0.0, 1.25];
		println(rolls.unique(), rolls.unique().sorted(), rolls.unique().len(), readings.unique(), ["b", "a", "b"].unique());
		let none: [Int] = [];
		println(none.unique(), rolls.map(n -> n % 3).unique().join("/"));
	`,
		`[4, 2, 6, 1] [1, 2, 4, 6] 4 [0.5, 1.25, -0, 0] ["b", "a"]`,
		`[] 1/2/0`,
	)
}

// A List prints as an array does, alone, in a text and inside other
// values, and has the methods of arrays that make new arrays, giving
// Lists: a shop's orders, read left to right.
func TestListPrintsAndForwards(t *testing.T) {
	expect(t, "import std.list;\nimport std.list.{List};\nstruct Order { customer: String; total: Float; items: [String]; }", `
		let orders = list.of([
			Order { customer: "ada", total: 12.5, items: ["tea", "scone"] },
			Order { customer: "bo", total: 40.0, items: ["cake", "tea"] },
			Order { customer: "ada", total: 7.25, items: ["tea"] },
			Order { customer: "cy", total: 19.0, items: ["jam"] },
		]);
		let totals = orders.map(o -> o.total);
		println(totals, "totals: ${totals}", [totals, list.of([1.5])], list.of(["a", "b"]), list.empty<Int>());

		let biggest = orders.sortedBy(o -> -o.total).take(2).map(o -> o.customer);
		println(biggest, orders.drop(3).map(o -> o.customer), orders.maxBy(o -> o.total)?.customer, orders.minBy(o -> o.customer)?.customer);
		println(orders.flatMap(o -> o.items).uniqueBy(i -> i), orders.countBy(o -> o.customer), orders.sumOf(o -> o.items.len()));

		let byCustomer = orders.groupBy(o -> o.customer);
		for (customer, theirs) in byCustomer.entries() {
			println(customer, theirs.len(), theirs.map(o -> o.total).reduce((a, b) -> a + b) ?? 0.0);
		}

		let (large, small) = orders.partition(o -> o.total > 15.0);
		println(large.map(o -> o.customer), small.map(o -> o.customer));
		println(list.of([1, 2, 3, 4, 5]).chunks(2), list.of([1, 2]).concat(list.of([3])), list.empty<Int>().reduce((a, b) -> a + b));

		// A copy is its own list
		let names = list.of(["ada", "bo"]);
		let saved = names.copy();
		names.add("cy");
		println(names, saved, saved == list.of(["ada", "bo"]), names.sortedBy(n -> n.len()).first());
	`,
		`[12.5, 40, 7.25, 19] totals: [12.5, 40, 7.25, 19] [[12.5, 40, 7.25, 19], [1.5]] ["a", "b"] []`,
		`["bo", "cy"] ["cy"] bo ada`,
		`["tea", "scone", "cake", "jam"] {"ada": 2, "bo": 1, "cy": 1} 6`,
		`ada 2 19.75`,
		`bo 1 40`,
		`cy 1 19`,
		`["bo", "cy"] ["ada", "ada"]`,
		`[[1, 2], [3, 4], [5]] [1, 2, 3] null`,
		`["ada", "bo", "cy"] ["ada", "bo"] true bo`,
	)
}
