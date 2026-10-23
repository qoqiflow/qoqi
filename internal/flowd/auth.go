package flowd

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Who may ask the daemon: a program with its token, as the qoqi commands
// and the SDKs are, or a person signed in to the console, whose browser
// holds a session in a cookie. The console starts with one user, admin,
// password admin, which is changed at the first sign in before anything
// else; then people are added by a person signed in, or by qoqi users. A
// daemon without a Keep has no people: the token alone.

const (
	sessionCookie = "qoqi_session"
	sessionLife   = 14 * 24 * time.Hour
	// A request of the console that changes something says it is one, in
	// a header a page of another site cannot send without asking first
	consoleHeader = "X-Qoqi-Console"
	hashRounds    = 210_000
	// What a request about people carries is small: a name and passwords
	authBodyLimit = 4 << 10
	// The names the throttle keeps, at most: beyond, the oldest go
	throttleNames = 10_000
)

// Principal is who asks: a user by name, or a holder of the token.
type Principal struct {
	User  string `json:"user,omitempty"`
	Token bool   `json:"token,omitempty"`
	// Renew says the user's password is one given them: until they change
	// it, it is all they may do
	Renew bool `json:"renew,omitempty"`
	// Operator says the user works with instances only, not with code
	// or people
	Operator bool `json:"operator,omitempty"`
}

type principalKey struct{}

func principalOf(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey{}).(Principal)
	return p
}

// HashPassword is what is kept of a password: pbkdf2 of sha512, salted.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha512.New, password, salt, hashRounds, 32)
	if err != nil {
		return "", err
	}
	enc := base64.RawStdEncoding
	return fmt.Sprintf("pbkdf2-sha512$%d$%s$%s", hashRounds, enc.EncodeToString(salt), enc.EncodeToString(key)), nil
}

// CheckPassword says whether a password is the one a hash was made of.
func CheckPassword(hash, password string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha512" {
		return false
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < 1 {
		return false
	}
	enc := base64.RawStdEncoding
	salt, err1 := enc.DecodeString(parts[2])
	want, err2 := enc.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha512.New, password, salt, rounds, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// CheckUserName says what is wrong with a name for a user, if anything.
func CheckUserName(name string) error {
	if name == "" || len(name) > 64 {
		return errors.New("a name has 1 to 64 characters")
	}
	for _, c := range name {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || strings.ContainsRune("._-@", c)) {
			return fmt.Errorf("a name is made of letters, digits and . _ - @, not %q", c)
		}
	}
	return nil
}

// CheckPasswordStrength says what is wrong with a password, if anything.
func CheckPasswordStrength(password string) error {
	if len([]rune(password)) < 10 {
		return errors.New("a password has 10 characters at least")
	}
	return nil
}

// sessionID is what is kept of a session: the hash of what the cookie
// holds, so that the table, read, signs no one in.
func sessionID(cookie string) string {
	sum := sha256.Sum256([]byte(cookie))
	return hex.EncodeToString(sum[:])
}

// throttle slows down whoever guesses passwords: a name that failed five
// times in a quarter of an hour is refused until it passes. It keeps
// throttleNames names at most, so that guesses of many names do not fill
// the memory.
type throttle struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

const throttleWindow = 15 * time.Minute

func (t *throttle) blocked(name string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	var recent []time.Time
	for _, at := range t.fails[name] {
		if now.Sub(at) < throttleWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) == 0 {
		delete(t.fails, name)
		return false
	}
	t.fails[name] = recent
	return len(recent) >= 5
}

func (t *throttle) failed(name string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fails == nil {
		t.fails = map[string][]time.Time{}
	}
	if _, kept := t.fails[name]; !kept && len(t.fails) >= throttleNames {
		t.forget(now)
	}
	t.fails[name] = append(t.fails[name], now)
}

// forget makes room: the names whose failures are past go, and if none
// is, the one whose last failure is the oldest.
func (t *throttle) forget(now time.Time) {
	oldest, at := "", now
	for name, fails := range t.fails {
		last := fails[len(fails)-1]
		if now.Sub(last) >= throttleWindow {
			delete(t.fails, name)
		} else if last.Before(at) {
			oldest, at = name, last
		}
	}
	if len(t.fails) >= throttleNames {
		delete(t.fails, oldest)
	}
}

// decodeBody reads a small JSON body, of limit bytes at most.
func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, into any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(into)
}

// absent is the hash a sign in under a name nobody has is checked
// against, so that it takes as long as one under a name somebody has.
var absent = sync.OnceValue(func() string {
	hash, _ := HashPassword("")
	return hash
})

func (t *throttle) passed(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.fails, name)
}

// authenticate is who a request is from, if anyone: the token in its
// header, or a session in its cookie. A request of a session that
// changes something carries the console's header besides.
func (d *Daemon) authenticate(r *http.Request) (Principal, string) {
	if header := r.Header.Get("Authorization"); header != "" {
		given := strings.TrimPrefix(header, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(given), []byte(d.Token)) == 1 {
			return Principal{Token: true}, ""
		}
		return Principal{}, "every request carries the daemon's token: Authorization: Bearer <token>"
	}
	if d.Keep != nil {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			s, ok := d.Keep.Session(sessionID(c.Value), time.Now().UnixMilli())
			if !ok {
				return Principal{}, "the session has ended: sign in again"
			}
			if r.Method != http.MethodGet && r.Header.Get(consoleHeader) == "" {
				return Principal{}, "a request of the console carries " + consoleHeader
			}
			return Principal{User: s.User, Renew: s.Renew, Operator: s.Operator}, ""
		}
		return Principal{}, "sign in to the console, or give the daemon's token: Authorization: Bearer <token>"
	}
	return Principal{}, "every request carries the daemon's token: Authorization: Bearer <token>"
}

func withPrincipal(r *http.Request, p Principal) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), principalKey{}, p))
}

// AuthState is what the console is told before anyone signs in.
type AuthState struct {
	Accounts bool       `json:"accounts"`        // whether the daemon keeps people at all
	Me       *Principal `json:"me,omitempty"`    // who the request is from, if signed in
	Prefs    any        `json:"prefs,omitempty"` // their choices of the pages
}

func (d *Daemon) authState(w http.ResponseWriter, r *http.Request) {
	state := AuthState{Accounts: d.Keep != nil}
	if p, problem := d.authenticate(r); problem == "" {
		state.Me = &p
		if p.User != "" {
			if u, err := d.Keep.User(p.User); err == nil && u.Prefs != "" {
				state.Prefs = json.RawMessage(u.Prefs)
			}
		}
	}
	answer(w, http.StatusOK, state)
}

// startSession signs a person in: a cookie, and its session kept.
func (d *Daemon) startSession(w http.ResponseWriter, r *http.Request, user string) error {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	if err := d.Keep.PutSession(Session{ID: sessionID(value), User: user, Created: now.UnixMilli(), Expires: now.Add(sessionLife).UnixMilli()}); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: secure(r), Expires: now.Add(sessionLife), MaxAge: int(sessionLife.Seconds()),
	})
	return nil
}

// secure says whether the browser reached the daemon over TLS, itself or
// through a proxy that says so: the cookie is then sent over TLS alone.
func secure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (d *Daemon) noAccounts(w http.ResponseWriter) bool {
	if d.Keep == nil {
		fail(w, http.StatusNotFound, "accounts", "this daemon keeps no users: it answers to its token")
		return true
	}
	return false
}

type credentials struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// addUser adds a person, with a password given them: with renew, theirs
// to change at their first sign in.
func (d *Daemon) addUser(w http.ResponseWriter, name, password string, renew, operator bool) bool {
	if err := CheckUserName(name); err != nil {
		fail(w, http.StatusBadRequest, "name", err.Error())
		return false
	}
	if err := CheckPasswordStrength(password); err != nil {
		fail(w, http.StatusBadRequest, "password", err.Error())
		return false
	}
	hash, err := HashPassword(password)
	if err != nil {
		fail(w, http.StatusInternalServerError, "password", err.Error())
		return false
	}
	if err := d.Keep.AddUser(User{Name: name, Hash: hash, Created: time.Now().UnixMilli(), Renew: renew, Operator: operator}); err != nil {
		fail(w, http.StatusConflict, "name", err.Error())
		return false
	}
	return true
}

// login signs a person in, by their name and password.
func (d *Daemon) login(w http.ResponseWriter, r *http.Request) {
	if d.noAccounts(w) {
		return
	}
	var c credentials
	if err := decodeBody(w, r, authBodyLimit, &c); err != nil {
		fail(w, http.StatusBadRequest, "body", "the body is JSON: {\"name\": ..., \"password\": ...}, of 4 KB at most")
		return
	}
	if CheckUserName(c.Name) != nil {
		// No user has such a name: nothing to throttle, nothing to keep
		CheckPassword(absent(), c.Password)
		fail(w, http.StatusUnauthorized, "login", "this name and this password do not go together")
		return
	}
	now := time.Now()
	if d.logins.blocked(c.Name, now) {
		fail(w, http.StatusTooManyRequests, "login", "too many tries for "+c.Name+": wait a quarter of an hour")
		return
	}
	u, err := d.Keep.User(c.Name)
	if err != nil {
		// As long as a wrong password, so that the time does not say who is there
		u.Hash = absent()
	}
	if !CheckPassword(u.Hash, c.Password) || err != nil {
		d.logins.failed(c.Name, now)
		fail(w, http.StatusUnauthorized, "login", "this name and this password do not go together")
		return
	}
	d.logins.passed(c.Name)
	if err := d.startSession(w, r, u.Name); err != nil {
		fail(w, http.StatusInternalServerError, "session", err.Error())
		return
	}
	answer(w, http.StatusOK, Principal{User: u.Name, Renew: u.Renew, Operator: u.Operator})
}

func (d *Daemon) logout(w http.ResponseWriter, r *http.Request) {
	if d.Keep != nil {
		if c, err := r.Cookie(sessionCookie); err == nil {
			_ = d.Keep.EndSession(sessionID(c.Value), time.Now().UnixMilli())
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secure(r), MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (d *Daemon) listUsers(w http.ResponseWriter, r *http.Request) {
	if d.noAccounts(w) {
		return
	}
	users, err := d.Keep.Users()
	if err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	if users == nil {
		users = []User{}
	}
	answer(w, http.StatusOK, users)
}

func (d *Daemon) createUser(w http.ResponseWriter, r *http.Request) {
	if d.noAccounts(w) {
		return
	}
	var c struct {
		credentials
		// Admin makes one who deploys and changes people: a user added is
		// an operator otherwise
		Admin bool `json:"admin"`
	}
	if err := decodeBody(w, r, authBodyLimit, &c); err != nil {
		fail(w, http.StatusBadRequest, "body", "the body is JSON: {\"name\": ..., \"password\": ..., \"admin\": false}, of 4 KB at most")
		return
	}
	d.accountsMu.Lock()
	defer d.accountsMu.Unlock()
	if d.addUser(w, c.Name, c.Password, true, !c.Admin) {
		answer(w, http.StatusCreated, User{Name: c.Name, Created: time.Now().UnixMilli(), Renew: true, Operator: !c.Admin})
	}
}

func (d *Daemon) removeUser(w http.ResponseWriter, r *http.Request) {
	if d.noAccounts(w) {
		return
	}
	name := r.PathValue("name")
	if principalOf(r).User == name {
		fail(w, http.StatusConflict, "user", "a user does not take themselves away: another does it")
		return
	}
	d.accountsMu.Lock()
	defer d.accountsMu.Unlock()
	if n, _ := d.Keep.CountUsers(); n <= 1 {
		fail(w, http.StatusConflict, "user", "the last user stays: the console would have no one")
		return
	}
	if err := d.Keep.RemoveUser(name); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrNoUser) {
			status = http.StatusNotFound
		}
		fail(w, status, "user", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// password gives a user another password: their own, given the one they
// have, or another's. Their sessions end, this one too.
func (d *Daemon) password(w http.ResponseWriter, r *http.Request) {
	if d.noAccounts(w) {
		return
	}
	name := r.PathValue("name")
	var asked struct {
		Password string `json:"password"`
		Current  string `json:"current"`
	}
	if err := decodeBody(w, r, authBodyLimit, &asked); err != nil {
		fail(w, http.StatusBadRequest, "body", "the body is JSON: {\"password\": ..., \"current\": ...}, of 4 KB at most")
		return
	}
	own := principalOf(r).User == name
	if !own && !d.mayAdminister(w, r) {
		return
	}
	u, err := d.Keep.User(name)
	if err != nil {
		fail(w, http.StatusNotFound, "user", "there is no user "+name)
		return
	}
	if own && !CheckPassword(u.Hash, asked.Current) {
		fail(w, http.StatusUnauthorized, "password", "the current password is not this one")
		return
	}
	if err := CheckPasswordStrength(asked.Password); err != nil {
		fail(w, http.StatusBadRequest, "password", err.Error())
		return
	}
	if own && asked.Password == asked.Current {
		fail(w, http.StatusBadRequest, "password", "the new password is the one there was")
		return
	}
	hash, err := HashPassword(asked.Password)
	if err == nil {
		// A password given by another is theirs to change at their next sign in
		err = d.Keep.SetPassword(name, hash, !own)
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "password", err.Error())
		return
	}
	if own {
		// Signed in again, under the new password
		_ = d.startSession(w, r, name)
	}
	w.WriteHeader(http.StatusNoContent)
}

// prefs keeps the choices of the pages of the person signed in.
func (d *Daemon) prefs(w http.ResponseWriter, r *http.Request) {
	user := principalOf(r).User
	if d.Keep == nil || user == "" {
		fail(w, http.StatusNotFound, "prefs", "choices are kept for a user signed in")
		return
	}
	var prefs json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&prefs); err != nil {
		fail(w, http.StatusBadRequest, "body", "the choices are a JSON object, of 16 KB at most")
		return
	}
	if err := d.Keep.SetPrefs(user, string(prefs)); err != nil {
		fail(w, http.StatusInternalServerError, "keep", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// mayAdminister says whether who asks deploys and changes people: the
// token, or a user who is not an operator. Refused, the request is
// answered.
func (d *Daemon) mayAdminister(w http.ResponseWriter, r *http.Request) bool {
	if principalOf(r).Operator {
		fail(w, http.StatusForbidden, "role", "an operator works with instances: an admin deploys code and changes people")
		return false
	}
	return true
}

// admin is a handler for admins alone.
func (d *Daemon) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.mayAdminister(w, r) {
			h(w, r)
		}
	}
}
