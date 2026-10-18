package flowd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The home of qoqi on a machine: QOQI_PATH, ~/.qoqi when it is not set.
// The daemon keeps all of its files there, and the commands find it by
// them:
//
//	flows.db      the store, unless QOQI_STORE or --store names another
//	daemon.json   where the daemon listens, its token and its process
//	token         the token of the daemon, readable by its user alone
//	logs/         what the daemon says, and what its flows print

// Home is the folder of qoqi: QOQI_PATH, or .qoqi in the user's folder.
func Home() string {
	if home := strings.TrimSpace(os.Getenv("QOQI_PATH")); home != "" {
		return home
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, ".qoqi")
}

// MakeHome makes the folder of qoqi and its logs, for its user alone.
func MakeHome(home string) error {
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o700); err != nil {
		return fmt.Errorf("cannot make %s, the folder of qoqi (QOQI_PATH): %v", home, err)
	}
	return nil
}

// HomeStore is the store of a home: QOQI_STORE, or flows.db in it.
func HomeStore(home string) string {
	if store := strings.TrimSpace(os.Getenv("QOQI_STORE")); store != "" {
		return store
	}
	return filepath.Join(home, "flows.db")
}

// LogFile is a file of the logs of a home.
func LogFile(home, name string) string { return filepath.Join(home, "logs", name) }

// TokenFile is where the daemon of a home keeps its token: a daemon
// started again has the same, so that what talks to it goes on as it was.
func TokenFile(home string) string { return filepath.Join(home, "token") }

// HomeToken is the token of the daemon of a home: the one kept there, or
// one drawn now and kept.
func HomeToken(home string) (string, error) {
	data, err := os.ReadFile(TokenFile(home))
	if err == nil {
		if token := strings.TrimSpace(string(data)); len(token) >= 32 {
			return token, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token := NewToken()
	if err := os.WriteFile(TokenFile(home), []byte(token+"\n"), 0o600); err != nil {
		return "", err
	}
	return token, nil
}

// Address is what the daemon writes in its home for the commands: where
// it listens and its token, readable by its user alone.
type Address struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
	Store string `json:"store,omitempty"` // as it is shown, without its password
}

// AddressFile is where the daemon of a home says where it is.
func AddressFile(home string) string { return filepath.Join(home, "daemon.json") }

func WriteAddress(home string, a Address) error {
	data, _ := json.MarshalIndent(a, "", "  ")
	return os.WriteFile(AddressFile(home), data, 0o600)
}

func ReadAddress(home string) (Address, error) {
	var a Address
	data, err := os.ReadFile(AddressFile(home))
	if errors.Is(err, os.ErrNotExist) {
		return a, fmt.Errorf("no daemon runs from %s: start it with qoqi serve, or name one with --url (QOQI_URL) and --token (QOQI_TOKEN)", home)
	}
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(data, &a)
	return a, err
}
