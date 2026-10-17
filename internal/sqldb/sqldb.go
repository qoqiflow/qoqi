// Package sqldb opens a database by its address, and says how each kind
// writes what the others write differently.
//
// An address is a file of SQLite (a path, or sqlite:path), or a server:
//
//	flows.db
//	postgres://qoqi:secret@db.internal:5432/flows
//	mysql://qoqi:secret@db.internal:3306/flows
//
// The store of the flows and std.sql both open what they are given here.
package sqldb

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Kind is the kind of database an address names.
type Kind int

const (
	SQLite Kind = iota
	Postgres
	MySQL
)

func (k Kind) String() string {
	switch k {
	case Postgres:
		return "postgres"
	case MySQL:
		return "mysql"
	}
	return "sqlite"
}

// KindOf is the kind of database an address names: a server by its
// scheme, a file otherwise.
func KindOf(address string) Kind {
	switch {
	case strings.HasPrefix(address, "postgres://"), strings.HasPrefix(address, "postgresql://"):
		return Postgres
	case strings.HasPrefix(address, "mysql://"):
		return MySQL
	}
	return SQLite
}

// IsServer says whether an address is a server's rather than a file's.
func IsServer(address string) bool { return KindOf(address) != SQLite }

// Open opens the database at an address. A file of SQLite has one
// connection: one writer at a time is what SQLite is, and one connection
// keeps it so for the program too.
func Open(address string) (*sql.DB, Kind, error) {
	return open(address, false)
}

// OpenShared opens the database at an address for writers that hold a
// transaction a while, as the store of flows does for a step that writes
// in it: on SQLite, a few connections, each transaction taking the
// writer as it begins, and the others reading meanwhile.
func OpenShared(address string) (*sql.DB, Kind, error) {
	return open(address, true)
}

func open(address string, shared bool) (*sql.DB, Kind, error) {
	kind := KindOf(address)
	var db *sql.DB
	var err error
	switch kind {
	case Postgres:
		db, err = sql.Open("pgx", address)
	case MySQL:
		var dsn string
		if dsn, err = mysqlDSN(address); err == nil {
			db, err = sql.Open("mysql", dsn)
		}
	default:
		path := strings.TrimPrefix(address, "sqlite:")
		options := "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
		if shared {
			options += "&_txlock=immediate"
		}
		db, err = sql.Open("sqlite", path+options)
		if err == nil && !shared {
			db.SetMaxOpenConns(1)
		} else if err == nil {
			db.SetMaxOpenConns(4)
		}
	}
	if err != nil {
		return nil, kind, fmt.Errorf("cannot open %s: %v", Redacted(address), err)
	}
	if kind != SQLite {
		if err := db.Ping(); err != nil {
			db.Close()
			return nil, kind, fmt.Errorf("cannot reach %s: %v", Redacted(address), err)
		}
	}
	return db, kind, nil
}

// mysqlDSN is the address the driver of MySQL takes, from a URL: what
// the URL's query says is passed on (tls=true, timeout=5s...).
func mysqlDSN(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil {
		return "", err
	}
	cfg := mysql.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = u.Host
	if u.Port() == "" {
		cfg.Addr = u.Host + ":3306"
	}
	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Passwd, _ = u.User.Password()
	}
	cfg.DBName = strings.TrimPrefix(u.Path, "/")
	// Texts compared as they are written, in every connection
	cfg.Collation = "utf8mb4_bin"
	dsn := cfg.FormatDSN()
	if u.RawQuery != "" {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		dsn += sep + u.RawQuery
	}
	if _, err := mysql.ParseDSN(dsn); err != nil {
		return "", err
	}
	return dsn, nil
}

// Redacted is an address as it may be shown: without its password.
func Redacted(address string) string {
	if !IsServer(address) {
		return address
	}
	u, err := url.Parse(address)
	if err != nil || u.User == nil {
		return address
	}
	if _, has := u.User.Password(); has {
		u.User = url.UserPassword(u.User.Username(), "xxxxx")
	}
	return u.String()
}

// Fingerprint names an address in a few letters that do not change
// with its password, for a file kept about it.
func Fingerprint(address string) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(Redacted(address), "xxxxx", "")))
	return hex.EncodeToString(sum[:6])
}

// Rebind writes a query's parameters as the database does: ?, as SQLite
// and MySQL do, or $1, $2, as Postgres does. A ? inside quotes is text,
// and left as it is.
func (k Kind) Rebind(query string) string {
	if k != Postgres || !strings.Contains(query, "?") {
		return query
	}
	var out strings.Builder
	n := 0
	var quote rune
	for _, r := range query {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '"':
			quote = r
		case r == '?':
			n++
			fmt.Fprintf(&out, "$%d", n)
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// Retry says whether a transaction failed only because another ran at
// the same time, and is to be run again: a serialization failure or a
// deadlock, which a server ends one of the two with.
func (k Kind) Retry(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "40001" || pg.Code == "40P01"
	}
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		return my.Number == 1213 || my.Number == 1205
	}
	// SQLite's writer taken longer than it waits for it
	var lite interface{ Code() int }
	if errors.As(err, &lite) {
		return lite.Code()&0xff == 5 || lite.Code()&0xff == 6
	}
	return false
}
