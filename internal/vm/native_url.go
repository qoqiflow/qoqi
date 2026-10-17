package vm

import (
	"net/url"
	"strconv"
	"strings"
)

// The parts std.url builds a Url from, in this order. What is not there is
// "": the library makes null of it.
func urlParts(u *url.URL) []string {
	user := ""
	if u.User != nil {
		user = u.User.Username()
	}
	return []string{"", u.Scheme, user, u.Hostname(), u.Port(), u.Path, u.RawQuery, u.Fragment}
}

// urlFailure is the parts of a text that is no address: the reason first,
// without Go's `parse "..."` in front of it, which the library says its way.
func urlFailure(err error) []string {
	reason := err.Error()
	if e, ok := err.(*url.Error); ok {
		reason = e.Err.Error()
	}
	return []string{reason, "", "", "", "", "", "", ""}
}

func init() {
	registerNatives("std.url",
		native{"split", 1, func(vm *VM, args []Value) (Value, error) {
			text, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			u, err := url.Parse(strings.TrimSpace(text))
			if err != nil {
				return vm.newStringArray(urlFailure(err))
			}
			if u.Port() != "" {
				if _, err := strconv.Atoi(u.Port()); err != nil {
					return vm.newStringArray([]string{"invalid port \"" + u.Port() + "\"", "", "", "", "", "", "", ""})
				}
			}
			return vm.newStringArray(urlParts(u))
		}},
		// An address against another: a link found on a page, against the
		// address of the page
		native{"against", 2, func(vm *VM, args []Value) (Value, error) {
			base, err := argString(args, 0)
			if err != nil {
				return NewNull(), err
			}
			ref, err := argString(args, 1)
			if err != nil {
				return NewNull(), err
			}
			b, err := url.Parse(base)
			if err != nil {
				return vm.newStringArray(urlFailure(err))
			}
			r, err := url.Parse(strings.TrimSpace(ref))
			if err != nil {
				return vm.newStringArray(urlFailure(err))
			}
			return vm.newStringArray(urlParts(b.ResolveReference(r)))
		}},
		// The text of an address from its parts; port 0 for none
		native{"join", 7, func(vm *VM, args []Value) (Value, error) {
			parts := make([]string, 7)
			for _, i := range []int{0, 1, 2, 4, 5, 6} {
				s, err := argString(args, i)
				if err != nil {
					return NewNull(), err
				}
				parts[i] = s
			}
			port, err := argInt(args, 3)
			if err != nil {
				return NewNull(), err
			}
			u := url.URL{Scheme: parts[0], Host: parts[2], Path: parts[4], RawQuery: parts[5], Fragment: parts[6]}
			if parts[1] != "" {
				u.User = url.User(parts[1])
			}
			if port > 0 {
				u.Host += ":" + strconv.FormatInt(port, 10)
			}
			return vm.newString(u.String())
		}},
	)
}
