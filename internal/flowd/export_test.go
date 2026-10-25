package flowd

import (
	"time"

	"github.com/qoqiflow/qoqi/internal/flowrt"
)

// What the tests of the package see of the throttle of sign ins.

const ThrottleNames = throttleNames

func (d *Daemon) FailLogin(name string, now time.Time) { d.logins.failed(name, now) }

func (d *Daemon) Throttled() int {
	d.logins.mu.Lock()
	defer d.logins.mu.Unlock()
	return len(d.logins.fails)
}

// What the tests see of the looks at the instances.

func (d *Daemon) Look(p Program) []*flowrt.Instance { return d.look(p) }

// AgeLooks makes the last look at every instance of each program that
// much older, as if that time had passed.
func (d *Daemon) AgeLooks(by time.Duration) {
	for name, l := range d.looks {
		l.all = l.all.Add(-by)
		d.looks[name] = l
	}
}
