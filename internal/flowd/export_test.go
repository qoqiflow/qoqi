package flowd

import "time"

// What the tests of the package see of the throttle of sign ins.

const ThrottleNames = throttleNames

func (d *Daemon) FailLogin(name string, now time.Time) { d.logins.failed(name, now) }

func (d *Daemon) Throttled() int {
	d.logins.mu.Lock()
	defer d.logins.mu.Unlock()
	return len(d.logins.fails)
}
