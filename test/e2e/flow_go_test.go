package e2e

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// The Go of the shop's steps is what qoqi gen writes from its
// flow, now: a signature changed in Qoqi is changed there.
func TestTheGoOfTheShopIsWrittenFromItsFlow(t *testing.T) {
	cmd := exec.Command(qoqi, "gen", "go", "examples/shop/orders.qi", "--package", "steps")
	cmd.Dir = repoRoot
	written, err := cmd.Output()
	if err != nil {
		t.Fatalf("qoqi gen: %v", err)
	}
	kept, err := os.ReadFile(filepath.Join(repoRoot, "examples/shop/app-go/steps/steps_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(kept) {
		t.Errorf("examples/shop/app-go/steps/steps_gen.go is not what qoqi gen writes: run\n  qoqi gen go examples/shop/orders.qi --package steps > examples/shop/app-go/steps/steps_gen.go")
	}
}

// The shop's flow, deployed to qoqi serve, its steps carried out by
// the shop's application in Go, built from the example.
func TestTheShopInGo(t *testing.T) {
	dir := t.TempDir()
	app := filepath.Join(dir, "shop")
	build := exec.Command("go", "build", "-o", app, "./examples/shop/app-go")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("cannot build the shop: %v\n%s", err, out)
	}
	theShop(t, dir, func(address string) *exec.Cmd {
		cmd := exec.Command(app, "-listen", address)
		cmd.Env = append(os.Environ(), "SHOP_DB="+filepath.Join(dir, "shop.db"))
		return cmd
	})
}

// The same, its steps carried out by the shop's application in PHP, on
// PHP's own server, its database a file of SQLite.
func TestTheShopInPHP(t *testing.T) {
	if _, err := exec.LookPath("php"); err != nil {
		t.Skip("php is not installed")
	}
	dir := t.TempDir()
	app, err := filepath.Abs(filepath.Join(repoRoot, "examples/shop/app-php"))
	if err != nil {
		t.Fatal(err)
	}
	theShop(t, dir, func(address string) *exec.Cmd {
		cmd := exec.Command("php", "-S", address, "-t", "public", "public/index.php")
		cmd.Dir = app
		cmd.Env = append(os.Environ(), "SHOP_DB="+filepath.Join(dir, "shop.sqlite"))
		return cmd
	})
}

// The same, its steps carried out by the shop's application in
// TypeScript, on Node, which runs it as it is written.
func TestTheShopInTypeScript(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	app, err := filepath.Abs(filepath.Join(repoRoot, "examples/shop/app-ts/main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	theShop(t, dir, func(address string) *exec.Cmd {
		cmd := exec.Command("node", app, "--listen", address)
		cmd.Env = append(os.Environ(), "SHOP_DB="+filepath.Join(dir, "shop.db"))
		return cmd
	})
}

// The TypeScript of the shop's steps is what qoqi gen writes from
// its flow, now.
func TestTheTypeScriptOfTheShopIsWrittenFromItsFlow(t *testing.T) {
	cmd := exec.Command(qoqi, "gen", "ts", "examples/shop/orders.qi", "--from", "../../../sdk/ts/src/index.ts")
	cmd.Dir = repoRoot
	written, err := cmd.Output()
	if err != nil {
		t.Fatalf("qoqi gen: %v", err)
	}
	kept, err := os.ReadFile(filepath.Join(repoRoot, "examples/shop/app-ts/steps.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != string(kept) {
		t.Errorf("examples/shop/app-ts/steps.ts is not what qoqi gen writes: run\n  qoqi gen ts examples/shop/orders.qi --from ../../../sdk/ts/src/index.ts > examples/shop/app-ts/steps.ts")
	}
}

// The PHP of the shop's steps is what qoqi gen writes from its
// flow, now.
func TestThePHPOfTheShopIsWrittenFromItsFlow(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command(qoqi, "gen", "php", "examples/shop/orders.qi", "--namespace", `App\Flow`, "--out", dir)
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("qoqi gen: %v\n%s", err, out)
	}
	kept := filepath.Join(repoRoot, "examples/shop/app-php/src/Flow")
	written := map[string]string{}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			data, _ := os.ReadFile(path)
			written[rel] = string(data)
		}
		return nil
	})
	if len(written) == 0 {
		t.Fatal("nothing written")
	}
	for rel, text := range written {
		data, err := os.ReadFile(filepath.Join(kept, rel))
		if err != nil || string(data) != text {
			t.Errorf("examples/shop/app-php/src/Flow/%s is not what qoqi gen writes: run\n  qoqi gen php examples/shop/orders.qi --namespace 'App\\Flow' --out examples/shop/app-php/src/Flow", rel)
		}
	}
}

// theShop starts the application start makes, at an address of its own,
// and the daemon, its queues there: an order that goes through; one
// declined, its stock released; one that cannot ship, refunded and
// released; one out of stock; one express.
func theShop(t *testing.T, dir string, start func(address string) *exec.Cmd) {
	t.Helper()
	for _, name := range []string{"orders.qi", "qoqi.json"} {
		data, err := os.ReadFile(filepath.Join(repoRoot, "examples/shop", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	shopOut := &lockedWriter{}
	shop := start(address)
	if shop.Env == nil {
		shop.Env = os.Environ()
	}
	shop.Env = append(shop.Env, "SHOP_SECRET=dev")
	shop.Stdout, shop.Stderr = shopOut, shopOut
	if err := shop.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		shop.Process.Signal(syscall.SIGTERM)
		shop.Wait()
	}()

	out := &lockedWriter{}
	daemon := serveIn(t, dir, out, "SHOP_SECRET=dev",
		"WAREHOUSE_URL=http://"+address+"/warehouse", "PAYMENTS_URL=http://"+address+"/payments")
	defer func() {
		daemon.Process.Signal(syscall.SIGTERM)
		daemon.Wait()
	}()
	deployIn(t, dir, "orders.qi", out)

	for _, c := range []struct{ id, customer, items, result string }{
		{"o1", "ada", `[{"sku": "mug", "quantity": 2, "cents": 1200}], "speed": "Standard"`, `Ok(Receipt { charge: "ch-1", parcel: "parcel-1" })`},
		{"o2", "mallory", `[{"sku": "mug", "quantity": 1, "cents": 1200}], "speed": "Standard"`, `Err(Declined(Fraud("the card was reported stolen")))`},
		{"o3", "nowhere", `[{"sku": "mug", "quantity": 1, "cents": 1200}], "speed": "Standard"`, `Err(Undeliverable("no carrier goes to nowhere"))`},
		{"o4", "lin", `[{"sku": "lamp", "quantity": 2, "cents": 9900}], "speed": "Standard"`, `Err(OutOfStock("lamp"))`},
		{"o5", "grace", `[{"sku": "mug", "quantity": 1, "cents": 1200}], "speed": {"Express": 2}`, `parcel: "express-2-2d"`},
	} {
		fields := `{"id": "` + c.id + `", "customer": "` + c.customer + `", "items": ` + c.items + `}`
		if got, code := flowCommand(t, dir, "start", "Order", c.id, fields); code != 0 {
			t.Fatalf("start %s: %d %s\n%s", c.id, code, got, out.String())
		}
		got, code := flowCommand(t, dir, "result", c.id, "--wait", "20s")
		if code != 0 || !strings.Contains(got, c.result) {
			t.Errorf("%s: expected %s, got %d %s\ndaemon:\n%s\nshop:\n%s", c.id, c.result, code, got, out.String(), shopOut.String())
		}
	}
	said := shopOut.String()
	for _, want := range []string{
		"[warehouse] ships hold-o1 to ada as parcel-1",
		"[warehouse] releases hold-o2",
		"[payments] refunds ch-2",
		"[warehouse] releases hold-o3",
	} {
		if !strings.Contains(said, want) {
			t.Errorf("expected the shop to say %q:\n%s", want, said)
		}
	}
	if strings.Contains(said, "hold-o4") {
		t.Errorf("nothing held for an order out of stock:\n%s", said)
	}
}
