package vm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The server of std.http is split in two. Go accepts the connections and
// reads the requests, each in its own goroutine, and queues them; the
// program takes them one at a time (accept), works out the answers, in a
// task each if it wants to work on several at once, and hands each back
// under the number of its request (respondTo).
//
// Closing (shut) is graceful: nothing more is taken, a request still
// waiting to be taken is answered 503, and the requests the program has
// taken are answered by it before the server goes, for closeWithin at
// most. What is still unanswered then is answered 503.

// exchange is one request waiting for its answer.
type exchange struct {
	request []string
	answer  chan answer
}

type answer struct {
	status  int
	headers []string // name, value, name, value, ...
	body    string
}

type httpServer struct {
	server   *http.Server
	port     int
	waiting  chan *exchange
	closed   chan struct{}       // closed by shut: nothing more is taken
	abandon  chan struct{}       // closed when shut stops waiting for answers
	accepted map[int64]*exchange // by number, until answered
	last     int64
	isClosed bool
}

// maxBody is the most a request or a response body may weigh.
const maxBody = 32 << 20

// closeWithin is how long closing a server waits for the program to
// answer the requests it has taken.
const closeWithin = 10 * time.Second

func (s *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		plainText(w, http.StatusRequestEntityTooLarge, "the request is too large")
		return
	}
	request := []string{r.Method, r.URL.Path, r.URL.RawQuery, string(body), r.RemoteAddr}
	request = append(request, headerPairs(r.Header)...)

	waiting := &exchange{request: request, answer: make(chan answer, 1)}
	select {
	case s.waiting <- waiting:
	case <-s.closed:
		plainText(w, http.StatusServiceUnavailable, closing)
		return
	}
	// Taken: the program answers it, closing or not, unless closing has
	// waited long enough
	select {
	case reply := <-waiting.answer:
		for i := 0; i+1 < len(reply.headers); i += 2 {
			w.Header().Set(reply.headers[i], reply.headers[i+1])
		}
		w.WriteHeader(reply.status)
		io.WriteString(w, reply.body)
	case <-s.abandon:
		plainText(w, http.StatusServiceUnavailable, closing)
	}
}

// closing is the answer of a server that is closing.
const closing = "the server is closing"

// plainText answers with a status and a text, as it is: http.Error would
// add a line end to it.
func plainText(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	io.WriteString(w, text)
}

// headerPairs lists headers as name, value, name, value, with the names
// in lower case and sorted, and the values of a repeated header joined.
func headerPairs(header http.Header) []string {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	pairs := make([]string, 0, 2*len(names))
	for _, name := range names {
		pairs = append(pairs, strings.ToLower(name), strings.Join(header[name], ", "))
	}
	return pairs
}

// kindOfNetworkError names why a request got no answer, for std.http to
// turn into its ErrorKind.
func kindOfNetworkError(err error) string {
	var dns *net.DNSError
	var network net.Error
	var address *url.Error
	switch {
	case errors.As(err, &dns):
		return "unknown host"
	case errors.As(err, &network) && network.Timeout():
		return "timeout"
	case strings.Contains(err.Error(), "connection refused"):
		return "refused"
	case errors.As(err, &address) && address.Op == "parse":
		return "bad url"
	}
	return "other"
}

func (vm *VM) httpServerOf(args []Value) (*httpServer, error) {
	id, err := argInt(args, 0)
	if err != nil {
		return nil, err
	}
	server, ok := vm.servers[id]
	if !ok {
		return nil, fmt.Errorf("there is no server %d", id)
	}
	return server, nil
}

func init() {
	registerNatives("std.http",
		// Sends a request and waits for the answer: the status, the body,
		// then the headers as name, value, name, value. Null if there was
		// no answer, the reason being kept.
		native{"exchange", 5, func(vm *VM, args []Value) (Value, error) {
			method, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			address, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			headers, err := argStrings(args, 2)
			if err != nil {
				return NewNull(), err
			}
			body, err := argString(args, 3)
			if err != nil {
				return NewNull(), err
			}
			timeout, err := argInt(args, 4)
			if err != nil {
				return NewNull(), err
			}

			parsed, err := url.Parse(address)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return vm.refused("bad url", "an address starts with http:// or https:// and names a host")
			}
			request, err := http.NewRequest(method, address, strings.NewReader(body))
			if err != nil {
				return vm.refused("bad url", err.Error())
			}
			for i := 0; i+1 < len(headers); i += 2 {
				request.Header.Set(headers[i], headers[i+1])
			}

			// Other tasks go on while the answer is on its way
			client := &http.Client{Timeout: time.Duration(timeout) * time.Millisecond}
			var parts []string
			var kind, reason string
			gone := vm.blocking(func() {
				response, err := client.Do(request)
				if err != nil {
					var wrapped *url.Error
					kind, reason = kindOfNetworkError(err), err.Error()
					if errors.As(err, &wrapped) {
						reason = wrapped.Err.Error()
					}
					return
				}
				defer response.Body.Close()
				content, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
				if err != nil {
					kind, reason = kindOfNetworkError(err), err.Error()
					return
				}
				parts = append([]string{strconv.Itoa(response.StatusCode), string(content)}, headerPairs(response.Header)...)
			})
			if gone != nil {
				return NewNull(), gone
			}
			if parts == nil {
				return vm.refused(kind, reason)
			}
			return vm.newStringArray(parts)
		}},

		// Starts listening on a port of an address of this machine, an IP
		// or a name: 127.0.0.1 for this machine only, 0.0.0.0 or :: for
		// every interface. Port 0 lets the system choose one. Gives the
		// server's number, its port and the IP it listens on, or null.
		native{"open", 2, func(vm *VM, args []Value) (Value, error) {
			port, err := argInt(args, 0)
			if err != nil {
				return NewNull(), err
			}
			host, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			// An empty host would be every interface to Go: never by mistake
			if reason := badHost(host); reason != "" {
				return vm.refused("bad url", reason)
			}
			// An IP listens on its own network: 0.0.0.0 is IPv4 and says so,
			// where Go would take it for every interface of both
			network := "tcp"
			if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
				network = "tcp4"
			} else if ip != nil {
				network = "tcp6"
			}
			listener, err := net.Listen(network, net.JoinHostPort(host, strconv.FormatInt(port, 10)))
			if err != nil {
				if strings.Contains(err.Error(), "address already in use") {
					return vm.refused("in use", "the port is taken")
				}
				var notFound *net.DNSError
				if errors.As(err, &notFound) {
					return vm.refused("unknown host", fmt.Sprintf("no machine is called %q", host))
				}
				var wrapped *net.OpError
				reason := err.Error()
				if errors.As(err, &wrapped) {
					reason = wrapped.Err.Error()
				}
				return vm.refused("other", reason)
			}
			address := listener.Addr().(*net.TCPAddr)
			server := &httpServer{
				port:     address.Port,
				waiting:  make(chan *exchange),
				accepted: map[int64]*exchange{},
				closed:   make(chan struct{}),
				abandon:  make(chan struct{}),
			}
			server.server = &http.Server{Handler: server}
			go server.server.Serve(listener)

			if vm.servers == nil {
				vm.servers = map[int64]*httpServer{}
			}
			vm.lastServer++
			vm.servers[vm.lastServer] = server
			return vm.newStringArray([]string{strconv.FormatInt(vm.lastServer, 10), strconv.Itoa(server.port), address.IP.String()})
		}},
		// The next request: its number, its method, path, query, body and
		// sender, then its headers. Waits for one, at most `timeout`
		// milliseconds if that is not 0, while other tasks go on. Null if
		// none came, or the server is closed.
		native{"accept", 2, func(vm *VM, args []Value) (Value, error) {
			server, err := vm.httpServerOf(args)
			if err != nil {
				return NewNull(), err
			}
			timeout, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			var limit <-chan time.Time
			if timeout > 0 {
				limit = time.After(time.Duration(timeout) * time.Millisecond)
			}
			var next *exchange
			quit := vm.sched.quit
			if gone := vm.blocking(func() {
				select {
				case next = <-server.waiting:
				case <-limit:
				case <-server.closed:
				case <-quit:
				}
			}); gone != nil {
				return NewNull(), gone
			}
			if next == nil {
				return NewNull(), nil
			}
			if server.isClosed {
				// Closed while this one came in: it is not taken
				next.answer <- answer{status: http.StatusServiceUnavailable, headers: []string{"Content-Type", "text/plain; charset=utf-8"}, body: closing}
				return NewNull(), nil
			}
			server.last++
			server.accepted[server.last] = next
			return vm.newStringArray(append([]string{strconv.FormatInt(server.last, 10)}, next.request...))
		}},
		// Answers a request accept gave, named by its number
		native{"respondTo", 5, func(vm *VM, args []Value) (Value, error) {
			server, err := vm.httpServerOf(args)
			if err != nil {
				return NewNull(), err
			}
			request, err := argInt(args, 1)
			if err != nil {
				return NewNull(), err
			}
			status, err := argInt(args, 2)
			if err != nil {
				return NewNull(), err
			}
			headers, err := argStrings(args, 3)
			if err != nil {
				return NewNull(), err
			}
			body, err := argString(args, 4)
			if err != nil {
				return NewNull(), err
			}
			asked, ok := server.accepted[request]
			if !ok {
				return NewNull(), errors.New("there is no request to answer, or it has been answered already")
			}
			if status < 100 || status > 599 {
				return NewNull(), fmt.Errorf("%d is not a status a server can answer with", status)
			}
			delete(server.accepted, request)
			asked.answer <- answer{status: int(status), headers: headers, body: body}
			return NewNull(), nil
		}},
		native{"shut", 1, func(vm *VM, args []Value) (Value, error) {
			server, err := vm.httpServerOf(args)
			if err != nil {
				return NewNull(), err
			}
			if server.isClosed {
				return NewNull(), nil
			}
			server.isClosed = true
			close(server.closed)

			// The tasks answering the requests taken go on meanwhile;
			// Shutdown returns once every answer has reached its sender
			quit := vm.sched.quit
			gone := vm.blocking(func() {
				done := make(chan struct{})
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), closeWithin)
					defer cancel()
					if server.server.Shutdown(ctx) != nil {
						// Waited long enough: what is left is answered 503
						close(server.abandon)
						ctx, cancel := context.WithTimeout(context.Background(), time.Second)
						defer cancel()
						server.server.Shutdown(ctx)
					}
					close(done)
				}()
				select {
				case <-done:
				case <-quit:
				}
			})
			server.server.Close()
			return NewNull(), gone
		}},
		// The type of a file's content, from its extension: "text/css;
		// charset=utf-8" for a.css
		native{"contentType", 1, func(vm *VM, args []Value) (Value, error) {
			name, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			if kind := mime.TypeByExtension(filepath.Ext(name)); kind != "" {
				return vm.newString(kind)
			}
			return vm.newString("application/octet-stream")
		}},
		problemNative,
	)
}

// badHost says why a host cannot be listened on, or "" if it may be: an
// IP, or a name of letters, digits, dots, hyphens and underscores.
func badHost(host string) string {
	if host == "" {
		return "the host is empty: 127.0.0.1 is this machine only, 0.0.0.0 every interface"
	}
	if net.ParseIP(host) != nil {
		return ""
	}
	for _, c := range host {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_'
		if !ok {
			return fmt.Sprintf("%q is not a host: an IP such as 0.0.0.0 or a name is", host)
		}
	}
	return ""
}
