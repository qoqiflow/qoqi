package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProgramArguments checks what a program is started with: what
// follows the file is its own, except the options of qoqi itself.
func TestProgramArguments(t *testing.T) {
	dir := project(t, map[string]string{
		"args.qi": `
			import std.os;
			import std.path;
			fn main() -> Int {
				println(os.args(), path.base(os.program()), path.isAbsolute(os.program()) || path.parts(os.program()).len() > 0);
				return os.args().len();
			}`,
		"qoqi.json": `{"name": "args", "entry": "args.qi"}`,
	})
	file := filepath.Join(dir, "args.qi")

	cases := []struct {
		args []string
		want string
		exit int
	}{
		{[]string{"run", file}, `[] args.qi true`, 0},
		{[]string{"run", file, "add", "two words", ""}, `["add", "two words", ""] args.qi true`, 3},
		// Options of the program pass through, with or without a value
		{[]string{"run", file, "--all", "--due=2026-10-20", "-x"}, `["--all", "--due=2026-10-20", "-x"] args.qi true`, 3},
		// The options of qoqi are taken out wherever they are
		{[]string{"run", "--max-depth=500", file, "a", "--heap-mb=64", "b"}, `["a", "b"] args.qi true`, 2},
		// ...unless they come after --, which is for the program too
		{[]string{"run", file, "a", "--", "--heap-mb=1", "--", "b"}, `["a", "--heap-mb=1", "--", "b"] args.qi true`, 4},
		// A project is run by its folder
		{[]string{"run", dir, "x"}, `["x"] args.qi true`, 1},
	}
	for _, c := range cases {
		res := runQoqi(t, c.args...)
		if strings.TrimSpace(res.stdout) != c.want || res.exit != c.exit || res.stderr != "" {
			t.Errorf("qoqi %v:\n  want %s, exit %d\n   got %s, exit %d\n%s", c.args, c.want, c.exit, strings.TrimSpace(res.stdout), res.exit, res.stderr)
		}
	}
}

// TestExitStatus checks that os.exit ends the process with its status,
// from wherever it is called, without an error message.
func TestExitStatus(t *testing.T) {
	dir := project(t, map[string]string{
		"exit.qi": `
			import std.os;
			import std.result.{attempt};
			fn leave(status: Int) -> Int {
				os.exit(status);
				return 99;
			}
			fn main() -> Int {
				let status = os.args()[0].parseInt() ?? 0;
				println("before");
				if os.args()[1] == "caught" {
					let outcome = attempt(fn() -> Int { return leave(status); });
					println("after", outcome.isOk());
				} else {
					leave(status);
				}
				println("not reached");
				return 50;
			}`,
	})
	file := filepath.Join(dir, "exit.qi")
	for _, c := range []struct {
		status, how string
		exit        int
	}{{"0", "plain", 0}, {"3", "plain", 3}, {"7", "caught", 7}, {"255", "plain", 255}, {"256", "plain", 0}} {
		res := runQoqi(t, "run", file, c.status, c.how)
		if res.exit != c.exit || res.stdout != "before\n" || res.stderr != "" {
			t.Errorf("exit(%s), %s: expected status %d after 'before', got %d, %q\n%s", c.status, c.how, c.exit, res.exit, res.stdout, res.stderr)
		}
	}
}

// TestSalesAndLogsExamples runs the two reports on files of the test's own.
func TestSalesAndLogsExamples(t *testing.T) {
	dir := t.TempDir()
	orders := filepath.Join(dir, "orders.csv")
	summary := filepath.Join(dir, "out", "summary.json")
	content := "order,date,customer,product,category,quantity,unit_price\n" +
		"1,2026-10-05,\"Doe, Jane\",Pen,stationery,10,1.50\n" +
		"2,2026-10-06,John,Desk,furniture,1,200\n" +
		"3,soon,John,Desk,furniture,1,200\n"
	if err := os.WriteFile(orders, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// The folder of the summary does not exist: said, with status 1
	res := runQoqi(t, "run", "examples/sales", orders, summary)
	if res.exit != 1 || !strings.Contains(res.stdout, "cannot write "+summary+": no such file or directory") {
		t.Errorf("expected the summary to be refused, got %d:\n%s%s", res.exit, res.stdout, res.stderr)
	}
	if err := os.Mkdir(filepath.Dir(summary), 0755); err != nil {
		t.Fatal(err)
	}
	res = runQoqi(t, "run", "examples/sales", orders, summary)
	for _, want := range []string{
		"2 orders over 2 days, 215.00 in all, 107.50 an order",
		"furniture        1      1   200.00  93.0%",
		"Doe, Jane       1     10    15.00   7.0%",
		"1 row left out of 3",
		"row 4 (order 3): 'soon' is not a date",
		"summary written to summary.json, sha256 ",
	} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("expected %q in the report, got:\n%s%s", want, res.stdout, res.stderr)
		}
	}
	var written struct {
		From, To  string
		Orders    int
		LeftOut   int `json:"left_out"`
		Customers []struct {
			Name    string
			Revenue float64
		}
	}
	data, err := os.ReadFile(summary)
	if err != nil || json.Unmarshal(data, &written) != nil {
		t.Fatalf("expected a summary in JSON, got %s (%v)", data, err)
	}
	if written.From != "2026-10-05" || written.Orders != 2 || written.LeftOut != 1 || len(written.Customers) != 2 || written.Customers[0].Name != "John" || written.Customers[1].Revenue != 15 {
		t.Errorf("unexpected summary: %+v", written)
	}

	for _, c := range [][2]string{
		{"order,date\n1\n", "orders.csv, line 2, column 1: expected 2 values, as in the header, found 1"},
		{"a,\"b\n", "orders.csv, line"},
		{"order,date,customer,product,category,quantity,unit_price\n", "no order could be read from"},
	} {
		os.WriteFile(orders, []byte(c[0]), 0644)
		if res := runQoqi(t, "run", "examples/sales", orders); res.exit != 1 || !strings.Contains(res.stdout, c[1]) {
			t.Errorf("%q: expected %q and status 1, got %d:\n%s", c[0], c[1], res.exit, res.stdout)
		}
	}

	// The log report, on a log of two lines, one of them broken
	log := filepath.Join(dir, "access.log")
	os.WriteFile(log, []byte("10.0.0.1 - bob [01/Jan/2026:23:59:59 +0100] \"GET /a/7?x=1 HTTP/1.1\" 200 2048 1.5\n\ngarbage\n"), 0644)
	res = runQoqi(t, "run", "examples/logs", log)
	for _, want := range []string{
		"Access log, Thursday 1 January 2026",
		"1 request from 1 client, 22:59 to 22:59", // told in UTC
		"2.0 KB sent",
		"/a/:id     1  1.5s          0",
		"bob: 1 request, 0 of them changing something",
		"GET /a/7 took 1.5s at 22:59:59 (status 200)",
		"line 3: not a log line: garbage...",
	} {
		if !strings.Contains(res.stdout, want) {
			t.Errorf("expected %q in the report, got:\n%s%s", want, res.stdout, res.stderr)
		}
	}
	if res := runQoqi(t, "run", "examples/logs", filepath.Join(dir, "none.log")); res.exit != 1 || !strings.Contains(res.stderr, "none.log: no such file or directory") {
		t.Errorf("expected a missing log to be reported, got %d: %s", res.exit, res.stderr)
	}
}
