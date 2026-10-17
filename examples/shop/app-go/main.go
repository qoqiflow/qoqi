// The shop's application: it carries out the steps of the orders' flow,
// orders.qi, those of the warehouse and those of payments, over a
// database of SQLite. The warehouse's steps change that database, and are
// kept under once: each writes its effect in the transaction flow.TxOf
// gives, with its answer. A charge reaches a payment provider, and is
// kept under started. Either way a step sent twice acts once.
//
//	SHOP_SECRET=dev go run ./examples/shop/app-go
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/qoqiflow/qoqi/examples/shop/app-go/steps"
	"github.com/qoqiflow/qoqi/pkg/flow"
	_ "modernc.org/sqlite"
)

// warehouse holds stock for orders, and ships them.
type warehouse struct{}

func (warehouse) Reserve(ctx context.Context, task *flow.Task, order string, items []steps.Item) (flow.Result[steps.Hold, steps.Problem], *steps.Hold, error) {
	tx := flow.TxOf(ctx)
	for _, item := range items {
		var left int
		err := tx.QueryRowContext(ctx, `SELECT quantity FROM stock WHERE sku = ?`, item.SKU).Scan(&left)
		if err == sql.ErrNoRows || left < item.Quantity {
			return flow.Err[steps.Hold, steps.Problem](steps.ProblemOutOfStock{Value: item.SKU}), nil, nil
		}
		if err != nil {
			return flow.Result[steps.Hold, steps.Problem]{}, nil, err
		}
	}
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE stock SET quantity = quantity - ? WHERE sku = ?`, item.Quantity, item.SKU); err != nil {
			return flow.Result[steps.Hold, steps.Problem]{}, nil, err
		}
	}
	hold := steps.Hold{ID: "hold-" + order}
	lines, _ := json.Marshal(items)
	if _, err := tx.ExecContext(ctx, `INSERT INTO holds (hold, items) VALUES (?, ?)`, hold.ID, string(lines)); err != nil {
		return flow.Result[steps.Hold, steps.Problem]{}, nil, err
	}
	log.Printf("[warehouse] holds %d lines for order %s", len(items), order)
	return flow.Ok[steps.Hold, steps.Problem](hold), &hold, nil
}

func (warehouse) Release(ctx context.Context, task *flow.Task, hold steps.Hold) (flow.Result[flow.Void, steps.Problem], error) {
	tx := flow.TxOf(ctx)
	var lines string
	if err := tx.QueryRowContext(ctx, `SELECT items FROM holds WHERE hold = ?`, hold.ID).Scan(&lines); err != nil && err != sql.ErrNoRows {
		return flow.Result[flow.Void, steps.Problem]{}, err
	}
	var items []steps.Item
	json.Unmarshal([]byte(lines), &items)
	for _, item := range items {
		if _, err := tx.ExecContext(ctx, `UPDATE stock SET quantity = quantity + ? WHERE sku = ?`, item.Quantity, item.SKU); err != nil {
			return flow.Result[flow.Void, steps.Problem]{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM holds WHERE hold = ?`, hold.ID); err != nil {
		return flow.Result[flow.Void, steps.Problem]{}, err
	}
	log.Printf("[warehouse] releases %s", hold.ID)
	return flow.Ok[flow.Void, steps.Problem](flow.Void{}), nil
}

func (warehouse) Ship(ctx context.Context, task *flow.Task, hold steps.Hold, to string, speed steps.Speed) (flow.Result[string, steps.Problem], error) {
	if to == "nowhere" {
		return flow.Err[string, steps.Problem](steps.ProblemUndeliverable{Value: "no carrier goes to " + to}), nil
	}
	tx := flow.TxOf(ctx)
	var shipped int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM parcels`).Scan(&shipped); err != nil {
		return flow.Result[string, steps.Problem]{}, err
	}
	parcel := fmt.Sprintf("parcel-%d", shipped+1)
	if express, ok := speed.(steps.SpeedExpress); ok {
		parcel = fmt.Sprintf("express-%d-%dd", shipped+1, express.Value)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO parcels (parcel, hold) VALUES (?, ?)`, parcel, hold.ID); err != nil {
		return flow.Result[string, steps.Problem]{}, err
	}
	log.Printf("[warehouse] ships %s to %s as %s", hold.ID, to, parcel)
	return flow.Ok[string, steps.Problem](parcel), nil
}

// payments charges cards at a payment provider, which the shop's own
// table of charges stands for here.
type payments struct {
	db *sql.DB
}

func (p payments) Charge(ctx context.Context, task *flow.Task, customer string, cents int) (flow.Result[string, steps.Decline], *string, error) {
	switch {
	case customer == "mallory":
		return flow.Err[string, steps.Decline](steps.DeclineFraud{Value: "the card was reported stolen"}), nil, nil
	case cents > 100_000:
		return flow.Err[string, steps.Decline](steps.DeclineInsufficient{}), nil, nil
	}
	var charged int
	if err := p.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM charges`).Scan(&charged); err != nil {
		return flow.Result[string, steps.Decline]{}, nil, err
	}
	id := fmt.Sprintf("ch-%d", charged+1)
	if _, err := p.db.ExecContext(ctx, `INSERT INTO charges (charge, customer) VALUES (?, ?)`, id, customer); err != nil {
		return flow.Result[string, steps.Decline]{}, nil, err
	}
	log.Printf("[payments] charges %s %d cents as %s", customer, cents, id)
	return flow.Ok[string, steps.Decline](id), &id, nil
}

func (payments) Refund(ctx context.Context, task *flow.Task, charge string) (flow.Result[flow.Void, steps.Decline], error) {
	log.Printf("[payments] refunds %s", charge)
	return flow.Ok[flow.Void, steps.Decline](flow.Void{}), nil
}

// open is the shop's database, made if it is not there.
func open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS stock (sku TEXT PRIMARY KEY, quantity INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS holds (hold TEXT PRIMARY KEY, items TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS parcels (parcel TEXT PRIMARY KEY, hold TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS charges (charge TEXT PRIMARY KEY, customer TEXT NOT NULL);
		INSERT OR IGNORE INTO stock (sku, quantity) VALUES ('mug', 10), ('lamp', 1);`)
	return db, err
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "the address the queues are served at")
	flag.Parse()
	secret := os.Getenv("SHOP_SECRET")
	if secret == "" {
		log.Fatal("SHOP_SECRET is the secret the daemon signs the steps with, as qoqi.json says")
	}
	path := os.Getenv("SHOP_DB")
	if path == "" {
		path = "shop.db"
	}
	db, err := open(path)
	if err != nil {
		log.Fatal(err)
	}
	keeper := &flow.Keeper{DB: db}
	if err := keeper.Setup(context.Background()); err != nil {
		log.Fatal(err)
	}

	// The warehouse's steps change the shop's database: under once. A
	// charge reaches the provider: under started
	stock := steps.WarehouseQueue(warehouse{}, secret)
	stock.KeepOnce(keeper)
	cards := steps.PaymentsQueue(payments{db: db}, secret)
	cards.KeepStarted(keeper)

	mux := http.NewServeMux()
	mux.Handle("/warehouse", stock)
	mux.Handle("/payments", cards)
	log.Printf("the shop serves its queues at http://%s/warehouse and /payments", *listen)
	log.Fatal(http.ListenAndServe(*listen, mux))
}
