// Package clock provides a logical clock so trials never depend on wall time.
package clock

// Clock is a monotonic tick counter. Timeouts advance it instead of sleeping.
type Clock struct {
	Tick int64
}

func New() *Clock { return &Clock{} }

func (c *Clock) Now() int64 { return c.Tick }

func (c *Clock) Advance(n int64) int64 {
	if n < 1 {
		n = 1
	}
	c.Tick += n
	return c.Tick
}
