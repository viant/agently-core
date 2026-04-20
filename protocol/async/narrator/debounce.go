package narrator

import "time"

type Debouncer struct {
	window  time.Duration
	timer   *time.Timer
	pending string
	active  bool
}

func NewDebouncer(window time.Duration) *Debouncer {
	return &Debouncer{window: window}
}

func (d *Debouncer) Push(text string) string {
	if d == nil {
		return text
	}
	if text == "" {
		return ""
	}
	if d.window <= 0 {
		return text
	}
	if !d.active {
		d.timer = time.NewTimer(d.window)
		d.active = true
		d.pending = text
		return ""
	}
	d.pending = text
	return ""
}

func (d *Debouncer) Channel() <-chan time.Time {
	if d == nil || !d.active || d.timer == nil {
		return nil
	}
	return d.timer.C
}

func (d *Debouncer) Flush() string {
	if d == nil {
		return ""
	}
	text := d.pending
	d.pending = ""
	if d.timer != nil {
		if !d.timer.Stop() {
			select {
			case <-d.timer.C:
			default:
			}
		}
		d.timer = nil
	}
	d.active = false
	return text
}
