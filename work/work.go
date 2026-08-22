// Package work provides task-grouped thread pools: separate worker groups
// for deleting emojis, creating channels, banning, etc., so one slow task
// type never starves another.
package work

import (
	"sync"
)

// Group is one named pool of workers pulling closures off a queue.
type Group struct {
	Name string
	jobs chan func()
	wg   sync.WaitGroup
}

// Dispatcher owns all groups.
type Dispatcher struct {
	Groups map[string]*Group
}

// New builds a dispatcher splitting total threads across named groups.
// Weight maps group -> relative weight; nil weights = even split.
func New(total int, order []string, weights map[string]int) *Dispatcher {
	d := &Dispatcher{Groups: make(map[string]*Group, len(order))}

	sum := 0
	if weights == nil {
		for range order {
			sum += 1
		}
	} else {
		for _, n := range order {
			w := weights[n]
			if w <= 0 {
				w = 1
			}
			sum += w
		}
	}

	allocated := 0
	for i, n := range order {
		var t int
		if i == len(order)-1 {
			t = total - allocated // last group absorbs rounding
		} else {
			var w int
			if weights == nil {
				w = 1
			} else {
				w = weights[n]
				if w <= 0 {
					w = 1
				}
			}
			t = total * w / sum
		}
		if t < 1 {
			t = 1
		}
		allocated += t

		g := &Group{Name: n, jobs: make(chan func(), 4096)}
		for j := 0; j < t; j++ {
			g.wg.Add(1)
			go func() {
				defer g.wg.Done()
				for f := range g.jobs {
					f()
				}
			}()
		}
		d.Groups[n] = g
	}
	return d
}

// Submit queues a closure onto a group. Blocks if the queue is full.
func (d *Dispatcher) Submit(group string, fn func()) {
	g, ok := d.Groups[group]
	if !ok {
		go fn()
		return
	}
	g.jobs <- fn
}

// TrySubmit queues without blocking; runs inline if full.
func (d *Dispatcher) TrySubmit(group string, fn func()) {
	g, ok := d.Groups[group]
	if !ok {
		go fn()
		return
	}
	select {
	case g.jobs <- fn:
	default:
		fn()
	}
}

// Wait blocks until every group drains.
func (d *Dispatcher) Wait() {
	var wg sync.WaitGroup
	for _, g := range d.Groups {
		wg.Add(1)
		go func(g *Group) {
			defer wg.Done()
			close(g.jobs)
			g.wg.Wait()
		}(g)
	}
	wg.Wait()
}
