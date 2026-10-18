# A shop

An order held, charged and shipped, by a flow in Qoqi whose steps the
shop's application carries out: [orders.qi](orders.qi) declares them
with their queues, `warehouse` and `payments`. The application is written
three times, the same: in Go, [app-go/](app-go), in PHP, [app-php/](app-php),
and in TypeScript, [app-ts/](app-ts).

```text
$ SHOP_SECRET=dev go run ./examples/shop/app-go &
    or
$ (cd examples/shop/app-php && SHOP_SECRET=dev php -S 127.0.0.1:8090 -t public public/index.php &)
    or
$ SHOP_SECRET=dev node examples/shop/app-ts/main.ts &

$ cd examples/shop
$ export SHOP_SECRET=dev WAREHOUSE_URL=http://127.0.0.1:8090/warehouse PAYMENTS_URL=http://127.0.0.1:8090/payments
$ qoqi serve &                     # the daemon reads the queues of the steps from this environment
$ qoqi deploy orders.qi
$ qoqi start Order order:1 '{"id": "1", "customer": "ada", "items": [{"sku": "mug", "quantity": 2, "cents": 1200}], "speed": "Standard"}'
$ qoqi result order:1
Ok(Receipt { charge: "ch-1", parcel: "parcel-1" })
```

`mallory`'s card is declined, and the stock held for her is released; an
order to `nowhere` is charged, cannot ship, and is refunded and released.

The types of the steps, an interface for each queue and its handler are
written from the flow; after a change to a step's signature:

```text
$ qoqi gen go examples/shop/orders.qi --package steps > examples/shop/app-go/steps/steps_gen.go
$ qoqi gen php examples/shop/orders.qi --namespace 'App\Flow' --out examples/shop/app-php/src/Flow
$ qoqi gen ts examples/shop/orders.qi --from ../../../sdk/ts/src/index.ts > examples/shop/app-ts/steps.ts
```

The PHP is PSR-12, and what PHP's types cannot say, generics among them,
is said in PHPStan's annotations: the application, the classes written
for it and the library pass `phpstan analyse --level=max`.
